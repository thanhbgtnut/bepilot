package types

// ClassLabel is one allowed document type.
type ClassLabel struct {
	Name        string `json:"name" yaml:"name"`
	Title       string `json:"title" yaml:"title"`
	Description string `json:"description,omitempty" yaml:"description"`
}

// Built-in labels.
const (
	LabelOther   = "other"
	LabelUnknown = "unknown"
)

// PageDocType is the document type the Decision API gave one page.
type PageDocType struct {
	PageNo     int
	Label      string // "" = not classified
	Confidence float64
	// FirstPage is the Decision API's is_first_page: the page is the first
	// page of a document, not the continuation of the one before it.
	FirstPage bool
}

// Text sources for pages and lines.
const (
	TextSourceOCR       = "ocr"
	TextSourceLayer     = "layer"
	TextSourceMerged    = "merged"
	TextSourceLayerOnly = "layer_only"
	// TextSourceVLM marks text transcribed by a vision-language model from a
	// cropped layout region (engine turboocr_vlm).
	TextSourceVLM = "vlm"
)
