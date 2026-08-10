package render

import (
	"fmt"
	"html/template"
	"sort"
	"sync/atomic"
	"unsafe"
)

// Program is a compiled, cacheable template. Programs are immutable
// after Compile returns. Render never mutates a *Program; it only
// reads from the SoA node stream and writes to a ByteWriter.
//
// A Program may reference another Program through its Layout field.
// This mirrors GO-Portfolio's two-pass render where the layout and
// the inner view are cached separately and composed at render time.
//
// EngineData is an opaque blob for engines that need their own
// compiled representation (e.g. *template.Template for jade and the
// html/template engine). Plain engines (the default HTML adapter)
// leave this nil and use the Nodes stream via the SoA executor.
type Program struct {
	Engine  string
	Name    string
	Nodes   NodeStream
	Layout  *Program
	FuncMap map[string]any
	ETag    string
	Tags    []string

	// EngineData is the engine's compiled output. nil if the engine
	// uses the SoA executor. Engines that defer to html/template or
	// templ store their compiled *template.Template or templ.Component
	// here. The render path calls Engine.Execute which dispatches on
	// the type.
	EngineData any

	// Bytecode is the flat instruction stream compiled from Nodes
	// (see CompileBytecode). When present, Execute runs it instead of
	// the recursive tree walk — static HTML renders as coalesced
	// writes, matching templ's compile-to-output speed.
	Bytecode *Bytecode

	// engine-private scratch
	scratch []byte
}

// Engine is the contract every template engine implements. Engines
// own three things: name, compile, execute.
type Engine interface {
	Name() string
	Compile(src []byte, name string) (*Program, error)
	Execute(p *Program, w ByteWriter, rc *RenderContext, data any) error
}

// Sourceless is an optional interface an Engine implements to signal
// that Compile ignores the source bytes — the engine's programs come
// from elsewhere (e.g. templ components, which are pre-compiled to Go
// at build time and registered by name). The registry then does NOT
// require a Loader on the RenderContext for this engine.
type Sourceless interface {
	Engine
	NoSource()
}

// PageComposer is an optional interface an Engine implements to
// provide NATIVE layout composition. Engines that compose by nesting
// (templ's Layout(view) pattern) implement this; Registry.RenderPageWith
// routes to it instead of the string-injection (yield) path.
type PageComposer interface {
	Engine
	RenderPage(rc *RenderContext, w ByteWriter, layout, view string, data any) error
}

// Composition is a render request that composes a layout, view, and
// optional partials.
type Composition struct {
	Engine string
	Layout string
	View   string
	Parts  []string
}

// Registry is the user-facing façade. It holds the set of registered
// engines, the shared program cache, and the per-request features
// (injectors, FuncMap, variants, compression, conditional).
//
// All hot-path fields are accessed via atomic.Pointer for lock-free
// reads. Writes are CAS-loop copy-on-write, which is also lock-free.
// No RWMutex in the hot path.
type Registry struct {
	// engines is the live slice of registered engines. Reads via
	// atomic.Pointer.Load. Writes via copy-on-write CAS.
	engines atomic.Pointer[[]Engine]

	// byName is the engine-name → engine map. Atomic pointer to
	// a map; writes copy-on-write.
	byName atomic.Pointer[map[string]Engine]

	// cache is the shared program cache.
	cache atomic.Pointer[Cache]

	// injectors: per-request data injectors. CAS-loop append.
	injectors atomic.Pointer[[]DataInjector]

	// funcMapBuilder: per-request FuncMap factory. CAS pointer.
	funcMapBuilder atomic.Pointer[FuncMapBuilder]

	// variants: per-dimension value set. CAS pointer.
	variants atomic.Pointer[map[string]map[string]struct{}]

	// compress*: compression config. CAS pointer.
	compress atomic.Pointer[CompressConfig]

	// conditional: bit. 0 = off, 1 = on.
	conditional atomic.Uint32

	// debug: bit. 0 = off, 1 = on.
	debug atomic.Uint32

	// pipeline: custom Pipeline. nil → default.
	pipeline atomic.Pointer[Pipeline]

	// tags: registry-level tag tracking.
	// tag → set of (engine, name) pairs. Read-mostly; copied on write.
	tags atomic.Pointer[map[string]map[tagPair]struct{}]

	// components: the attached ComponentRegistry. nil when no
	// components are registered.
	components atomic.Pointer[ComponentRegistry]

	// minify: the active MinifyConfig. nil when minification is off.
	// Lock-free read via atomic.Pointer.
	minify atomic.Pointer[MinifyConfig]

	// defaultLoader: the registry-level loader used when a
	// RenderContext has no per-request loader. Set via
	// WithDefaultLoader. Lock-free read via atomic.Pointer.
	defaultLoader atomic.Pointer[SourceFunc]

	// preloads: registry-level <link rel="preload"> hints. The
	// <PRELOADS/> component tag emits these inside the layout's
	// <head>, so the browser fetches CSS / JS / images in parallel
	// with the shell's render. Lock-free read via atomic.Pointer;
	// writes are copy-on-write CAS.
	preloads atomic.Pointer[[]PreloadHint]
}

