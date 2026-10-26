package vlm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/parser/assemble"
	"github.com/thanhenti/bepaylot/docpipe/parser/turboocr"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

const sampleLayout = "../turboocr/testdata/output_example.json"

func TestSplitFrontMatter(t *testing.T) {
	meta, text := SplitFrontMatter("---\nprimary_language: vi\nis_table: True\n---\nGIẤY CHỨNG NHẬN\nđăng ký")
	if meta["primary_language"] != "vi" || meta["is_table"] != "True" || text != "GIẤY CHỨNG NHẬN\nđăng ký" {
		t.Fatalf("meta=%v text=%q", meta, text)
	}
	meta, text = SplitFrontMatter("```markdown\nplain\n```")
	if meta != nil || text != "plain" {
		t.Fatalf("fence: meta=%v text=%q", meta, text)
	}
	if _, text = SplitFrontMatter("no front matter --- here"); text != "no front matter --- here" {
		t.Fatalf("text=%q", text)
	}
}

// fakeLayout serves the TurboOCR sample.
type fakeLayout struct {
	calls  atomic.Int32
	sealID int // >0: that region becomes a seal
}

func (f *fakeLayout) Name() string                 { return turboocr.Name }
func (f *fakeLayout) Health(context.Context) error { return nil }
func (f *fakeLayout) ParsePage(_ context.Context, in parser.PageImage, _ parser.PageOptions) (*parser.RawPage, error) {
	f.calls.Add(1)
	if _, err := io.ReadAll(in.Body); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(sampleLayout)
	if err != nil {
		return nil, err
	}
	p, err := turboocr.Decode(body)
	if err != nil {
		return nil, err
	}
	p.Width, p.Height = in.Width, in.Height
	for i := range p.Regions {
		if f.sealID > 0 && p.Regions[i].ID == f.sealID {
			p.Regions[i].Class = "seal"
		}
	}
	return p, nil
}

// fakeVLM plays a page model: it answers the page markdown built from the
// sample OCR (regions in reading order, accents fixed, the table as HTML,
// headings with #), and records the prompts it got.
type fakeVLM struct {
	page     *parser.RawPage
	inflight atomic.Int32
	peak     atomic.Int32
	calls    atomic.Int32
	mu       sync.Mutex
	prompts  []string
	fail     error
	skipLine int    // >0: that OCR line id is left out of the markdown
	extra    string // appended paragraph (not on the page)
}

func (f *fakeVLM) Model() string                { return "fake-olmocr" }
func (f *fakeVLM) Health(context.Context) error { return nil }
func (f *fakeVLM) Transcribe(ctx context.Context, req Request) (*Transcription, error) {
	n := f.inflight.Add(1)
	defer f.inflight.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	f.calls.Add(1)
	time.Sleep(5 * time.Millisecond)
	if _, err := jpeg.DecodeConfig(bytes.NewReader(req.JPEG)); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.prompts = append(f.prompts, req.Prompt)
	f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	return &Transcription{Text: f.markdown(), PromptTokens: 2000}, nil
}

func (f *fakeVLM) markdown() string {
	class := map[int]string{}
	for _, r := range f.page.Regions {
		class[r.ID] = r.Class
	}
	var parts, cur []string
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, strings.Join(cur, "\n"))
		}
		cur = nil
	}
	prev, tableDone := -2, false
	for _, li := range readingOrder(f.page) {
		l := f.page.Lines[li]
		if l.LayoutID != prev {
			flush()
			prev = l.LayoutID
		}
		if l.ID == f.skipLine {
			continue
		}
		text := strings.ReplaceAll(l.Text, "BẢNGỐC", "BẢN GỐC")
		switch class[l.LayoutID] {
		case "table":
			if !tableDone {
				tableDone = true
				cur = append(cur, "<table><tr><th>STT</th><th>Tên ngành</th><th>Mã ngành</th></tr><tr><td>1</td><td>Gia công cơ khí; xử lý và tráng phủ kim loại</td><td>2592</td></tr></table>")
			}
		case "doc_title":
			cur = append(cur, "# "+text)
		case "paragraph_title":
			cur = append(cur, "## "+text)
		default:
			cur = append(cur, text)
		}
	}
	flush()
	if f.extra != "" {
		parts = append(parts, f.extra)
	}
	return strings.Join(parts, "\n\n")
}

