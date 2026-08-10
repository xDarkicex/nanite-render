module github.com/xDarkicex/nanite-render

go 1.25.7

require (
	github.com/Joker/jade v1.1.3
	github.com/a-h/templ v0.3.1020
	github.com/fsnotify/fsnotify v1.7.0
	github.com/tdewolff/minify v2.3.6+incompatible
	github.com/xDarkicex/memory v1.2.2
	github.com/xDarkicex/nanite v0.5.7
)

require (
	github.com/tdewolff/parse v2.3.4+incompatible // indirect
	github.com/tdewolff/test v1.0.12 // indirect
	golang.org/x/sys v0.43.0 // indirect
)

// Local development of the SWAR lexer primitives and the LRU cache.
replace github.com/xDarkicex/lexer => ../lexer

replace github.com/xDarkicex/liteLRU => ../liteLRU