// NewRegistry returns a Registry with the given engines. The first
// engine is the default for unqualified Render calls.
//
// The PRELOADS component is registered automatically. It emits the
// registry-level preload hints as <link rel="preload"> tags when
// referenced in a layout (typically inside <head>).
func NewRegistry(engines ...Engine) *Registry {
	r := &Registry{}
	empty := []Engine{}
	r.engines.Store(&empty)
	emptyMap := map[string]Engine{}
	r.byName.Store(&emptyMap)
	emptyInjectors := []DataInjector{}
	r.injectors.Store(&emptyInjectors)
	emptyTags := map[string]map[tagPair]struct{}{}
	r.tags.Store(&emptyTags)
	for _, e := range engines {
		r.AddEngine(e)
	}
	// Register PRELOADS as a built-in component backed by this
	// registry. Users may override via AttachComponents if they
	// want custom emission.
	r.components.Store(NewComponentRegistry())
	r.components.Load().Register("PRELOADS", &preloadsComponent{r: r})
	// Register a default FuncMap builder so templates get the
	// standard helpers (useState, get, set, useContext) without
	// any opt-in. Users can call WithFuncMap to merge their own
	// helpers on top — see Registry.FuncMap for the merge logic.
	r.FuncMap(defaultFuncMap)
	return r
}

// defaultFuncMap returns the batteries-included template helper
// set: useState, get, set (for per-render state), and useContext
// (for cascading context). Users can register additional helpers
// via WithFuncMap; the merge happens in InjectFuncMap so all
// helpers stay visible to templates.
func defaultFuncMap(rc *RenderContext) template.FuncMap {
	return template.FuncMap{
		"useState": func(key string, initial any) any {
			if rc == nil || rc.state == nil {
				return initial
			}
			return rc.state.UseState(key, initial)
		},
		"get": func(key string) any {
			if rc == nil || rc.state == nil {
				return nil
			}
			v, _ := rc.state.Get(key)
			return v
		},
		"set": func(key string, val any) string {
			if rc == nil || rc.state == nil {
				return ""
			}
			rc.state.Set(key, val)
			return ""
		},
		"useContext": func(key string) any {
			if rc == nil {
				return nil
			}
			return rc.GetContext(key)
		},
	}
}

// Cache returns the shared program cache, lazily creating it.
func (r *Registry) Cache() *Cache {
	if c := r.cache.Load(); c != nil {
		return c
	}
	c := NewCache(1024)
	if r.cache.CompareAndSwap(nil, c) {
		return c
	}
	return r.cache.Load()
}

// SetCache replaces the shared program cache.
func (r *Registry) SetCache(c *Cache) { r.cache.Store(c) }

// Engines returns the live slice of registered engines. The slice
// is a copy; callers may mutate it.
func (r *Registry) Engines() []Engine {
	if p := r.engines.Load(); p != nil {
		return *p
	}
	return nil
}

// Engine returns the engine registered under the given name, or nil.
func (r *Registry) Engine(name string) Engine {
	if p := r.byName.Load(); p != nil {
		return (*p)[name]
	}
	return nil
}