func sampleVLM(t *testing.T) (*fakeLayout, *fakeVLM) {
	t.Helper()
	layout := &fakeLayout{}
	sample, err := layout.ParsePage(context.Background(), parser.PageImage{Body: strings.NewReader("")}, parser.PageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	layout.calls.Store(0)
	return layout, &fakeVLM{page: sample}
}

func parseSample(t *testing.T, e *Engine) (*parser.RawPage, rawEnvelope) {
	t.Helper()
	img := pageJPEG(t, 3319, 4939)
	raw, err := e.ParsePage(context.Background(), parser.PageImage{PageNo: 1, Body: bytes.NewReader(img), Width: 3319, Height: 4939},
		parser.PageOptions{Layout: true, ReadingOrder: true, Tables: true, Refine: true})
	if err != nil {
		t.Fatal(err)
	}
	var env rawEnvelope
	if err := json.Unmarshal(raw.Raw, &env); err != nil {
		t.Fatal(err)
	}
	return raw, env
}

func assembleSample(t *testing.T, raw *parser.RawPage) *types.ParsedPage {
	t.Helper()
	page := assemble.Build(raw, assemble.Options{PageNo: 1, DPI: 300, Engine: Name, ReadingOrderFix: true, LowConfThreshold: 0.6})
	assemble.Render(page)
	runes := []rune(page.Markdown)
	for _, l := range page.Lines {
		if l.MdStart >= 0 && string(runes[l.MdStart:l.MdEnd]) != l.Text {
			t.Errorf("line %d offsets: markdown %q, text %q", l.LineNo, string(runes[l.MdStart:l.MdEnd]), l.Text)
		}
	}
	return page
}

func pageJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	img.Set(10, 10, color.Black)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 50}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Two steps: one TurboOCR call and ONE VLM call for the page, whose prompt
// holds no OCR text; the markdown lands on the OCR regions and keeps the
// OCR positions.
func TestEngineOneCallPerPageWithoutOCRText(t *testing.T) {
	layout, fv := sampleVLM(t)
	e, _ := New(Config{Layout: layout, Client: fv, MaxSide: 1288})
	raw, env := parseSample(t, e)
	if layout.calls.Load() != 1 || fv.calls.Load() != 1 {
		t.Fatalf("layout calls %d, VLM calls %d; want 1 and 1", layout.calls.Load(), fv.calls.Load())
	}
	if prompt := fv.prompts[0]; prompt != DefaultPagePrompt {
		t.Errorf("prompt = %q, want the page prompt alone", prompt)
	}
	if env.Mode != "page" || env.Call == nil || env.Call.PromptTokens != 2000 || env.Align == nil || env.Markdown == "" || len(env.Layout) == 0 {
		t.Fatalf("envelope = %+v", env)
	}
	// The fake's table HTML keeps one row of three: the other rows stay OCR.
	if env.Align.Coverage < 0.8 || len(env.Align.Unmatched) != 0 {
		t.Fatalf("align = %+v", env.Align)
	}

	page := assembleSample(t, raw)
	refined := 0
	for _, b := range page.Blocks {
		if b.TextSource == types.TextSourceVLM {
			refined++
		}
	}
	if refined < 20 || !strings.Contains(page.Markdown, "ĐÃ ĐỐI CHIẾU BẢN GỐC") || !strings.Contains(page.Markdown, "# GIẤY CHỨNG NHẬN") {
		t.Fatalf("%d blocks refined:\n%s", refined, page.Markdown)
	}
	for _, l := range page.Lines {
		if l.TextOCR == "ĐÃ ĐỐI CHIẾU BẢNGỐC" {
			if l.Text != "ĐÃ ĐỐI CHIẾU BẢN GỐC" || l.BBox.Width() <= 0 || l.MdStart < 0 {
				t.Fatalf("corrected line = %+v", l)
			}
			return
		}
	}
	t.Fatal("the corrected line was not found")
}

