package textlayer

import (
	"strings"
	"testing"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/parser/assemble"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

func box(x0, y0, x1, y1 float64) types.BBox { return types.BBox{X0: x0, Y0: y0, X1: x1, Y1: y1} }

// ocrPage builds a two-line OCR page with one OCR mistake ("BỊCH").
func ocrPage() *types.ParsedPage {
	raw := &parser.RawPage{
		Width: 1000, Height: 1000,
		Regions: []parser.RawRegion{{ID: 0, Class: "text", Quad: types.QuadFromBBox(box(0, 0, 1000, 200))}},
		Lines: []parser.RawLine{
			{ID: 0, Text: "UBND XÃ NHA BỊCH", Confidence: 0.9, LayoutID: 0, Quad: types.QuadFromBBox(box(10, 10, 500, 40))},
			{ID: 1, Text: "Vốn kinh doanh: 50.000.000 đồng", Confidence: 0.9, LayoutID: 0, Quad: types.QuadFromBBox(box(10, 60, 600, 90))},
		},
		ReadingOrder: []int{0, 1},
	}
	p := assemble.Build(raw, assemble.Options{PageNo: 1})
	assemble.Render(p)
	return p
}

func words(ws ...types.TextWord) *types.TextLayer { return &types.TextLayer{Words: ws} }

func TestWordsFromChars(t *testing.T) {
	var chars []Char
	x := 0.0
	for _, r := range "Nha Bích" {
		chars = append(chars, Char{Text: string(r), BBox: box(x, 0, x+10, 20)})
		x += 10
	}
	ws := WordsFromChars(chars)
	if len(ws) != 2 || ws[0].Text != "Nha" || ws[1].Text != "Bích" {
		t.Fatalf("words = %+v", ws)
	}
}

// N7c: good text layer fixes diacritics and adds a line OCR missed.
func TestMergeFixesAccentsAndAddsMissedLine(t *testing.T) {
	p := ocrPage()
	layer := words(
		types.TextWord{Text: "UBND", BBox: box(10, 12, 90, 38)},
		types.TextWord{Text: "XÃ", BBox: box(100, 12, 140, 38)},
		types.TextWord{Text: "NHA", BBox: box(150, 12, 220, 38)},
		types.TextWord{Text: "BÍCH", BBox: box(230, 12, 320, 38)},
		types.TextWord{Text: "Vốn", BBox: box(10, 62, 70, 88)},
		types.TextWord{Text: "kinh", BBox: box(80, 62, 140, 88)},
		types.TextWord{Text: "doanh:", BBox: box(150, 62, 240, 88)},
		types.TextWord{Text: "50.000.000", BBox: box(250, 62, 400, 88)},
		types.TextWord{Text: "đồng", BBox: box(410, 62, 480, 88)},
		types.TextWord{Text: "Ghi chú OCR bỏ sót", BBox: box(10, 120, 400, 150)},
	)
	q := Quality(layer, assemble.PlainText(p), Options{})
	if !Usable(q, Options{}) {
		t.Fatalf("quality %v should be usable", q)
	}
	st := Merge(p, layer, Options{})
	assemble.Render(p)
	if st.Replaced != 1 || st.Added != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if p.Lines[0].Text != "UBND XÃ NHA BÍCH" || p.Lines[0].TextOCR != "UBND XÃ NHA BỊCH" || p.Lines[0].TextSource != types.TextSourceLayer {
		t.Fatalf("line 0 = %+v", p.Lines[0])
	}
	if p.TextSource != types.TextSourceMerged {
		t.Fatalf("page text source = %s", p.TextSource)
	}
	if !strings.Contains(p.Markdown, "Ghi chú OCR bỏ sót") || !strings.Contains(p.Markdown, "NHA BÍCH") {
		t.Fatalf("markdown = %s", p.Markdown)
	}
	for i, l := range p.Lines {
		if l.LineNo != i {
			t.Fatalf("line numbering broken at %d", i)
		}
	}
}

// A garbage hidden OCR layer (disagrees with our OCR) is not used.
func TestGarbageLayerScoresLow(t *testing.T) {
	p := ocrPage()
	layer := words(
		types.TextWord{Text: "Ngày surta thhnng am ng Băm cts xyzzy qwerty", BBox: box(10, 12, 500, 38)},
	)
	if q := Quality(layer, assemble.PlainText(p), Options{}); Usable(q, Options{}) {
		t.Fatalf("garbage layer quality %v should not be usable", q)
	}
	// Even if merged, a dissimilar layer keeps OCR text.
	st := Merge(p, words(types.TextWord{Text: "zzz qqq www", BBox: box(10, 12, 300, 38)}), Options{})
	if st.Replaced != 0 || st.Kept != 1 || p.Lines[0].TextLayer != "zzz qqq www" {
		t.Fatalf("stats=%+v line=%+v", st, p.Lines[0])
	}
}

func TestBadCharsRejected(t *testing.T) {
	layer := words(types.TextWord{Text: strings.Repeat("", 30), BBox: box(0, 0, 10, 10)})
	if q := Quality(layer, "", Options{}); q != 0 {
		t.Fatalf("q = %v", q)
	}
}

func TestPageFromLayer(t *testing.T) {
	layer := words(
		types.TextWord{Text: "Dòng", BBox: box(10, 10, 60, 30)},
		types.TextWord{Text: "một", BBox: box(70, 10, 110, 30)},
		types.TextWord{Text: "Đoạn hai", BBox: box(10, 200, 110, 220)},
	)
	p := PageFromLayer(3, 1000, 1000, 300, layer)
	assemble.Render(p)
	if len(p.Blocks) != 2 || len(p.Lines) != 2 || p.Lines[0].Text != "Dòng một" {
		t.Fatalf("page = %+v", p)
	}
	if p.Markdown != "Dòng một\n\nĐoạn hai" {
		t.Fatalf("markdown = %q", p.Markdown)
	}
}

// A comma's glyph box sits below the letters; it must stay on the row and
// attach to the word before it.
func TestLowPunctuationStaysOnRow(t *testing.T) {
	chars := []Char{
		{Text: "B", BBox: box(0, 10, 10, 30)}, {Text: "Í", BBox: box(10, 4, 18, 30)},
		{Text: ",", BBox: box(18, 26, 21, 34)}, {Text: " ", BBox: box(21, 10, 26, 30)},
		{Text: "D", BBox: box(26, 10, 36, 30)},
	}
	ws := WordsFromChars(chars)
	if len(ws) != 2 || ws[0].Text != "BÍ," || ws[1].Text != "D" {
		t.Fatalf("words = %+v", ws)
	}
	if rs := rows(ws); len(rs) != 1 {
		t.Fatalf("rows = %d", len(rs))
	}
}
