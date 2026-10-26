// Package parser turns page images into structured pages. It is a pure
// library: it knows nothing about the database, the queue or HTTP (§3.3).
//
// Engines (OCR back-ends) return a RawPage; package assemble turns a RawPage
// into a types.ParsedPage with reading order, markdown and offsets.
package parser

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/thanhenti/bepaylot/docpipe/types"
)

// PageImage is the input of an engine: one rendered page as JPEG.
type PageImage struct {
	PageNo int
	// Body streams the JPEG bytes; Size is its length (-1 when unknown).
	Body   io.Reader
	Size   int64
	Width  int
	Height int
}

// PageOptions toggles optional engine features.
type PageOptions struct {
	Layout, ReadingOrder, Tables, Formulas bool
	// Autorotate asks the OCR engine to turn a 90/180/270° page upright
	// before reading it.
	Autorotate bool
	// Refine lets a refining engine (turboocr_vlm) transcribe layout regions
	// with a VLM. The pipeline turns it off for pages whose PDF text layer is
	// already good enough.
	Refine bool
}

// RawLine is one OCR text line.
type RawLine struct {
	ID         int
	Text       string
	Confidence float64
	Quad       types.Quad
	// LayoutID is the id of the region the engine assigned, or -1.
	LayoutID int
}

// RawRegion is one layout region.
type RawRegion struct {
	ID         int
	Class      string
	Confidence float64
	Quad       types.Quad
	HTML       string // tables
	LaTeX      string // formulas
	// Text is a refined transcription of the region (markdown, set by a
	// refining engine); empty when the region was not refined.
	Text string
}

// RawPage is an engine's normalized, not-yet-assembled output.
type RawPage struct {
	Width, Height int
	Lines         []RawLine
	Regions       []RawRegion
	// ReadingOrder lists RawLine.IDs in reading order; may be empty.
	ReadingOrder []int
	// Markdown is the engine's own page markdown, verbatim (turboocr_vlm:
	// the VLM's reply); empty when the engine has none.
	Markdown string
	// Raw is the engine's original response, kept for debugging/reparse.
	Raw []byte
}

// Engine is an OCR/layout back-end.
type Engine interface {
	Name() string
	ParsePage(ctx context.Context, in PageImage, opt PageOptions) (*RawPage, error)
	Health(ctx context.Context) error
}

// EngineInfo describes a registered engine.
type EngineInfo struct {
	Name      string `json:"name"`
	Default   bool   `json:"default"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}

// Registry resolves engines by name.
type Registry struct {
	mu      sync.RWMutex
	engines map[string]Engine
	def     string
}

// NewRegistry builds a registry with a default engine name.
func NewRegistry(def string) *Registry {
	return &Registry{engines: map[string]Engine{}, def: def}
}

// Register adds or replaces an engine.
func (r *Registry) Register(e Engine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.engines[e.Name()] = e
}

// Get returns the engine by name; empty name returns the default.
func (r *Registry) Get(name string) (Engine, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if name == "" {
		name = r.def
	}
	e, ok := r.engines[name]
	if !ok {
		return nil, fmt.Errorf("parser: engine %q is not registered", name)
	}
	return e, nil
}

// DefaultName returns the default engine name.
func (r *Registry) DefaultName() string { return r.def }

// List reports every engine with a health probe.
func (r *Registry) List(ctx context.Context) []EngineInfo {
	r.mu.RLock()
	names := make([]string, 0, len(r.engines))
	for n := range r.engines {
		names = append(names, n)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	out := make([]EngineInfo, 0, len(names))
	for _, n := range names {
		e, _ := r.Get(n)
		info := EngineInfo{Name: n, Default: n == r.def, Available: true}
		if err := e.Health(ctx); err != nil {
			info.Available, info.Error = false, err.Error()
		}
		out = append(out, info)
	}
	return out
}