// waitLayout is a layout engine that answers only once the VLM call has
// started: it fails when the two steps run one after the other.
type waitLayout struct {
	*fakeLayout
	vlm *fakeVLM
}

func (w waitLayout) ParsePage(ctx context.Context, in parser.PageImage, opt parser.PageOptions) (*parser.RawPage, error) {
	for deadline := time.Now().Add(2 * time.Second); w.vlm.calls.Load() == 0; {
		if time.Now().After(deadline) {
			return nil, errors.New("the VLM was not called while the layout engine ran")
		}
		time.Sleep(time.Millisecond)
	}
	return w.fakeLayout.ParsePage(ctx, in, opt)
}

// TurboOCR and the VLM run at the same time, and the VLM's markdown is kept
// verbatim next to the markdown rebuilt from the blocks.
func TestEngineCallsLayoutAndVLMTogether(t *testing.T) {
	layout, fv := sampleVLM(t)
	e, _ := New(Config{Layout: waitLayout{layout, fv}, Client: fv})
	raw, _ := parseSample(t, e)
	want := strings.TrimSpace(fv.markdown())
	if raw.Markdown != want {
		t.Fatalf("raw markdown = %q, want the VLM reply verbatim", raw.Markdown)
	}
	if page := assembleSample(t, raw); page.MarkdownVLM != want {
		t.Fatalf("page markdown_vlm = %q", page.MarkdownVLM)
	}
}

// The markdown is the page's text: a line the model skipped is not put back
// from OCR; the OCR line stays as reference data with its box.
func TestEngineDoesNotPutBackOCRLinesTheVLMSkipped(t *testing.T) {
	layout, fv := sampleVLM(t)
	fv.skipLine = 12 // "Nai, Việt Nam", the second line of region 4
	e, _ := New(Config{Layout: layout, Client: fv})
	raw, env := parseSample(t, e)
	if strings.Contains(env.Markdown, "\nNai, Việt Nam") {
		t.Fatal("fixture: the markdown should skip the line")
	}
	page := assembleSample(t, raw)
	if !strings.Contains(page.Markdown, "Xã Nha B") || strings.Contains(page.Markdown, "\nNai, Việt Nam") {
		t.Fatalf("OCR text reached the page markdown:\n%s", page.Markdown)
	}
	found := false
	for _, l := range page.Lines {
		if l.Text == "Nai, Việt Nam" {
			found = true
			if l.TextSource != types.TextSourceOCR || l.MdStart >= 0 || l.BBox.Width() <= 0 {
				t.Fatalf("reference line = %+v", l)
			}
		}
	}
	if !found {
		t.Fatal("the OCR line was not kept")
	}
}

// Markdown no OCR word agrees with is kept: OCR only places the text.
func TestEngineKeepsMarkdownOCRDoesNotConfirm(t *testing.T) {
	layout, fv := sampleVLM(t)
	fv.extra = "Lorem ipsum dolor sit amet consectetur adipiscing elit"
	e, _ := New(Config{Layout: layout, Client: fv})
	raw, env := parseSample(t, e)
	if len(env.Align.Unmatched) != 1 || !strings.HasPrefix(env.Align.Unmatched[0], "Lorem") {
		t.Fatalf("unmatched = %q", env.Align.Unmatched)
	}
	page := assembleSample(t, raw)
	if !strings.HasSuffix(page.Markdown, fv.extra) {
		t.Fatalf("markdown lost or moved the text:\n%s", page.Markdown)
	}
}

