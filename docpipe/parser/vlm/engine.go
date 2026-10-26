package vlm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // page images are JPEG; PNG is accepted for tests and tools
	"io"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"golang.org/x/image/draw"

	"github.com/thanhenti/bepaylot/docpipe/parser"
)

// Name is the engine name used in config, the registry and documents.engine.
const Name = "turboocr_vlm"

// Error policies when the page call fails.
const (
	OnErrorFallback = "fallback" // keep the OCR text of the page
	OnErrorFail     = "fail"     // fail the page (the task retries it)
)

// Config configures the engine.
type Config struct {
	// Layout is the engine that reads lines, boxes and layout (TurboOCR).
	Layout parser.Engine
	Client Transcriber
	// MaxConcurrency bounds in-flight page calls across all pages of this
	// process.
	MaxConcurrency int
	// PagePrompt reads a page after TurboOCR; Prompt reads a page the layout
	// engine failed on (full_page). Neither carries the OCR text.
	PagePrompt string
	Prompt     string
	// MaxSide downscales page images whose longer side exceeds it (olmOCR is
	// trained on 1288 px pages); 0 keeps the size.
	MaxSide     int
	JPEGQuality int
	Retries     int
	OnError     string
	// FullPage sends the page to the VLM without OCR text when the layout
	// engine fails or finds nothing.
	FullPage bool
	// NumberCheck compares the numbers of the markdown with the OCR text:
	// NumbersOff (default), NumbersDetect or NumbersConfirm (see numbers.go).
	// ConfirmPrompt is the follow-up question (default DefaultConfirmPrompt,
	// %s is the list of conflicts).
	NumberCheck   string
	ConfirmPrompt string
	Log           *slog.Logger
}

// Engine implements parser.Engine: step 1 reads the page with Config.Layout,
// step 2 asks the VLM for the page markdown from the image alone.
type Engine struct {
	cfg Config
	sem chan struct{}
}

// New returns the engine.
func New(cfg Config) (*Engine, error) {
	if cfg.Layout == nil || cfg.Client == nil {
		return nil, errors.New("vlm: layout engine and client are required")
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 4
	}
	if cfg.PagePrompt == "" {
		cfg.PagePrompt = DefaultPagePrompt
	}
	if cfg.Prompt == "" {
		cfg.Prompt = DefaultPrompt
	}
	if cfg.JPEGQuality <= 0 {
		cfg.JPEGQuality = 90
	}
	if cfg.OnError == "" {
		cfg.OnError = OnErrorFallback
	}
	switch cfg.NumberCheck {
	case "":
		cfg.NumberCheck = NumbersOff
	case NumbersOff, NumbersDetect, NumbersConfirm:
	default:
		return nil, fmt.Errorf("vlm: number_check %q: want off, detect or confirm", cfg.NumberCheck)
	}
	if cfg.ConfirmPrompt == "" {
		cfg.ConfirmPrompt = DefaultConfirmPrompt
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Engine{cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrency)}, nil
}

// Name implements parser.Engine.
func (e *Engine) Name() string { return Name }

// Health requires both the layout engine and the VLM.
func (e *Engine) Health(ctx context.Context) error {
	// With full_page the engine still works without the layout engine
	// (degraded: pages go to the VLM without OCR text), so only the VLM is
	// required.
	if err := e.cfg.Layout.Health(ctx); err != nil && !e.cfg.FullPage {
		return err
	}
	return e.cfg.Client.Health(ctx)
}

// CallResult records the page call.
type CallResult struct {
	Ms               int               `json:"ms"`
	PromptTokens     int               `json:"prompt_tokens,omitempty"`
	CompletionTokens int               `json:"completion_tokens,omitempty"`
	CachedTokens     int               `json:"cached_tokens,omitempty"`
	Truncated        bool              `json:"truncated,omitempty"`
	Meta             map[string]string `json:"meta,omitempty"`
	Error            string            `json:"error,omitempty"`
}

// rawEnvelope is what RawPage.Raw holds for this engine.
type rawEnvelope struct {
	Engine string `json:"engine"`
	Model  string `json:"model"`
	// Mode is "page" (TurboOCR + one VLM call) or "full_page" (the layout
	// engine failed: the VLM is the only text source).
	Mode        string          `json:"mode"`
	LayoutError string          `json:"layout_error,omitempty"`
	Layout      json.RawMessage `json:"layout,omitempty"`
	Call        *CallResult     `json:"call,omitempty"`
	Markdown    string          `json:"markdown,omitempty"`
	Align       *AlignReport    `json:"align,omitempty"`
	// Numbers is the number check; when it changed numbers, the page
	// markdown differs from Markdown (the VLM's reply) by exactly those.
	Numbers *NumberReport `json:"numbers,omitempty"`
	Ms      int           `json:"ms"`
}