// Define returns a fluent builder for a component. The component is
// registered with the ComponentRegistry when Register is called.
//
// Usage:
//
//	cr.Define("NAVBAR").
//	    WithFuncs(template.FuncMap{"currentUser": ...}).
//	    RenderChildren(func(c *render.ComponentContext) error {
//	        c.WriteString("<nav>")
//	        c.WriteChildren()
//	        c.WriteString("</nav>")
//	        return nil
//	    }).
//	    Register(cr)
func (r *Registry) Define(name string) *Definition {
	return &Definition{name: name}
}

// Default returns the first registered engine, or nil if none.
func (r *Registry) Default() Engine {
	engines := r.Engines()
	if len(engines) == 0 {
		return nil
	}
	return engines[0]
}

// AddEngine registers an engine. Copy-on-write CAS.
func (r *Registry) AddEngine(e Engine) *Registry {
	if e == nil {
		return r
	}
	for {
		oldEngines := r.engines.Load()
		newEngines := make([]Engine, len(*oldEngines)+1)
		copy(newEngines, *oldEngines)
		newEngines[len(*oldEngines)] = e
		if r.engines.CompareAndSwap(oldEngines, &newEngines) {
			break
		}
	}
	for {
		oldByName := r.byName.Load()
		newByName := make(map[string]Engine, len(*oldByName)+1)
		for k, v := range *oldByName {
			newByName[k] = v
		}
		newByName[e.Name()] = e
		if r.byName.CompareAndSwap(oldByName, &newByName) {
			break
		}
	}
	return r
}

// Part returns the compiled program for the given template, fetching
// it from the cache (or compiling on miss).
func (r *Registry) Part(rc *RenderContext, engine, name string) (*Program, error) {
	return r.loadPart(r.Engine(engine), engine, name, rc)
}

// PreloadParts ensures each named part is in the cache.
func (r *Registry) PreloadParts(rc *RenderContext, engine string, parts ...string) error {
	eng := r.Engine(engine)
	if eng == nil {
		return fmt.Errorf("%w: %q", ErrEngineNotFound, engine)
	}
	for _, name := range parts {
		if _, err := r.loadPart(eng, engine, name, rc); err != nil {
			return err
		}
	}
	return nil
}

// MustPreloadParts is the boot-time version of PreloadParts. It
// panics with the underlying error if any part fails to load. Use
// this in main() to fail loudly at startup when a template is
// missing or misnamed.
//
// The panic value is the wrapped error, so recover sites can
// introspect via errors.Is / errors.As:
//
//	defer func() {
//	    if r := recover(); r != nil {
//	        if err, ok := r.(error); ok {
//	            switch {
//	            case errors.Is(err, render.ErrEngineNotFound):
//	                // missing engine
//	            case errors.Is(err, render.ErrTemplateNotFound):
//	                // missing template file
//	            }
//	        }
//	    }
//	}()
//	reg.MustPreloadParts(rc, "jade", "layouts/app", "posts/show")
func (r *Registry) MustPreloadParts(rc *RenderContext, engine string, parts ...string) {
	if err := r.PreloadParts(rc, engine, parts...); err != nil {
		panic(err)
	}
}

// loadPart is the shared cache-lookup-with-fallback path.
func (r *Registry) loadPart(eng Engine, engine, name string, rc *RenderContext) (*Program, error) {
	if eng == nil {
		return nil, fmt.Errorf("%w: %q", ErrEngineNotFound, engine)
	}
	cache := r.Cache()
	if p, ok := cache.Get(engine, name); ok {
		return p, nil
	}
	// Resolve source: use the loader if present; Sourceless engines
	// (e.g. templ) never load — Compile ignores the bytes entirely.
	_, sourceless := eng.(Sourceless)
	var src []byte
	if rc != nil && rc.Loader != nil && !sourceless {
		var err error
		src, err = rc.Loader(name)
		if err != nil {
			return nil, fmt.Errorf("%w %q: %w", ErrTemplateNotFound, name, err)
		}
	} else if !sourceless {
		return nil, fmt.Errorf("%w: %q", ErrLoaderMissing, name)
	}
	p, err := eng.Compile(src, name)
	if err != nil {
		return nil, fmt.Errorf("render: compile %q: %w", name, err)
	}
	cache.Put(engine, name, p)
	return p, nil
}