// Every word of the page markdown comes from the VLM, even where the layout
// engine found regions the VLM gave no text to.
func TestEngineMarkdownHasNoOCRText(t *testing.T) {
	layout, fv := sampleVLM(t)
	e, _ := New(Config{Layout: layout, Client: fv})
	raw, env := parseSample(t, e)
	page := assembleSample(t, raw)
	vlmWords := map[string]bool{}
	for _, w := range strings.Fields(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(env.Markdown, " ")) {
		vlmWords[strings.Trim(w, "|#*-")] = true
	}
	for _, w := range strings.Fields(page.Markdown) {
		if w = strings.Trim(w, "|#*-"); w != "" && !vlmWords[w] {
			t.Errorf("word %q is not in the VLM markdown", w)
		}
	}
}

// OCR lines outside every layout region become a region when the markdown
// agrees with them, so they are corrected too.
func TestEngineMakesRegionForLinesOutsideLayout(t *testing.T) {
	layout, fv := sampleVLM(t)
	e, _ := New(Config{Layout: &stripLayout{fakeLayout: layout, drop: 3}, Client: fv}) // "Nơi thường trú…", "Nơi ở hiện tại…"
	raw, env := parseSample(t, e)
	created := 0
	for _, r := range env.Align.Regions {
		if r.Created {
			created++
		}
	}
	if created == 0 {
		t.Fatalf("no region created: %+v", env.Align.Regions)
	}
	page := assembleSample(t, raw)
	vlmLines := 0
	for _, l := range page.Lines {
		if strings.HasPrefix(l.Text, "Nơi ") && l.TextSource == types.TextSourceVLM && l.MdStart >= 0 {
			vlmLines++
		}
	}
	if vlmLines != 2 {
		t.Fatalf("lines outside the layout not refined (%d):\n%s", vlmLines, page.Markdown)
	}
}

// stripLayout drops one region from the sample, leaving its lines outside
// every region.
type stripLayout struct {
	*fakeLayout
	drop int
}

func (s *stripLayout) ParsePage(ctx context.Context, in parser.PageImage, opt parser.PageOptions) (*parser.RawPage, error) {
	p, err := s.fakeLayout.ParsePage(ctx, in, opt)
	if err != nil {
		return nil, err
	}
	var keep []parser.RawRegion
	for _, r := range p.Regions {
		if r.ID != s.drop {
			keep = append(keep, r)
		}
	}
	p.Regions = keep
	for i := range p.Lines {
		if p.Lines[i].LayoutID == s.drop {
			p.Lines[i].LayoutID = -1
		}
	}
	return p, nil
}

func TestEngineConcurrencyAcrossPages(t *testing.T) {
	layout, fv := sampleVLM(t)
	e, _ := New(Config{Layout: layout, Client: fv, MaxConcurrency: 2})
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			parseSample(t, e)
		}()
	}
	wg.Wait()
	if fv.calls.Load() != 6 || fv.peak.Load() > 2 {
		t.Fatalf("calls %d peak %d", fv.calls.Load(), fv.peak.Load())
	}
}

func TestEngineSkipsVLMWhenRefineOff(t *testing.T) {
	layout, fv := sampleVLM(t)
	e, _ := New(Config{Layout: layout, Client: fv})
	_, err := e.ParsePage(context.Background(), parser.PageImage{Body: strings.NewReader("x")}, parser.PageOptions{Refine: false})
	if err != nil || fv.calls.Load() != 0 || layout.calls.Load() != 1 {
		t.Fatalf("err=%v vlm calls=%d layout calls=%d", err, fv.calls.Load(), layout.calls.Load())
	}
}