// PageNumbers returns the number check kept in the Raw of a page read by
// this engine, or nil.
func PageNumbers(raw []byte) *NumberReport {
	var env struct {
		Numbers *NumberReport `json:"numbers"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return nil
	}
	return env.Numbers
}

// ParsePage runs the two steps at the same time and independently: TurboOCR
// reads the page while one VLM call returns the page markdown from the image
// alone (no OCR text in the prompt). The markdown is kept verbatim on
// RawPage.Markdown and is the page's text; it is split onto the OCR regions
// (distribute) only to give it positions. OCR text never enters it: the OCR
// lines stay as reference data.
//
// With FullPage, a page the layout engine cannot handle (error, circuit open,
// or nothing found) is sent to the VLM without OCR text and its markdown is
// split into blocks (fullPage). This also applies when Refine is off: a page
// the layout engine fails on has no other text source besides the VLM.
func (e *Engine) ParsePage(ctx context.Context, in parser.PageImage, opt parser.PageOptions) (*parser.RawPage, error) {
	img, err := io.ReadAll(io.LimitReader(in.Body, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("vlm: read page image: %w", err)
	}
	var vlm chan pageCall
	if opt.Refine {
		opt.Layout = true // regions carry the markdown back to positions
		src, _, err := image.Decode(bytes.NewReader(img))
		if err != nil {
			return nil, fmt.Errorf("vlm: decode page image: %w", err)
		}
		// The VLM reads the image alone, so it does not wait for TurboOCR.
		vctx, cancel := context.WithCancel(ctx)
		defer cancel() // stops the call when the page ends without it
		vlm = make(chan pageCall, 1)
		go func() {
			t0 := time.Now()
			jpg, err := e.encode(src)
			if err != nil {
				vlm <- pageCall{cr: CallResult{Error: err.Error()}, t0: t0}
				return
			}
			tr, cr := e.send(vctx, Request{Prompt: e.cfg.PagePrompt, JPEG: jpg, PageNo: in.PageNo})
			vlm <- pageCall{tr: tr, cr: cr, t0: t0, jpg: jpg}
		}()
	}
	in.Body, in.Size = bytes.NewReader(img), int64(len(img))
	page, err := e.cfg.Layout.ParsePage(ctx, in, opt)
	if err != nil {
		if !e.cfg.FullPage || ctx.Err() != nil {
			return nil, err
		}
		e.cfg.Log.Warn("vlm: layout engine failed, sending the page to the VLM without OCR text", "page", in.PageNo, "err", err)
		return e.fullPage(ctx, img, in, err.Error(), wait(vlm))
	}
	if !opt.Refine {
		return page, nil
	}
	pc := <-vlm
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(page.Lines) == 0 && len(page.Regions) == 0 && e.cfg.FullPage {
		return e.fullPage(ctx, img, in, "layout engine found no region and no line", &pc)
	}
	tr, cr := pc.tr, pc.cr
	env := rawEnvelope{Mode: "page", Call: &cr}
	if cr.Error != "" {
		e.envelope(page, env, int(time.Since(pc.t0).Milliseconds()))
		e.cfg.Log.Warn("vlm: page call failed", "page", in.PageNo, "err", cr.Error)
		if e.cfg.OnError == OnErrorFail {
			return nil, fmt.Errorf("vlm: page call failed: %s", cr.Error)
		}
		return page, nil // OCR text only
	}
	if tr.Truncated {
		e.cfg.Log.Warn("vlm: page markdown truncated at max_tokens; the rest keeps OCR text", "page", in.PageNo)
	}
	env.Markdown = strings.TrimSpace(tr.Text)
	page.Markdown = env.Markdown
	if e.cfg.NumberCheck != NumbersOff {
		page.Markdown, env.Numbers = e.checkNumbers(ctx, in.PageNo, page, env.Markdown, pc)
		env.Numbers.Truncated = tr.Truncated
	}
	rep := distribute(page, page.Markdown)
	env.Align = &rep
	if rep.OCRWords > 0 && rep.Coverage < 0.5 {
		e.cfg.Log.Warn("vlm: page markdown agrees with little of the OCR text", "page", in.PageNo,
			"coverage", rep.Coverage, "unmatched_segments", len(rep.Unmatched), "skipped", rep.Skipped)
	}
	e.envelope(page, env, int(time.Since(pc.t0).Milliseconds()))
	return page, nil
}

// pageCall is the result of the page call that runs alongside the layout
// engine, with the JPEG it sent (a follow-up turn repeats it byte for byte).
type pageCall struct {
	tr  *Transcription
	cr  CallResult
	t0  time.Time
	jpg []byte
}

// checkNumbers compares the numbers of md with the OCR lines of page and,
// with NumbersConfirm, asks the VLM about the conflicts as the next turn of
// the conversation that read the page. It returns the markdown (changed
// only where the model was sure) and the report.
func (e *Engine) checkNumbers(ctx context.Context, pageNo int, page *parser.RawPage, md string, pc pageCall) (string, *NumberReport) {
	rep := DetectNumbers(md, page)
	rep.Mode = e.cfg.NumberCheck
	if len(rep.Conflicts) == 0 {
		return md, rep
	}
	if e.cfg.NumberCheck != NumbersConfirm || len(pc.jpg) == 0 {
		rep.Unverified = rep.Conflicts
		return md, rep
	}
	answer := pc.tr.Raw
	if answer == "" {
		answer = pc.tr.Text
	}
	tr, cr := e.send(ctx, Request{
		PageNo: pageNo,
		Prompt: confirmPrompt(e.cfg.ConfirmPrompt, rep.Conflicts),
		History: []Turn{
			{Role: "user", Text: e.cfg.PagePrompt, JPEG: pc.jpg},
			{Role: "assistant", Text: answer},
		},
	})
	rep.Call = &cr
	if cr.Error == "" {
		answers, err := parseConfirm(tr.Text)
		if err == nil {
			md, rep.Resolutions, rep.Unverified = applyConfirm(md, rep.Conflicts, answers)
			return md, rep
		}
		cr.Error = err.Error()
	}
	e.cfg.Log.Warn("vlm: number confirmation failed; conflicts stay unverified", "page", pageNo, "err", cr.Error)
	rep.Unverified = rep.Conflicts
	return md, rep
}

// wait returns the page call's result, or nil when no call was started.
func wait(ch chan pageCall) *pageCall {
	if ch == nil {
		return nil
	}
	pc := <-ch
	return &pc
}

func (e *Engine) envelope(page *parser.RawPage, env rawEnvelope, ms int) {
	env.Engine, env.Model, env.Ms = Name, e.cfg.Client.Model(), ms
	if json.Valid(page.Raw) {
		env.Layout = json.RawMessage(page.Raw)
	}
	if b, err := json.Marshal(env); err == nil {
		page.Raw = b
	}
}

// call sends one page image to the Transcriber under the process-wide
// semaphore, retrying transient errors.
func (e *Engine) call(ctx context.Context, pageNo int, prompt string, img image.Image) (*Transcription, CallResult) {
	jpg, err := e.encode(img)
	if err != nil {
		return nil, CallResult{Error: err.Error()}
	}
	return e.send(ctx, Request{Prompt: prompt, JPEG: jpg, PageNo: pageNo})
}

// send sends one request under the process-wide semaphore, retrying
// transient errors.
func (e *Engine) send(ctx context.Context, req Request) (*Transcription, CallResult) {
	var cr CallResult
	var err error
	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		cr.Error = ctx.Err().Error()
		return nil, cr
	}
	defer func() { <-e.sem }()

	t0 := time.Now()
	var tr *Transcription
	for attempt := 0; ; attempt++ {
		tr, err = e.cfg.Client.Transcribe(ctx, req)
		if err == nil || attempt >= e.cfg.Retries || !Retryable(err) || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(time.Duration(attempt+1) * time.Second):
		case <-ctx.Done():
		}
	}
	cr.Ms = int(time.Since(t0).Milliseconds())
	if err != nil {
		cr.Error = err.Error()
		return nil, cr
	}
	cr.PromptTokens, cr.CompletionTokens, cr.Truncated, cr.Meta = tr.PromptTokens, tr.CompletionTokens, tr.Truncated, tr.Meta
	cr.CachedTokens = tr.CachedTokens
	return tr, cr
}

// encode downscales to MaxSide and encodes JPEG.
func (e *Engine) encode(img image.Image) ([]byte, error) {
	b := img.Bounds()
	if long := max(b.Dx(), b.Dy()); e.cfg.MaxSide > 0 && long > e.cfg.MaxSide {
		f := float64(e.cfg.MaxSide) / float64(long)
		w, h := max(1, int(float64(b.Dx())*f+0.5)), max(1, int(float64(b.Dy())*f+0.5))
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: e.cfg.JPEGQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var (
	reTable   = regexp.MustCompile(`(?is)<table\b.*</table>`)
	reMathDel = regexp.MustCompile(`(?s)^\s*(?:\$\$|\\\[|\\\(|\$)\s*(.*?)\s*(?:\$\$|\\\]|\\\)|\$)\s*$`)
)

// apply stores a transcription on its region: Text always (the block renders
// it verbatim); a table also takes the HTML table of the transcription in
// place of the layout engine's (OCR) HTML, a formula its LaTeX.
func apply(r *parser.RawRegion, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	switch strings.ToLower(r.Class) {
	case "table":
		r.HTML = reTable.FindString(text)
	case "formula":
		if m := reMathDel.FindStringSubmatch(text); m != nil {
			text = m[1]
		}
		r.LaTeX = text
	}
	r.Text = text
}