// Render fetches the named program from the cache (compiling on
// miss) and executes it against the given data.
func (r *Registry) Render(rc *RenderContext, engine, name string, data any) error {
	return r.RenderNamed(rc, EngineName(engine), name, data)
}

// RenderNamed is the typed variant of Render. It takes an EngineName
// instead of a raw string, so callers use the constants or
// CustomEngine(name) — a typo'd name is a compile error.
func (r *Registry) RenderNamed(rc *RenderContext, engine EngineName, name string, data any) error {
	if err := r.renderNamed(rc, engine.String(), name, data); err != nil {
		return err
	}
	// Drain any perFlushWriter / pooled writer so the bytes are
	// visible. RenderStream manages its own flushing; this is for
	// the one-shot Render path so callers don't have to remember.
	if rc != nil {
		if fw, ok := rc.Writer.(interface{ Flush() error }); ok {
			return fw.Flush()
		}
	}
	return nil
}

func (r *Registry) renderNamed(rc *RenderContext, engine, name string, data any) error {
	eng := r.Engine(engine)
	if eng == nil {
		return fmt.Errorf("%w: %q", ErrEngineNotFound, engine)
	}
	r.InjectFuncMap(rc, data)
	if d := rc.UserData(); d != nil {
		data = d
	}
	// Auto-populate the per-request ComponentRegistry from the
	// registry's built-ins (e.g. PRELOADS) when the caller hasn't
	// already attached one. Users who call AttachComponents with
	// their own registry override this.
	if rc.ComponentRegistry() == nil {
		if cr := r.Components(); cr != nil {
			rc.SetComponentRegistry(cr)
		}
	}
	p, err := r.loadPart(eng, engine, name, rc)
	if err != nil {
		return err
	}
	if r.conditional.Load() != 0 && CheckConditional(rc, p) {
		return nil
	}
	return eng.Execute(p, rc.Writer, rc, data)
}

// RenderComposition renders a layout + view + partials composition.
func (r *Registry) RenderComposition(rc *RenderContext, c Composition, data any) error {
	if c.Engine == "" {
		return fmt.Errorf("%w: missing Engine", ErrRenderPageInvalid)
	}
	eng := r.Engine(c.Engine)
	if eng == nil {
		return fmt.Errorf("%w: %q", ErrEngineNotFound, c.Engine)
	}
	r.InjectFuncMap(rc, data)
	if d := rc.UserData(); d != nil {
		data = d
	}

	for _, name := range c.Parts {
		if _, err := r.loadPart(eng, c.Engine, name, rc); err != nil {
			return err
		}
	}

	view, err := r.loadPart(eng, c.Engine, c.View, rc)
	if err != nil {
		return err
	}

	if c.Layout == "" {
		if r.conditional.Load() != 0 && CheckConditional(rc, view) {
			return nil
		}
		return eng.Execute(view, rc.Writer, rc, data)
	}

	layout, err := r.loadPart(eng, c.Engine, c.Layout, rc)
	if err != nil {
		return err
	}
	view.Layout = layout
	if r.conditional.Load() != 0 && CheckConditional(rc, view) {
		return nil
	}
	return eng.Execute(view, rc.Writer, rc, data)
}

// InjectFuncMap runs all registered injectors and the FuncMap
// factory. Lock-free reads via atomic.Pointer.
func (r *Registry) InjectFuncMap(rc *RenderContext, data any) {
	if fm := r.funcMapBuilder.Load(); fm != nil {
		rc.WithFuncMap((*fm)(rc))
	}
	if p := r.injectors.Load(); p != nil && len(*p) > 0 {
		switch d := data.(type) {
		case map[string]any:
			for _, fn := range *p {
				fn(rc, d)
			}
		default:
			wrap := map[string]any{"_data": data}
			for _, fn := range *p {
				fn(rc, wrap)
			}
			rc.SetUserData(wrap)
		}
	}
}

// Inject registers a DataInjector. Copy-on-write CAS.
func (r *Registry) Inject(in DataInjector) {
	for {
		old := r.injectors.Load()
		var newList []DataInjector
		if old != nil {
			// make with len 0, cap len+1; append grows it to exactly
			// len+1 with the new injector at the end — no nil hole.
			newList = make([]DataInjector, 0, len(*old)+1)
			newList = append(newList, (*old)...)
		} else {
			newList = make([]DataInjector, 0, 4)
		}
		newList = append(newList, in)
		if r.injectors.CompareAndSwap(old, &newList) {
			return
		}
	}
}

