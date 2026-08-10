package render

// Option configures a Registry. Apply via New(WithEngine(...),
// WithCompression(...), etc.) or via the fluent builder
// (reg.WithEngine(...).WithCompression(...)).
type Option func(*Registry)

// New returns a Registry configured by the given options.
func New(opts ...Option) *Registry {
	r := NewRegistry()
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// WithEngine registers an engine.
func WithEngine(e Engine) Option {
	return func(r *Registry) { r.AddEngine(e) }
}

// WithEngines registers multiple engines.
func WithEngines(engines ...Engine) Option {
	return func(r *Registry) {
		for _, e := range engines {
			r.AddEngine(e)
		}
	}
}

// WithCache replaces the default cache with a custom one.
func WithCache(c *Cache) Option {
	return func(r *Registry) { r.SetCache(c) }
}

// WithCacheSize sets the default cache capacity.
func WithCacheSize(n int) Option {
	return func(r *Registry) { r.SetCache(NewCache(n)) }
}

// WithCompression enables gzip compression.
func WithCompression(level, minSize int) Option {
	return func(r *Registry) {
		r.Compress(CompressConfig{Level: level, MinSize: minSize})
	}
}

// WithConditional enables ETag/304 handling.
func WithConditional() Option {
	return func(r *Registry) { r.Conditional(true) }
}

// WithVariant declares a variant dimension with allowed values.
func WithVariant(name string, values ...string) Option {
	return func(r *Registry) { r.Variant(name, values...) }
}

// WithInjector registers a DataInjector.
func WithInjector(fn DataInjector) Option {
	return func(r *Registry) { r.Inject(fn) }
}

// WithFuncMap registers a FuncMapBuilder.
func WithFuncMap(fn FuncMapBuilder) Option {
	return func(r *Registry) { r.FuncMap(fn) }
}

// WithDebug enables the Debug pipeline.
func WithDebug() Option {
	return func(r *Registry) { r.debug.Store(1) }
}

// WithPipeline installs a custom Pipeline.
func WithPipeline(p *Pipeline) Option {
	return func(r *Registry) { r.SetPipeline(p) }
}

// WithDefaultLoader sets the registry-level loader. Used when a
// RenderContext has no per-request loader. This is the typical boot
// configuration — one loader for the whole app.
func WithDefaultLoader(loader SourceFunc) Option {
	return func(r *Registry) { r.SetDefaultLoader(loader) }
}

// Chainable aliases for fluent builder.

// WithEngine (alias) is exposed via Registry.WithEngine below.

// Inject (alias) is exposed via Registry.Inject below.