func TestEngineOnError(t *testing.T) {
	layout, fv := sampleVLM(t)
	fv.fail = &PermanentError{Err: errors.New("bad image")}
	e, _ := New(Config{Layout: layout, Client: fv})
	raw, env := parseSample(t, e)
	if env.Call == nil || env.Call.Error == "" || len(raw.Lines) != 54 || raw.Markdown != "" {
		t.Fatalf("fallback: envelope %+v, markdown %q", env, raw.Markdown)
	}
	if page := assembleSample(t, raw); strings.Contains(page.Markdown, "BẢN GỐC") || !strings.Contains(page.Markdown, "BẢNGỐC") {
		t.Fatal("fallback must keep the OCR text")
	}

	e, _ = New(Config{Layout: layout, Client: fv, OnError: OnErrorFail})
	img := pageJPEG(t, 3319, 4939)
	_, err := e.ParsePage(context.Background(), parser.PageImage{Body: bytes.NewReader(img), Width: 3319, Height: 4939}, parser.PageOptions{Refine: true})
	if err == nil || !strings.Contains(err.Error(), "page call failed") {
		t.Fatalf("err = %v", err)
	}
}

// TestLiveVLM runs the whole engine against a real OpenAI-compatible VLM:
//
//	VLM_BASE_URL=http://localhost:1234/v1 VLM_TEST_IMAGE=page.jpg go test -run TestLiveVLM -v ./parser/vlm
//
// The image must match the layout in turboocr/testdata (the sample page).
func TestLiveVLM(t *testing.T) {
	base, imgPath := os.Getenv("VLM_BASE_URL"), os.Getenv("VLM_TEST_IMAGE")
	if base == "" || imgPath == "" {
		t.Skip("VLM_BASE_URL and VLM_TEST_IMAGE not set")
	}
	img, err := os.ReadFile(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	model := os.Getenv("VLM_MODEL")
	if model == "" {
		model = "allenai/olmocr-2-7b"
	}
	client := &httpVLM{base: strings.TrimRight(base, "/"), model: model}
	e, _ := New(Config{Layout: &fakeLayout{}, Client: client, MaxConcurrency: 4, MaxSide: 1288, Retries: 1})
	t0 := time.Now()
	raw, err := e.ParsePage(context.Background(), parser.PageImage{PageNo: 1, Body: bytes.NewReader(img), Width: cfg.Width, Height: cfg.Height},
		parser.PageOptions{Layout: true, ReadingOrder: true, Tables: true, Refine: true})
	if err != nil {
		t.Fatal(err)
	}
	var env rawEnvelope
	_ = json.Unmarshal(raw.Raw, &env)
	if env.Call == nil || env.Call.Error != "" || env.Align == nil {
		t.Fatalf("page call: %+v", env.Call)
	}
	t.Logf("call %d ms, %d prompt + %d completion tokens; coverage %.2f, %d regions, unmatched %q",
		env.Call.Ms, env.Call.PromptTokens, env.Call.CompletionTokens, env.Align.Coverage, len(env.Align.Regions), env.Align.Unmatched)
	page := assemble.Build(raw, assemble.Options{PageNo: 1, DPI: 300, Engine: Name, ReadingOrderFix: true})
	assemble.Render(page)
	refined, vlmLines, located := 0, 0, 0
	for _, b := range page.Blocks {
		if b.TextSource == types.TextSourceVLM {
			refined++
		}
	}
	for _, l := range page.Lines {
		if l.TextSource == types.TextSourceVLM {
			vlmLines++
			if l.MdStart >= 0 {
				located++
			}
		}
	}
	t.Logf("%s; %d blocks refined; %d/%d VLM lines located\n%s",
		time.Since(t0).Round(time.Millisecond), refined, located, vlmLines, page.Markdown)
	if refined == 0 {
		t.Fatal("no region was refined")
	}
}

// httpVLM is a minimal OpenAI-compatible client for the live test only; in
// production the engine reads through the agent (agent.Extract).
type httpVLM struct{ base, model string }

func (h *httpVLM) Model() string                { return h.model }
func (h *httpVLM) Health(context.Context) error { return nil }
func (h *httpVLM) Transcribe(ctx context.Context, req Request) (*Transcription, error) {
	body, _ := json.Marshal(map[string]any{"model": h.model, "temperature": 0.1, "max_tokens": 4096,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": req.Prompt},
			map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(req.JPEG)}},
		}}}})
	hr, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.base+"/chat/completions", bytes.NewReader(body))
	hr.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var cr struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil || len(cr.Choices) == 0 {
		return nil, fmt.Errorf("status %d: %v", resp.StatusCode, err)
	}
	meta, text := SplitFrontMatter(cr.Choices[0].Message.Content)
	return &Transcription{Text: text, Meta: meta}, nil
}