// FuncMap registers a per-request FuncMap factory.
func (r *Registry) FuncMap(b FuncMapBuilder) {
	r.funcMapBuilder.Store(&b)
}

// Variant declares a variant dimension with allowed values.
func (r *Registry) Variant(name string, values ...string) {
	for {
		old := r.variants.Load()
		var newMap map[string]map[string]struct{}
		if old != nil {
			newMap = make(map[string]map[string]struct{}, len(*old)+1)
			for k, v := range *old {
				newMap[k] = v
			}
		} else {
			newMap = make(map[string]map[string]struct{}, 1)
		}
		set := newMap[name]
		if set == nil {
			set = make(map[string]struct{}, len(values))
		}
		for _, v := range values {
			set[v] = struct{}{}
		}
		newMap[name] = set
		if r.variants.CompareAndSwap(old, &newMap) {
			return
		}
	}
}

// SetVariant records a variant for a key in the registry tag table.
func (r *Registry) SetVariant(dim, value string) { r.Variant(dim, value) }

// Compress enables gzip with the given config.
func (r *Registry) Compress(cfg CompressConfig) *Registry {
	if cfg.Level == 0 {
		cfg.Level = 6
	}
	if cfg.MinSize == 0 {
		cfg.MinSize = 1024
	}
	if len(cfg.Types) == 0 {
		cfg.Types = []string{"text/html", "application/json"}
	}
	r.compress.Store(&cfg)
	return r
}

// Conditional enables ETag/304.
func (r *Registry) Conditional(enabled bool) *Registry {
	if enabled {
		r.conditional.Store(1)
	} else {
		r.conditional.Store(0)
	}
	return r
}

// SetPipeline installs a custom Pipeline.
func (r *Registry) SetPipeline(p *Pipeline) { r.pipeline.Store(p) }

// SetDefaultLoader sets the registry-level loader. Lock-free;
// concurrent readers see the previous value until the next store.
func (r *Registry) SetDefaultLoader(loader SourceFunc) {
	r.defaultLoader.Store(&loader)
}

// DefaultLoader returns the registry-level loader, or nil if none
// is set. Callers (notably the nano adapter) use this to populate
// the per-request RenderContext.Loader. Lock-free atomic load.
func (r *Registry) DefaultLoader() SourceFunc {
	p := r.defaultLoader.Load()
	if p == nil {
		return nil
	}
	return *p
}

// loadPartKey was removed when the cache API changed to take
// (engine, name) pairs. Variants are now applied via the cache's
// internal hash; no string concat is performed on the hot path.

// variantKey returns the cache key suffix for the active variants.
// Used by engine adapters that need to re-resolve the template name
// with variant suffixes (e.g. "layouts/application.fr.jade").
//
// The returned string is a view of rc.varKeyBuf via unsafe.String.
// The caller must use the result before releasing rc to the pool.
// Zero allocations on the steady-state hot path.
func (r *Registry) variantKey(rc *RenderContext) string {
	if len(rc.Variants) == 0 {
		return ""
	}
	buf := rc.varKeyBuf[:0]
	// Sort variant keys for a stable cache key. Stack-allocated
	// buffer avoids the keys slice alloc; cap at 8 dimensions.
	var keysBuf [8]string
	n := 0
	for k := range rc.Variants {
		if n >= len(keysBuf) {
			break
		}
		keysBuf[n] = k
		n++
	}
	keys := keysBuf[:n]
	sort.Strings(keys)
	for i, k := range keys {
		if i > 0 {
			buf = append(buf, '@')
		}
		buf = append(buf, k...)
		buf = append(buf, '.')
		buf = append(buf, rc.Variants[k]...)
	}
	rc.varKeyBuf = buf
	return unsafe.String(&buf[0], len(buf))
}

