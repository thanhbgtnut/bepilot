package docpipe

import "github.com/thanhenti/bepaylot/docpipe/types"

// Document is a run of consecutive pages of a file that are one document.
type Document struct {
	PageStart, PageEnd int
	// Label is the document type of the first page, or types.LabelUnknown
	// when that page is below the confidence threshold or not classified;
	// ProposedLabel then keeps the type the model gave it.
	Label, ProposedLabel string
	Confidence           float64
}

// GroupByFirstPage groups the pages of a file into documents: a page the
// classifier marked as the first page of a document (FirstPage) starts a
// document, and every page after it belongs to that document until the next
// first page. The first page of the file always starts one. A document has
// the type and confidence of its first page; when that page is below min
// (or not classified) the document is unknown and keeps the type as a
// proposal. Without any classified page there is no document. It is pure.
func GroupByFirstPage(pages []types.PageDocType, min float64) []Document {
	classified := false
	for _, p := range pages {
		classified = classified || p.Label != ""
	}
	if !classified {
		return nil
	}
	var out []Document
	for _, p := range pages {
		if len(out) > 0 && !p.FirstPage {
			out[len(out)-1].PageEnd = p.PageNo
			continue
		}
		d := Document{PageStart: p.PageNo, PageEnd: p.PageNo, Label: p.Label, Confidence: p.Confidence}
		if p.Label == "" || p.Confidence < min {
			d.Label, d.ProposedLabel = types.LabelUnknown, p.Label
		}
		out = append(out, d)
	}
	return out
}
