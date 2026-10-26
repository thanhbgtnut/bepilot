package types

// BBox is an axis-aligned box in pixels of the rendered page image.
type BBox struct {
	X0 float64 `json:"x0"`
	Y0 float64 `json:"y0"`
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
}

// Width of the box.
func (b BBox) Width() float64 { return b.X1 - b.X0 }

// Height of the box.
func (b BBox) Height() float64 { return b.Y1 - b.Y0 }

// Union returns the smallest box containing both. A zero box is ignored.
func (b BBox) Union(o BBox) BBox {
	if b.IsZero() {
		return o
	}
	if o.IsZero() {
		return b
	}
	return BBox{X0: min(b.X0, o.X0), Y0: min(b.Y0, o.Y0), X1: max(b.X1, o.X1), Y1: max(b.Y1, o.Y1)}
}

// IsZero reports whether the box is unset.
func (b BBox) IsZero() bool { return b.X0 == 0 && b.Y0 == 0 && b.X1 == 0 && b.Y1 == 0 }

// Contains reports whether the point lies inside the box.
func (b BBox) Contains(x, y float64) bool { return x >= b.X0 && x <= b.X1 && y >= b.Y0 && y <= b.Y1 }

// Array returns [x0,y0,x1,y1].
func (b BBox) Array() []float64 { return []float64{b.X0, b.Y0, b.X1, b.Y1} }

// BBoxFromArray builds a box from [x0,y0,x1,y1]; short input yields a zero box.
func BBoxFromArray(a []float64) BBox {
	if len(a) < 4 {
		return BBox{}
	}
	return BBox{X0: a[0], Y0: a[1], X1: a[2], Y1: a[3]}
}

// Quad is the four-point polygon returned by OCR (TL, TR, BR, BL).
type Quad [4][2]float64

// BBox returns the axis-aligned bounds of the quad.
func (q Quad) BBox() BBox {
	b := BBox{X0: q[0][0], Y0: q[0][1], X1: q[0][0], Y1: q[0][1]}
	for _, p := range q[1:] {
		b.X0, b.Y0 = min(b.X0, p[0]), min(b.Y0, p[1])
		b.X1, b.Y1 = max(b.X1, p[0]), max(b.Y1, p[1])
	}
	return b
}

// Flat returns the 8 coordinates in order.
func (q Quad) Flat() []float64 {
	return []float64{q[0][0], q[0][1], q[1][0], q[1][1], q[2][0], q[2][1], q[3][0], q[3][1]}
}

// QuadFromFlat is the inverse of Flat.
func QuadFromFlat(a []float64) Quad {
	var q Quad
	for i := 0; i < 4 && 2*i+1 < len(a); i++ {
		q[i] = [2]float64{a[2*i], a[2*i+1]}
	}
	return q
}

// QuadFromBBox builds a rectangle quad.
func QuadFromBBox(b BBox) Quad {
	return Quad{{b.X0, b.Y0}, {b.X1, b.Y0}, {b.X1, b.Y1}, {b.X0, b.Y1}}
}

// BlockType is the normalized layout class (§5.3).
type BlockType string

const (
	BlockTitle      BlockType = "title"
	BlockHeading    BlockType = "heading"
	BlockParagraph  BlockType = "paragraph"
	BlockTable      BlockType = "table"
	BlockFormula    BlockType = "formula"
	BlockFigure     BlockType = "figure"
	BlockCaption    BlockType = "caption"
	BlockHeader     BlockType = "header"
	BlockFooter     BlockType = "footer"
	BlockPageNumber BlockType = "page_number"
	BlockFootnote   BlockType = "footnote"
	BlockUnknown    BlockType = "unknown"
)

// ParsedPage is the structured result of parsing one page (§5.4).
type ParsedPage struct {
	PageNo      int           `json:"page_no"`
	Width       int           `json:"width"`
	Height      int           `json:"height"`
	DPI         float64       `json:"dpi"`
	Rotation    int           `json:"rotation"`
	Engine      string        `json:"engine"`
	TextSource  string        `json:"text_source"`
	TextQuality float64       `json:"text_quality"`
	IsBlank     bool          `json:"is_blank"`
	Blocks      []ParsedBlock `json:"blocks"`
	Lines       []ParsedLine  `json:"lines"`
	Markdown    string        `json:"markdown"`
	// MarkdownVLM is the VLM's page markdown, verbatim (RawPage.Markdown);
	// Markdown is rebuilt from the blocks and carries the line offsets.
	MarkdownVLM string `json:"markdown_vlm,omitempty"`
	// DocMdOffset is the rune offset of this page's markdown in the
	// full-document markdown (set once the document is assembled).
	DocMdOffset int `json:"doc_md_offset"`
}

// ParsedBlock is one layout region in reading order.
type ParsedBlock struct {
	BlockNo     int       `json:"block_no"`
	SourceID    int       `json:"source_id"`
	Type        BlockType `json:"type"`
	RawClass    string    `json:"raw_class"`
	Confidence  float64   `json:"confidence"`
	BBox        BBox      `json:"bbox"`
	Text        string    `json:"text"`
	HTML        string    `json:"html,omitempty"`
	LaTeX       string    `json:"latex,omitempty"`
	AssetKey    string    `json:"asset_key,omitempty"`
	IsFurniture bool      `json:"is_furniture"`
	// TextSource is "vlm" when Text is a VLM transcription of the region that
	// is rendered verbatim as the block's markdown; empty otherwise (Text is
	// then the block's lines joined).
	TextSource string `json:"text_source,omitempty"`
	MdStart    int    `json:"md_start"`
	MdEnd      int    `json:"md_end"`
}

// ParsedLine is one text line in reading order. Offsets are in runes of the
// page markdown; -1 when the line is not rendered verbatim (table cells).
type ParsedLine struct {
	LineNo        int     `json:"line_no"`
	SourceID      int     `json:"source_id"`
	BlockNo       int     `json:"block_no"`
	Text          string  `json:"text"`
	Confidence    float64 `json:"confidence"`
	Quad          Quad    `json:"quad"`
	BBox          BBox    `json:"bbox"`
	InFigure      bool    `json:"in_figure"`
	LowConfidence bool    `json:"low_confidence"`
	TextSource    string  `json:"text_source"`
	TextOCR       string  `json:"text_ocr,omitempty"`
	TextLayer     string  `json:"text_layer,omitempty"`
	MdStart       int     `json:"md_start"`
	MdEnd         int     `json:"md_end"`
}

// TextLayer is a PDF page's own text with pixel positions (§5.8).
type TextLayer struct {
	Words []TextWord `json:"words"`
}

// TextWord is a run of characters on one line with a pixel box.
type TextWord struct {
	Text string `json:"text"`
	BBox BBox   `json:"bbox"`
}

// PDFBookmark is one entry of a PDF outline.
type PDFBookmark struct {
	Title    string        `json:"title"`
	Page     int           `json:"page"` // 1-based; 0 when unknown
	Children []PDFBookmark `json:"children,omitempty"`
}
