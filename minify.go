package render

import (
	"bytes"

	"github.com/tdewolff/minify"
	"github.com/tdewolff/minify/html"
)

// MinifyConfig controls HTML minification at compile time. The
// original GO-Portfolio used tdewolff/minify per-render; we apply
// it once at compile time so the cache stores the minified output.
type MinifyConfig struct {
	KeepDefaultAttrVals     bool
	KeepWhitespace          bool
	KeepDocumentTags        bool
	KeepEndTags             bool
	KeepConditionalComments bool
}

// MinifyHTML minifies the given HTML according to the config. Used
// by engines at compile time to produce the smallest possible
// cached program.
func MinifyHTML(src []byte, cfg MinifyConfig) ([]byte, error) {
	m := minify.New()
	m.Add("text/html", &html.Minifier{
		KeepDefaultAttrVals:     cfg.KeepDefaultAttrVals,
		KeepWhitespace:          cfg.KeepWhitespace,
		KeepDocumentTags:        cfg.KeepDocumentTags,
		KeepEndTags:             cfg.KeepEndTags,
		KeepConditionalComments: cfg.KeepConditionalComments,
	})
	var out bytes.Buffer
	if err := m.Minify("text/html", &out, bytes.NewReader(src)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// WithMinify attaches a MinifyConfig to the Registry. Engines that
// support minification (jade, html/template) check this and apply
// it in their Compile step. Lock-free.
func (r *Registry) WithMinify(cfg MinifyConfig) *Registry {
	r.minify.Store(&cfg)
	return r
}

// Minify reports the active MinifyConfig, or nil. Lock-free.
func (r *Registry) Minify() *MinifyConfig {
	return r.minify.Load()
}
