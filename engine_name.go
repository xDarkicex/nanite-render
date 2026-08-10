package render

// EngineName is a compile-time-checked engine identifier. It is a
// string underneath — the same value used for cache keys and the
// byName lookup — but the distinct type rejects arbitrary strings
// at the call site. A typo'd `"jad"` is a compile error; `EngineJade`
// is not.
//
// Prefer the constants for the built-in engines:
//
//	reg.Page(rc).
//	    Engine(EngineJade).
//	    Layout("layouts/app").
//	    View("posts/show").
//	    Render()
//
// Custom engines use CustomEngine(name):
//
//	reg.Page(rc).
//	    Engine(CustomEngine("my-engine")).
//	    View("home").
//	    Render()
type EngineName string

// Built-in engine names.
const (
	EngineJade         EngineName = "jade"
	EngineHTML         EngineName = "html"
	EngineHTMLTemplate EngineName = "html-template"
	EngineTempl        EngineName = "templ"
)

// CustomEngine returns the EngineName for a user-defined engine.
// Because EngineName is a string type, custom engines may use any
// name they like — this is the escape hatch for the built-in set.
//
//	myEng := &MyEngine{}
//	reg.AddEngine(myEng)
//	reg.Page(rc).Engine(CustomEngine(myEng.Name())).View("home").Render()
func CustomEngine(name string) EngineName { return EngineName(name) }

// String returns the raw engine name (cache key, byName lookup).
func (n EngineName) String() string { return string(n) }
