# Changelog

All notable changes to `nanite-render` are recorded here.

## [unreleased]

Initial scaffold.

* SoA `NodeStream` and `NodeStreamBuilder` for cache-local walks.
* `Pipeline` with pluggable `Stage` chain.
* `Engine` interface plus `templ`, `joker-jade`, `html/template` adapters.
* Sharded `Cache` keyed by content hash.
* Pooled `ByteWriter` and `RenderContext`.
* `Watcher` (fsnotify-based) for hot-reload of templates.
* `nano.Middleware` adapter for `nanite` routes.
