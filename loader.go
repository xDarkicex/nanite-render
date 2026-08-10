package render

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// NewFileLoader returns a SourceFunc that reads templates from the
// given root directory. Names are resolved as root + "/" + name + ext.
func NewFileLoader(root, ext string) SourceFunc {
	return func(name string) ([]byte, error) {
		path := root + "/" + name
		if !strings.HasSuffix(path, ext) {
			path += ext
		}
		return os.ReadFile(path)
	}
}

// NewPrefixLoader returns a SourceFunc that prepends prefix to name and
// reads the resulting path. Useful for embedding.
func NewPrefixLoader(prefix string, fs interface {
	Open(name string) (io.ReadCloser, error)
}) SourceFunc {
	return func(name string) ([]byte, error) {
		f, err := fs.Open(prefix + name)
		if err != nil {
			return nil, fmt.Errorf("render: open %s: %w", name, err)
		}
		defer f.Close()
		return io.ReadAll(f)
	}
}

// NewMapLoader returns a SourceFunc that serves templates from an
// in-memory map. Useful for tests and small embedded sets.
func NewMapLoader(templates map[string][]byte) SourceFunc {
	return func(name string) ([]byte, error) {
		if b, ok := templates[name]; ok {
			return b, nil
		}
		return nil, fmt.Errorf("render: %q not found", name)
	}
}
