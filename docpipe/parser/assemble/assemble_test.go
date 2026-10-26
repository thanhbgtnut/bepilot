package assemble

import (
	"os"
	"strings"
	"testing"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/parser/turboocr"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

func samplePage(t *testing.T, fix bool) *types.ParsedPage {
	t.Helper()
	body, err := os.ReadFile("testdata/output_example.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := turboocr.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	raw.Width, raw.Height = 3500, 5000
	p := Build(raw, Options{PageNo: 1, DPI: 300, Engine: "turboocr", ReadingOrderFix: fix, LowConfThreshold: 0.6,
		AssetKey: func(pg, b int) string { return "asset://doc/p1/b.jpg" }})
	Render(p)
	return p
}

// N7: golden test on the spec sample.
func TestSampleReadingOrderFix(t *testing.T) {
	p := samplePage(t, true)
	md := p.Markdown
	iSection := strings.Index(md, "3. Ngành, nghề kinh doanh:")
	iTable := strings.Index(md, "| STT | Tên ngành | Mã ngành |")
	if iSection < 0 || iTable < 0 {
		t.Fatalf("missing section or table in markdown:\n%s", md)
	}
	if iSection > iTable {
		t.Fatalf("reading order not fixed: section heading after table")
	}
	// Without the fix the engine's order puts the table first.
	raw := samplePage(t, false).Markdown
	if strings.Index(raw, "3. Ngành, nghề kinh doanh:") < strings.Index(raw, "| STT |") {
		t.Fatalf("expected the unfixed order to have the table first")
	}
}

func TestSampleTableIsGFMWithThreeRows(t *testing.T) {
	p := samplePage(t, true)
	var table *types.ParsedBlock
	for i := range p.Blocks {
		if p.Blocks[i].Type == types.BlockTable {
			table = &p.Blocks[i]
		}
	}
	if table == nil {
		t.Fatal("no table block")
	}
	md := string([]rune(p.Markdown)[table.MdStart:table.MdEnd])
	rows := strings.Split(md, "\n")
	if len(rows) != 4 { // header + separator + 2 data rows
		t.Fatalf("table rows = %d:\n%s", len(rows), md)
	}
	if !strings.Contains(rows[3], "4673 (Chính)") {
		t.Fatalf("last row = %q", rows[3])
	}
}

func TestSampleLineOffsetsMatchMarkdown(t *testing.T) {
	p := samplePage(t, true)
	runes := []rune(p.Markdown)
	withOffset := 0
	for i, l := range p.Lines {
		if l.LineNo != i {
			t.Fatalf("line %d has LineNo %d", i, l.LineNo)
		}
		if l.MdStart < 0 {
			continue
		}
		withOffset++
		if got := string(runes[l.MdStart:l.MdEnd]); got != l.Text {
			t.Fatalf("line %d offset text %q != %q", i, got, l.Text)
		}
	}
	if withOffset < 30 {
		t.Fatalf("only %d lines have offsets", withOffset)
	}
}

func TestSampleTitleHeaderFigureAndConfidence(t *testing.T) {
	p := samplePage(t, true)
	if !strings.Contains(p.Markdown, "# GIẤY CHỨNG NHẬN ĐĂNG KÝ HỘ KINH DOANH") {
		t.Fatalf("title missing:\n%s", p.Markdown)
	}
	if !strings.HasPrefix(p.Markdown, "UBND XÃ NHA BÍCH") {
		t.Fatalf("header block should lead the page, got %q", p.Markdown[:40])
	}
	var inFigure int
	for _, l := range p.Lines {
		if l.InFigure {
			inFigure++
		}
	}
	if inFigure != 4 {
		t.Fatalf("in-figure lines = %d, want 4 (seal text)", inFigure)
	}
	if !strings.Contains(p.Markdown, "> PHÒNG") {
		t.Fatalf("figure text not quoted")
	}
	if len(p.Blocks) != 35 {
		t.Fatalf("blocks = %d, want 35 (line-less regions kept)", len(p.Blocks))
	}
	if p.IsBlank {
		t.Fatal("page marked blank")
	}
}

func TestMarkFurnitureOnlyRepeatedHeaders(t *testing.T) {
	mk := func(header string) *types.ParsedPage {
		raw := &parser.RawPage{
			Regions: []parser.RawRegion{
				{ID: 0, Class: "header", Quad: types.QuadFromBBox(types.BBox{X0: 0, Y0: 0, X1: 100, Y1: 10})},
				{ID: 1, Class: "text", Quad: types.QuadFromBBox(types.BBox{X0: 0, Y0: 20, X1: 100, Y1: 30})},
			},
			Lines: []parser.RawLine{
				{ID: 0, Text: header, LayoutID: 0, Quad: types.QuadFromBBox(types.BBox{X0: 0, Y0: 0, X1: 100, Y1: 10})},
				{ID: 1, Text: "body", LayoutID: 1, Quad: types.QuadFromBBox(types.BBox{X0: 0, Y0: 20, X1: 100, Y1: 30})},
			},
		}
		p := Build(raw, Options{})
		Render(p)
		return p
	}
	pages := []*types.ParsedPage{mk("Báo cáo tài chính - Trang 1"), mk("Báo cáo tài chính - Trang 2"), mk("Chỉ có ở trang 3")}
	changed := MarkFurniture(pages, 0.5)
	if len(changed) != 2 {
		t.Fatalf("changed = %v", changed)
	}
	if strings.Contains(pages[0].Markdown, "Báo cáo") || !strings.Contains(pages[2].Markdown, "Chỉ có") {
		t.Fatalf("furniture not applied correctly: %q / %q", pages[0].Markdown, pages[2].Markdown)
	}
}

func TestMergedCellTableKeepsHTML(t *testing.T) {
	md := tableMarkdown(`<table><tr><td rowspan="2">A</td><td>B</td></tr><tr><td>C</td></tr></table>`)
	if !strings.HasPrefix(md, "<table>") || !strings.Contains(md, `rowspan="2"`) {
		t.Fatalf("md = %s", md)
	}
}
