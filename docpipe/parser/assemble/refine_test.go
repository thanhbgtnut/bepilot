package assemble

import (
	"strings"
	"testing"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

func box(x0, y0, x1, y1 float64) types.Quad {
	return types.QuadFromBBox(types.BBox{X0: x0, Y0: y0, X1: x1, Y1: y1})
}

// A paragraph OCR'd as three wrapped, slightly wrong lines; the VLM returns
// it reflowed and corrected.
func refinedSample(vlmText string) *parser.RawPage {
	return &parser.RawPage{
		Width: 1000, Height: 1000,
		Regions: []parser.RawRegion{
			{ID: 0, Class: "paragraph_title", Confidence: 0.9, Quad: box(100, 50, 900, 90), Text: "## 1. Tên hộ kinh doanh"},
			{ID: 1, Class: "text", Confidence: 0.9, Quad: box(100, 100, 900, 250), Text: vlmText},
			{ID: 2, Class: "text", Confidence: 0.9, Quad: box(100, 300, 900, 380), Text: "Ghi chú: bản sao\nKhông có dòng OCR"},
		},
		Lines: []parser.RawLine{
			{ID: 0, Text: "1. Ten ho kinh doanh", Confidence: 0.9, Quad: box(100, 50, 600, 90), LayoutID: 0},
			{ID: 1, Text: "Nơi thường trú: Tổ 5, ấp Minh", Confidence: 0.5, Quad: box(100, 100, 900, 140), LayoutID: 1},
			{ID: 2, Text: "Thang 3, Xa Nha Bich, Tinh Dong", Confidence: 0.5, Quad: box(100, 150, 900, 190), LayoutID: 1},
			{ID: 3, Text: "Nai, Viet Nam", Confidence: 0.5, Quad: box(100, 200, 400, 240), LayoutID: 1},
		},
		ReadingOrder: []int{0, 1, 2, 3},
	}
}

func build(raw *parser.RawPage) *types.ParsedPage {
	p := Build(raw, Options{PageNo: 1, DPI: 300, Engine: "turboocr_vlm", LowConfThreshold: 0.6})
	Render(p)
	return p
}

func checkOffsets(t *testing.T, p *types.ParsedPage) {
	t.Helper()
	md := []rune(p.Markdown)
	for _, l := range p.Lines {
		if l.MdStart < 0 {
			continue
		}
		if got := string(md[l.MdStart:l.MdEnd]); got != l.Text {
			t.Errorf("line %d: markdown[%d:%d] = %q, text %q", l.LineNo, l.MdStart, l.MdEnd, got, l.Text)
		}
	}
	for _, b := range p.Blocks {
		if b.MdStart >= 0 && b.TextSource == types.TextSourceVLM && b.Type == types.BlockParagraph {
			if got := string(md[b.MdStart:b.MdEnd]); got != b.Text {
				t.Errorf("block %d: markdown = %q, text %q", b.BlockNo, got, b.Text)
			}
		}
	}
}

func TestRefineAlignsReflowedParagraph(t *testing.T) {
	vlmText := "Nơi thường trú: Tổ 5, ấp Minh Thắng 3, Xã Nha Bích, Tỉnh Đồng Nai, Việt Nam"
	p := build(refinedSample(vlmText))
	checkOffsets(t, p)
	if p.TextSource != types.TextSourceVLM {
		t.Errorf("page text source = %q", p.TextSource)
	}
	if !strings.Contains(p.Markdown, "## 1. Tên hộ kinh doanh\n\n"+vlmText) {
		t.Fatalf("markdown:\n%s", p.Markdown)
	}
	want := map[int]string{
		1: "Nơi thường trú: Tổ 5, ấp Minh",
		2: "Thắng 3, Xã Nha Bích, Tỉnh Đồng",
		3: "Nai, Việt Nam",
	}
	for _, l := range p.Lines {
		w, ok := want[l.SourceID]
		if !ok {
			continue
		}
		if l.Text != w || l.TextSource != types.TextSourceVLM || l.MdStart < 0 || l.LowConfidence {
			t.Errorf("line %d = %+v, want text %q from vlm with offsets", l.SourceID, l, w)
		}
	}
	for _, l := range p.Lines {
		if l.SourceID == 2 && l.TextOCR != "Thang 3, Xa Nha Bich, Tinh Dong" {
			t.Errorf("OCR text not kept: %q", l.TextOCR)
		}
	}
	if !strings.Contains(PlainText(p), "Tỉnh Đồng Nai") {
		t.Errorf("plain text lacks refined text: %q", PlainText(p))
	}
}

func TestRefineSyntheticLinesForRegionWithoutOCR(t *testing.T) {
	p := build(refinedSample("Nơi thường trú: Tổ 5, ấp Minh Thắng 3, Xã Nha Bích, Tỉnh Đồng Nai, Việt Nam"))
	checkOffsets(t, p)
	var synth []types.ParsedLine
	for _, l := range p.Lines {
		if l.SourceID == -1 {
			synth = append(synth, l)
		}
	}
	if len(synth) != 2 || synth[0].Text != "Ghi chú: bản sao" || synth[1].BBox.Y0 <= synth[0].BBox.Y0 || synth[0].MdStart < 0 {
		t.Fatalf("synthetic lines = %+v", synth)
	}
}

// A transcription OCR disagrees with is still the block's text; the OCR
// lines stay as reference data.
func TestRefineKeepsDisagreeingTranscription(t *testing.T) {
	p := build(refinedSample("Lorem ipsum dolor sit amet, consectetur adipiscing elit"))
	checkOffsets(t, p)
	if !strings.Contains(p.Markdown, "Lorem ipsum") || strings.Contains(p.Markdown, "Thang 3, Xa Nha Bich, Tinh Dong") {
		t.Fatalf("markdown:\n%s", p.Markdown)
	}
	for _, l := range p.Lines {
		if l.TextSource == types.TextSourceOCR && l.MdStart >= 0 {
			t.Errorf("OCR line rendered: %+v", l)
		}
	}
}

// On a page the VLM read, a region it gave no text to does not render its
// OCR lines.
func TestRefineSkipsOCROnlyBlock(t *testing.T) {
	raw := refinedSample("Nơi thường trú: Tổ 5, ấp Minh Thắng 3, Xã Nha Bích, Tỉnh Đồng Nai, Việt Nam")
	raw.Regions = append(raw.Regions, parser.RawRegion{ID: 3, Class: "text", Confidence: 0.9, Quad: box(100, 700, 900, 740)})
	raw.Lines = append(raw.Lines, parser.RawLine{ID: 4, Text: "Chi co trong OCR", Confidence: 0.9, Quad: box(100, 700, 900, 740), LayoutID: 3})
	raw.ReadingOrder = append(raw.ReadingOrder, 4)
	p := build(raw)
	if strings.Contains(p.Markdown, "Chi co trong OCR") {
		t.Fatalf("OCR-only text rendered:\n%s", p.Markdown)
	}
	if l := p.Lines[len(p.Lines)-1]; l.Text != "Chi co trong OCR" || l.MdStart >= 0 {
		t.Fatalf("reference line = %+v", l)
	}
}

func TestRefinedBlockSurvivesRerender(t *testing.T) {
	p := build(refinedSample("Nơi thường trú: Tổ 5, ấp Minh Thắng 3, Xã Nha Bích, Tỉnh Đồng Nai, Việt Nam"))
	before := p.Markdown
	Render(p) // MarkFurniture re-renders stored pages
	if p.Markdown != before {
		t.Fatalf("re-render changed markdown:\n%s\n---\n%s", before, p.Markdown)
	}
	checkOffsets(t, p)
}

func TestRefinedTableAndSample(t *testing.T) {
	raw := refinedSample("x")
	raw.Regions = append(raw.Regions, parser.RawRegion{ID: 3, Class: "table", Quad: box(100, 400, 900, 600),
		Text: "| STT | Tên |\n|---|---|\n| 1 | Gia công |"})
	p := build(raw)
	if !strings.Contains(p.Markdown, "| 1 | Gia công |") {
		t.Fatalf("markdown table missing:\n%s", p.Markdown)
	}
}

func TestCleanRefined(t *testing.T) {
	if got := cleanRefined(types.BlockHeading, "## **Điều 1.**\nPhạm vi"); got != "Điều 1. Phạm vi" {
		t.Errorf("heading = %q", got)
	}
	if got := cleanRefined(types.BlockParagraph, "a\n\n\n\nb ![fig](page_1_2_3_4.png)  "); got != "a\n\nb" {
		t.Errorf("paragraph = %q", got)
	}
}

// A line the transcription skipped is not put back from OCR; a
// low-confidence line the model rewrote takes the model's reading.
func TestRefineDoesNotPutBackSkippedLines(t *testing.T) {
	raw := &parser.RawPage{
		Width: 1000, Height: 1000,
		Regions: []parser.RawRegion{{ID: 0, Class: "text", Confidence: 0.9, Quad: box(100, 100, 900, 400),
			Text: "Họ và tên: NGUYỄN VĂN TÌNH\nNgày 05 tháng 01 năm 2026\nQuốc tịch: Việt Nam"}},
		Lines: []parser.RawLine{
			{ID: 0, Text: "Họ và tên: NGUYEN VAN TINH", Confidence: 0.9, Quad: box(100, 100, 900, 140), LayoutID: 0},
			{ID: 1, Text: "Điện thoại: 0792 127 116", Confidence: 0.9, Quad: box(100, 150, 900, 190), LayoutID: 0},
			{ID: 2, Text: "Ngy surta thhnng am", Confidence: 0.4, Quad: box(100, 200, 900, 240), LayoutID: 0},
			{ID: 3, Text: "Quoc tich: Viet Nam", Confidence: 0.9, Quad: box(100, 250, 900, 290), LayoutID: 0},
		},
		ReadingOrder: []int{0, 1, 2, 3},
	}
	p := build(raw)
	checkOffsets(t, p)
	want := "Họ và tên: NGUYỄN VĂN TÌNH\nNgày 05 tháng 01 năm 2026\nQuốc tịch: Việt Nam"
	if p.Blocks[0].Text != want || p.Blocks[0].TextSource != types.TextSourceVLM || p.Markdown != want {
		t.Fatalf("block text = %q, markdown %q", p.Blocks[0].Text, p.Markdown)
	}
	if l := p.Lines[1]; l.Text != "Điện thoại: 0792 127 116" || l.TextSource != types.TextSourceOCR || l.MdStart >= 0 {
		t.Fatalf("reference line = %+v", l)
	}
	if l := p.Lines[2]; l.TextSource != types.TextSourceVLM || !strings.HasPrefix(l.Text, "Ngày 05") || l.TextOCR != "Ngy surta thhnng am" {
		t.Fatalf("low-confidence line = %+v", l)
	}
}