func trim(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "⏎")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

const fullPageMD = `---
primary_language: vi
is_table: False
---
# GIẤY CHỨNG NHẬN ĐĂNG KÝ HỘ KINH DOANH

Mã số hộ kinh doanh: 070082001498

## 3. Ngành, nghề kinh doanh

<table>
  <tr><th>STT</th><th>Tên ngành</th></tr>
  <tr><td>1</td><td>Gia công cơ khí</td></tr>
</table>

![Con dấu đỏ](page_100_200_300_150.png)

Họ và tên: NGUYỄN VĂN TÌNH
Giới tính: Nam`

func TestSplitMarkdown(t *testing.T) {
	_, md := SplitFrontMatter(fullPageMD)
	rs := SplitMarkdown(md, 2000, 3000, 2)
	var classes []string
	for _, r := range rs {
		classes = append(classes, r.Class)
	}
	if got := strings.Join(classes, ","); got != "doc_title,text,paragraph_title,table,figure,text" {
		t.Fatalf("classes = %s", got)
	}
	if !strings.Contains(rs[3].HTML, "<td>Gia công cơ khí</td>") || rs[3].Text != rs[3].HTML {
		t.Errorf("table = %+v", rs[3])
	}
	if fb := rs[4].Quad.BBox(); fb.X0 != 200 || fb.Y0 != 400 || fb.X1 != 800 || fb.Y1 != 700 {
		t.Errorf("figure box = %+v (scaled ×2)", fb)
	}
	if rs[5].Text != "Họ và tên: NGUYỄN VĂN TÌNH\nGiới tính: Nam" {
		t.Errorf("paragraph = %q", rs[5].Text)
	}
	for i := 1; i < len(rs); i++ {
		if rs[i].Class != "figure" && rs[i-1].Class != "figure" && rs[i].Quad.BBox().Y0 < rs[i-1].Quad.BBox().Y1-0.001 {
			t.Errorf("bands overlap at %d", i)
		}
	}
}

type failingLayout struct{}

func (failingLayout) Name() string { return turboocr.Name }
func (failingLayout) Health(context.Context) error {
	return errors.New("turboocr: connection refused")
}
func (failingLayout) ParsePage(context.Context, parser.PageImage, parser.PageOptions) (*parser.RawPage, error) {
	return nil, errors.New("turboocr: request: dial tcp: i/o timeout")
}

type pageVLM struct{ calls atomic.Int32 }

func (p *pageVLM) Model() string                { return "fake-olmocr" }
func (p *pageVLM) Health(context.Context) error { return nil }
func (p *pageVLM) Transcribe(context.Context, Request) (*Transcription, error) {
	p.calls.Add(1)
	meta, text := SplitFrontMatter(fullPageMD)
	return &Transcription{Text: text, Meta: meta}, nil
}

