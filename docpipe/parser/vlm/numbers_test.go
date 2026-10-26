package vlm

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"strings"
	"sync"
	"testing"

	"github.com/thanhenti/bepaylot/docpipe/parser"
)

func TestCanonicalNumber(t *testing.T) {
	for in, want := range map[string]string{
		"1.234.567,89": "1234567.89",
		"1,234,567.89": "1234567.89",
		"1234567.89":   "1234567.89",
		"1.234":        "1234",
		"1,234":        "1234",
		"12,5":         "12.5",
		"12.50":        "12.5",
		"(1.234)":      "-1234",
		"-1.234":       "-1234",
		"−1.234":       "-1234",
		"12,5%":        "12.5%",
		"007":          "7",
		"0,00":         "0",
		"2025":         "2025",
	} {
		if got := CanonicalNumber(in); got != want {
			t.Errorf("CanonicalNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

// ocrPage is a page whose OCR lines are lines, in that reading order.
func ocrPage(lines ...string) *parser.RawPage {
	p := &parser.RawPage{Width: 100, Height: 100}
	for i, l := range lines {
		p.Lines = append(p.Lines, parser.RawLine{ID: i + 1, Text: l, Confidence: 0.97, LayoutID: -1,
			Quad: [4][2]float64{{0, float64(i * 10)}, {90, float64(i * 10)}, {90, float64(i*10 + 8)}, {0, float64(i*10 + 8)}}})
		p.ReadingOrder = append(p.ReadingOrder, i+1)
	}
	return p
}

func TestDetectNumbersAgreesAcrossFormats(t *testing.T) {
	md := "# Báo cáo\n<table><tr><td colspan=\"2\">Doanh thu</td><td>1.234.567</td></tr><tr><td>Lỗ</td><td>(12.500)</td></tr></table>\n![fig](page_10_20_300_400.png)"
	rep := DetectNumbers(md, ocrPage("Doanh thu 1,234,567", "Lỗ -12.500"))
	if len(rep.Conflicts) != 0 || rep.Agreed != 2 || rep.MDNumbers != 2 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestDetectNumbersFindsConflictsWithoutChangingTheMarkdown(t *testing.T) {
	md := "Doanh thu 1.284.530\nGiá vốn 845.200\nLợi nhuận 389.330\nThuế 77.866"
	page := ocrPage("Doanh thu 1.234.530", "Giá vốn 845.200", "Lợi nhuận 389.330", "Chi phí khác 12.000", "Thuế 77.866")
	rep := DetectNumbers(md, page)
	if rep.Agreed != 3 || len(rep.Conflicts) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	c := rep.Conflicts[0]
	if c.Kind != ConflictMismatch || c.MD != "1.284.530" || c.OCR != "1.234.530" || c.Context != "Doanh thu 1.284.530" || md[c.mdStart:c.mdEnd] != "1.284.530" {
		t.Fatalf("conflict = %+v", c)
	}
	if o := rep.Conflicts[1]; o.Kind != ConflictOCROnly || o.OCR != "12.000" || o.Context != "Chi phí khác 12.000" {
		t.Fatalf("ocr-only conflict = %+v", o)
	}
}

func TestDetectNumbersAcceptsAnotherReadingOrder(t *testing.T) {
	// The VLM reads the table by rows, OCR by columns.
	md := "| A | 1.100 | 2.200 |\n| B | 3.300 | 4.400 |"
	rep := DetectNumbers(md, ocrPage("A", "B", "1.100", "3.300", "2.200", "4.400"))
	if len(rep.Conflicts) != 0 || rep.Agreed != 4 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestApplyConfirmReplacesOnlyWhenSure(t *testing.T) {
	md := "Doanh thu 1.284.530\nGiá vốn 845.900\nLãi 38.330\nThuế 99"
	rep := DetectNumbers(md, ocrPage("Doanh thu 1.234.530", "Giá vốn 845.200", "Lãi 38.830", "Thuế 77.866"))
	// "99" and "77.866" differ too much in length to be one number misread:
	// md_only and ocr_only.
	if len(rep.Conflicts) != 5 || rep.Conflicts[3].Kind != ConflictMDOnly || rep.Conflicts[4].Kind != ConflictOCROnly {
		t.Fatalf("conflicts = %+v", rep.Conflicts)
	}
	answers, err := parseConfirm("```json\n[" +
		`{"id":"c1","value":"1.234.530","source":"ocr","sure":true},` +
		`{"id":"c2","value":"845.200","source":"ocr","sure":false},` +
		`{"id":"c3","value":"38.000","source":"other","sure":true}` +
		"]\n```")
	if err != nil {
		t.Fatal(err)
	}
	out, res, open := applyConfirm(md, rep.Conflicts, answers)
	if out != "Doanh thu 1.234.530\nGiá vốn 845.900\nLãi 38.330\nThuế 99" {
		t.Fatalf("markdown = %q", out)
	}
	if len(res) != 3 || !res[0].Verified || res[0].Before != "1.284.530" || res[0].After != "1.234.530" || res[1].Verified || res[2].Verified {
		t.Fatalf("resolutions = %+v", res)
	}
	// Not sure, a value neither reading has, and no answer at all.
	if len(open) != 4 || open[0].ID != "c2" || open[1].ID != "c3" || open[2].ID != "c4" || open[3].ID != "c5" {
		t.Fatalf("unverified = %+v", open)
	}
	note := (&NumberReport{Unverified: open}).Note()
	if !strings.HasPrefix(note, "<!-- unverified numbers: ") || !strings.Contains(note, `"845.900" (OCR read "845.200")`) {
		t.Fatalf("note = %q", note)
	}
}

// scriptVLM answers the page call with markdown and the follow-up with
// confirm, and records every request.
type scriptVLM struct {
	markdown, confirm string
	mu                sync.Mutex
	reqs              []Request
}

func (s *scriptVLM) Model() string                { return "script" }
func (s *scriptVLM) Health(context.Context) error { return nil }
func (s *scriptVLM) Transcribe(_ context.Context, req Request) (*Transcription, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	if len(req.History) > 0 {
		return &Transcription{Text: s.confirm, Raw: s.confirm}, nil
	}
	raw := "---\nprimary_language: vi\n---\n" + s.markdown
	return &Transcription{Text: s.markdown, Raw: raw}, nil
}

type staticLayout struct{ page *parser.RawPage }

func (l staticLayout) Name() string                 { return "static" }
func (l staticLayout) Health(context.Context) error { return nil }
func (l staticLayout) ParsePage(context.Context, parser.PageImage, parser.PageOptions) (*parser.RawPage, error) {
	cp := *l.page
	cp.Lines = append([]parser.RawLine(nil), l.page.Lines...)
	return &cp, nil
}

func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestEngineConfirmsNumbersInTheSameConversation(t *testing.T) {
	page := ocrPage("Doanh thu 1.234.530", "Giá vốn 845.200")
	sv := &scriptVLM{
		markdown: "Doanh thu 1.284.530\nGiá vốn 845.200",
		confirm:  `[{"id":"c1","value":"1.234.530","source":"ocr","sure":true}]`,
	}
	e, err := New(Config{Layout: staticLayout{page}, Client: sv, NumberCheck: NumbersConfirm})
	if err != nil {
		t.Fatal(err)
	}
	img := tinyJPEG(t)
	raw, err := e.ParsePage(context.Background(), parser.PageImage{PageNo: 3, Body: bytes.NewReader(img), Size: int64(len(img)), Width: 8, Height: 8}, parser.PageOptions{Refine: true})
	if err != nil {
		t.Fatal(err)
	}
	if raw.Markdown != "Doanh thu 1.234.530\nGiá vốn 845.200" {
		t.Fatalf("markdown = %q", raw.Markdown)
	}
	if len(sv.reqs) != 2 {
		t.Fatalf("%d VLM calls, want 2", len(sv.reqs))
	}
	first, follow := sv.reqs[0], sv.reqs[1]
	// The follow-up repeats the first turn byte for byte, then the answer
	// as the model sent it (front matter included), with no new image.
	if len(follow.History) != 2 || follow.History[0].Text != first.Prompt || !bytes.Equal(follow.History[0].JPEG, first.JPEG) ||
		follow.History[1].Role != "assistant" || !strings.HasPrefix(follow.History[1].Text, "---\nprimary_language") || follow.JPEG != nil {
		t.Fatalf("follow-up = %+v", follow.History)
	}
	if !strings.Contains(follow.Prompt, `c1: you wrote "1.284.530", OCR read "1.234.530"`) {
		t.Fatalf("follow-up prompt = %q", follow.Prompt)
	}
	rep := PageNumbers(raw.Raw)
	if rep == nil || rep.Mode != NumbersConfirm || len(rep.Resolutions) != 1 || len(rep.Unverified) != 0 || rep.Call == nil {
		t.Fatalf("report = %+v", rep)
	}
}

func TestEngineDetectOnlyMarksConflicts(t *testing.T) {
	page := ocrPage("Doanh thu 1.234.530")
	sv := &scriptVLM{markdown: "Doanh thu 1.284.530"}
	e, _ := New(Config{Layout: staticLayout{page}, Client: sv, NumberCheck: NumbersDetect})
	img := tinyJPEG(t)
	raw, err := e.ParsePage(context.Background(), parser.PageImage{PageNo: 1, Body: bytes.NewReader(img), Size: int64(len(img)), Width: 8, Height: 8}, parser.PageOptions{Refine: true})
	if err != nil {
		t.Fatal(err)
	}
	rep := PageNumbers(raw.Raw)
	if len(sv.reqs) != 1 || raw.Markdown != "Doanh thu 1.284.530" || rep == nil || len(rep.Unverified) != 1 {
		t.Fatalf("%d calls, markdown %q, report %+v", len(sv.reqs), raw.Markdown, rep)
	}
}

func TestNewRejectsUnknownNumberCheck(t *testing.T) {
	if _, err := New(Config{Layout: staticLayout{ocrPage()}, Client: &scriptVLM{}, NumberCheck: "always"}); err == nil {
		t.Fatal("want an error")
	}
}