// variantName returns the rewritten template name with the variant
// dimensions appended. Engines call this to resolve the variant before
// compile.
//
// The returned string is a view of rc.varKeyBuf via unsafe.String.
// The caller must use the result before releasing rc to the pool.
// Zero allocations on the steady-state hot path.
func (r *Registry) variantName(name string, rc *RenderContext) string {
	if len(rc.Variants) == 0 {
		return name
	}
	buf := rc.varKeyBuf[:0]
	buf = append(buf, name...)
	// Sort variant keys.
	var keysBuf [8]string
	n := 0
	for k := range rc.Variants {
		if n >= len(keysBuf) {
			break
		}
		keysBuf[n] = k
		n++
	}
	keys := keysBuf[:n]
	sort.Strings(keys)
	for _, k := range keys {
		buf = append(buf, '.')
		buf = append(buf, rc.Variants[k]...)
	}
	rc.varKeyBuf = buf
	return unsafe.String(&buf[0], len(buf))
}

// tagPair is a (engine, name) tuple used as the tag-set key. It
// avoids the string round-trip when inlining the cache key.
type tagPair struct {
	engine string
	name   string
}

// SetTag associates a (engine, name) pair with one or more tags.
// Tags are used by InvalidateTag for category-based eviction.
func (r *Registry) SetTag(engine, name string, tags ...string) {
	pair := tagPair{engine: engine, name: name}
	for {
		old := r.tags.Load()
		var newMap map[string]map[tagPair]struct{}
		if old != nil {
			newMap = make(map[string]map[tagPair]struct{}, len(*old)+len(tags))
			for k, v := range *old {
				newMap[k] = v
			}
		} else {
			newMap = make(map[string]map[tagPair]struct{}, len(tags))
		}
		for _, t := range tags {
			set, ok := newMap[t]
			if !ok {
				set = make(map[tagPair]struct{})
				newMap[t] = set
			}
			set[pair] = struct{}{}
		}
		if r.tags.CompareAndSwap(old, &newMap) {
			return
		}
	}
}

// InvalidateTag drops every cached (engine, name) pair associated
// with the given tag. This is a cold path (boot, hot-reload, ops
// tool calls) — the loop body is O(n) over the tagged set.
func (r *Registry) InvalidateTag(tag string) int {
	m := r.tags.Load()
	if m == nil {
		return 0
	}
	keys := (*m)[tag]
	n := len(keys)
	cache := r.Cache()
	for k := range keys {
		cache.Delete(k.engine, k.name)
	}
	// Clear from the tag map.
	for {
		old := r.tags.Load()
		newMap := make(map[string]map[tagPair]struct{}, len(*old))
		for k, v := range *old {
			if k == tag {
				continue
			}
			newMap[k] = v
		}
		if r.tags.CompareAndSwap(old, &newMap) {
			break
		}
	}
	return n
}

// RenderComponent dispatches a named, registered component directly
// to w, bypassing any parent template or layout. This is the entry
// point for HTMX-style targeted swaps: a handler receives an
// `hx-get="/posts/123/card"` request and renders JUST the CARD
// component without re-rendering the surrounding page.
//
// The component is resolved via the registry's ComponentRegistry
// (the same path the html/template `{{ component "Name" . }}`
// superpower uses). The lookup follows rc.ComponentRegistry() first
// (so per-request attachment via SetComponentRegistry wins), then
// the registry's built-in components.
//
// Returns nil (no error) when the name is not registered — the same
// semantics as the html/template superpower. Missing components are
// not a render error; they are a configuration gap the caller may
// or may not care about.
func (r *Registry) RenderComponent(w ByteWriter, rc *RenderContext, name string, props any) error {
	cr := rc.ComponentRegistry()
	if cr == nil {
		cr = r.Components()
		if cr == nil {
			return nil
		}
		rc.SetComponentRegistry(cr)
	}
	c, ok := cr.Lookup(name)
	if !ok {
		return nil
	}
	if err := renderComponent(c, w, rc, props, nil); err != nil {
		return err
	}
	// Drain any perFlushWriter / pooled writer so the bytes are
	// visible to the underlying io.Writer. Without this, small
	// components (below the perFlushWriter threshold) stay
	// buffered until the caller flushes manually.
	if fw, ok := w.(interface{ Flush() error }); ok {
		return fw.Flush()
	}
	return nil
}