func TestEngineFullPageWhenLayoutUnavailable(t *testing.T) {
	pv := &pageVLM{}
	e, _ := New(Config{Layout: failingLayout{}, Client: pv, FullPage: true, MaxSide: 1288})
	if err := e.Health(context.Background()); err != nil {
		t.Fatalf("health with full_page should tolerate the layout engine: %v", err)
	}
	img := pageJPEG(t, 2000, 3000)
	for _, refine := range []bool{true, false} {
		raw, err := e.ParsePage(context.Background(), parser.PageImage{PageNo: 1, Body: bytes.NewReader(img), Width: 2000, Height: 3000},
			parser.PageOptions{Refine: refine})
		if err != nil {
			t.Fatal(err)
		}
		var env rawEnvelope
		if err := json.Unmarshal(raw.Raw, &env); err != nil || env.Mode != "full_page" || !strings.Contains(env.LayoutError, "i/o timeout") {
			t.Fatalf("envelope %+v %v", env, err)
		}
		page := assemble.Build(raw, assemble.Options{PageNo: 1, Engine: Name, ReadingOrderFix: true,
			AssetKey: func(pg, b int) string { return "fig.jpg" }})
		assemble.Render(page)
		for _, want := range []string{"# GIẤY CHỨNG NHẬN", "Mã số hộ kinh doanh: 070082001498", "## 3. Ngành", "| 1 | Gia công cơ khí |", "![figure", "Giới tính: Nam"} {
			if !strings.Contains(page.Markdown, want) {
				t.Errorf("markdown lacks %q:\n%s", want, page.Markdown)
			}
		}
		runes := []rune(page.Markdown)
		located := 0
		for _, l := range page.Lines {
			if l.MdStart >= 0 {
				located++
				if string(runes[l.MdStart:l.MdEnd]) != l.Text {
					t.Errorf("line offsets wrong: %q", l.Text)
				}
			}
		}
		if located < 5 {
			t.Errorf("only %d synthetic lines located", located)
		}
	}
	e2, _ := New(Config{Layout: failingLayout{}, Client: pv, FullPage: false})
	if _, err := e2.ParsePage(context.Background(), parser.PageImage{Body: bytes.NewReader(img)}, parser.PageOptions{Refine: true}); err == nil {
		t.Fatal("without full_page a layout failure must fail the page")
	}
}

func TestSplitMarkdownPlainHeadings(t *testing.T) {
	md := "HỢP ĐỒNG THUÊ MẶT BẰNG KINH DOANH\nSố: 15/2026/HĐTMB\n\nĐiều 1. Đối tượng hợp đồng\nBên A cho Bên B thuê mặt bằng.\n\nĐiều 2. Giá thuê\n\n| Khoản mục | Số tiền |\n|---|---|\n| Tiền thuê | 12.000.000 |"
	rs := SplitMarkdown(md, 2480, 3508, 1)
	var got []string
	for _, r := range rs {
		got = append(got, r.Class+":"+strings.SplitN(r.Text, "\n", 2)[0])
	}
	want := "doc_title:HỢP ĐỒNG THUÊ MẶT BẰNG KINH DOANH,text:Số: 15/2026/HĐTMB,paragraph_title:Điều 1. Đối tượng hợp đồng," +
		"text:Bên A cho Bên B thuê mặt bằng.,paragraph_title:Điều 2. Giá thuê,table:| Khoản mục | Số tiền |"
	if strings.Join(got, ",") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, ","), want)
	}
	page := assemble.Build(&parser.RawPage{Width: 2480, Height: 3508, Regions: rs}, assemble.Options{PageNo: 2})
	assemble.Render(page)
	runes := []rune(page.Markdown)
	found := false
	for _, l := range page.Lines {
		if l.Text == "Tiền thuê | 12.000.000" {
			found = l.MdStart >= 0 && string(runes[l.MdStart:l.MdEnd]) == l.Text
		}
	}
	if !found || !strings.Contains(page.Markdown, "# HỢP ĐỒNG") || !strings.Contains(page.Markdown, "## Điều 1.") {
		t.Fatalf("table row line not located or headings missing:\n%s", page.Markdown)
	}
}
