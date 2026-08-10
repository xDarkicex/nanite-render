package render

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Watcher is an fsnotify-based hot-reload handler. It watches a set
// of template roots and emits invalidation events to a channel. The
// program cache subscribes to the channel and evicts matching keys
// when an event fires.
//
// Hot-reload is enabled by default; production builds can disable it
// at construction time.
type Watcher struct {
	fs       *fsnotify.Watcher
	roots    []string
	events   chan Event
	stop     chan struct{}
	mu       sync.Mutex
	disabled bool
}

// Event is a single invalidation signal sent to subscribers.
type Event struct {
	// Path is the file path that triggered the event, relative to
	// the watched root.
	Path string
	// Op is the fsnotify op (Create, Write, Remove, Rename, Chmod).
	Op fsnotify.Op
}

// NewWatcher returns a Watcher over the given roots. The returned
// watcher is started; call Close to shut it down.
func NewWatcher(roots ...string) (*Watcher, error) {
	if len(roots) == 0 {
		return &Watcher{disabled: true}, nil
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		fs:     fw,
		roots:  roots,
		events: make(chan Event, 64),
		stop:   make(chan struct{}),
	}
	for _, r := range roots {
		if err := fw.Add(r); err != nil {
			_ = fw.Close()
			return nil, err
		}
	}
	go w.loop()
	return w, nil
}

// Events returns the channel of invalidation events.
func (w *Watcher) Events() <-chan Event {
	if w == nil {
		return nil
	}
	return w.events
}

// Close shuts the watcher down.
func (w *Watcher) Close() error {
	if w == nil || w.fs == nil {
		return nil
	}
	close(w.stop)
	return w.fs.Close()
}

// Disabled reports whether the watcher is a no-op.
func (w *Watcher) Disabled() bool { return w == nil || w.disabled }

// loop reads events from fsnotify and forwards them to subscribers.
func (w *Watcher) loop() {
	defer close(w.events)
	for {
		select {
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			w.emit(ev)
		case <-w.stop:
			return
		}
	}
}

// emit publishes an event to the channel, dropping on backpressure.
func (w *Watcher) emit(ev fsnotify.Event) {
	if w == nil || w.disabled {
		return
	}
	rel, err := filepath.Rel(w.roots[0], ev.Name)
	if err != nil {
		rel = ev.Name
	}
	// Strip the extension for cache-key matching.
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	select {
	case w.events <- Event{Path: rel, Op: ev.Op}:
	default:
		// Drop on backpressure. The cache will naturally recompile
		// on the next miss; dropped events are not data-loss.
	}
}
