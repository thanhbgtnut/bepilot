package vlm

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thanhenti/bepaylot/docpipe/parser"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

// fullPage transcribes the whole page in one VLM call without OCR text (the
// layout engine failed or found nothing) and turns the markdown
// into regions (headings, tables, formulas, figures, paragraphs) stacked down
// the page. Without OCR there are no real line positions: region boxes are
// approximate vertical bands proportional to text length (confidence 0), and
// assemble gives each region synthetic lines inside its band. Figures keep
// the box the model reports (olmOCR: page_x_y_w_h.png). A page call that
// already succeeded (pc, run alongside the layout engine) is reused instead
// of a new call.
func (e *Engine) fullPage(ctx context.Context, img []byte, in parser.PageImage, reason string, pc *pageCall) (*parser.RawPage, error) {
	src, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, fmt.Errorf("vlm: decode page image: %w", err)
	}
	b := src.Bounds()
	w, h := in.Width, in.Height
	if w <= 0 || h <= 0 {
		w, h = b.Dx(), b.Dy()
	}
	t0 := time.Now()
	var tr *Transcription
	var cr CallResult
	if pc != nil && pc.cr.Error == "" {
		tr, cr, t0 = pc.tr, pc.cr, pc.t0
	} else {
		tr, cr = e.call(ctx, in.PageNo, e.cfg.Prompt, src)
	}
	if cr.Error != "" {
		return nil, fmt.Errorf("vlm: full page (layout unavailable: %s): %s", reason, cr.Error)
	}
	// Figure boxes are in the pixels of the image the model saw.
	scale := 1.0
	if long := max(b.Dx(), b.Dy()); e.cfg.MaxSide > 0 && long > e.cfg.MaxSide {
		scale = float64(long) / float64(e.cfg.MaxSide)
	}
	page := &parser.RawPage{Width: w, Height: h, Regions: SplitMarkdown(tr.Text, w, h, scale), Markdown: strings.TrimSpace(tr.Text)}
	e.envelope(page, rawEnvelope{Mode: "full_page", LayoutError: reason, Call: &cr, Markdown: tr.Text}, int(time.Since(t0).Milliseconds()))
	return page, nil
}

var (
	reFigure  = regexp.MustCompile(`^!\[([^\]]*)\]\(page_(\d+)_(\d+)_(\d+)_(\d+)\.png\)\s*$`)
	reHeading = regexp.MustCompile(`^(#{1,6})\s+\S`)
	// Vietnamese administrative/legal structure the model writes as plain
	// text: "Điều 1.", "Chương II", "Mục 3", "Phần IV".
	reLegalHeading = regexp.MustCompile(`^(?i:điều|chương|mục|phần)\s+(?:\d+|[IVXLC]+)\b`)
	// Document titles: an all-caps line starting with a document type.
	reDocType = regexp.MustCompile(`^(?:GIẤY|HỢP ĐỒNG|QUYẾT ĐỊNH|BIÊN BẢN|THÔNG BÁO|TỜ TRÌNH|BÁO CÁO|CÔNG VĂN|ĐƠN|PHỤ LỤC|NGHỊ QUYẾT|NGHỊ ĐỊNH|THÔNG TƯ|CHỨNG NHẬN|BẢN CAM KẾT)\b`)
)

// plainHeading classifies a plain-text line as a heading ("" when it is not).
func plainHeading(line string) string {
	if utf8.RuneCountInString(line) > 120 {
		return ""
	}
	switch {
	case reLegalHeading.MatchString(line):
		return "paragraph_title"
	case reDocType.MatchString(line) && strings.ToUpper(line) == line:
		return "doc_title"
	}
	return ""
}

type segment struct {
	class string
	text  string
	box   *types.BBox // figures only
}

// SplitMarkdown splits a page transcription into regions in reading order.
// scale maps figure coordinates reported by the model back to page pixels.
func SplitMarkdown(md string, width, height int, scale float64) []parser.RawRegion {
	segs := splitSegments(md, width, height, scale)

	// Vertical bands proportional to text length inside the page margins.
	total := 0
	for _, s := range segs {
		if s.box == nil {
			total += weight(s.text)
		}
	}
	x0, x1 := float64(width)*0.06, float64(width)*0.94
	top, span := float64(height)*0.05, float64(height)*0.90
	y := top
	out := make([]parser.RawRegion, 0, len(segs))
	for i, s := range segs {
		r := parser.RawRegion{ID: i, Class: s.class}
		if s.box != nil {
			r.Quad, r.Confidence = types.QuadFromBBox(*s.box), 0.5
		} else {
			hh := span * float64(weight(s.text)) / float64(max(total, 1))
			r.Quad = types.QuadFromBBox(types.BBox{X0: x0, Y0: y, X1: x1, Y1: y + hh})
			y += hh
		}
		if s.class != "figure" {
			apply(&r, s.text)
		}
		out = append(out, r)
	}
	return out
}

// splitSegments cuts a page transcription into segments in reading order:
// headings, tables, formulas, figures (box from the model, scaled) and
// paragraphs separated by blank lines.
func splitSegments(md string, width, height int, scale float64) []segment {
	var segs []segment
	var cur []string
	curClass := ""
	flush := func() {
		if t := strings.TrimSpace(strings.Join(cur, "\n")); t != "" {
			segs = append(segs, segment{class: curClass, text: t})
		}
		cur, curClass = nil, ""
	}
	start := func(class string) {
		if curClass != class {
			flush()
			curClass = class
		}
	}
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	titled := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		switch {
		case trim == "":
			flush()
		case strings.HasPrefix(strings.ToLower(trim), "<table"):
			flush()
			var tb []string
			for ; i < len(lines); i++ {
				tb = append(tb, lines[i])
				if strings.Contains(strings.ToLower(lines[i]), "</table>") {
					break
				}
			}
			segs = append(segs, segment{class: "table", text: strings.Join(tb, "\n")})
		case trim == "$$" || trim == `\[`:
			flush()
			closing := map[string]string{"$$": "$$", `\[`: `\]`}[trim]
			fm := []string{line}
			for i++; i < len(lines); i++ {
				fm = append(fm, lines[i])
				if strings.TrimSpace(lines[i]) == closing {
					break
				}
			}
			segs = append(segs, segment{class: "formula", text: strings.Join(fm, "\n")})
		case reHeading.MatchString(trim):
			flush()
			class := "paragraph_title"
			if strings.HasPrefix(trim, "# ") && !titled {
				class, titled = "doc_title", true
			}
			segs = append(segs, segment{class: class, text: trim})
		case reFigure.MatchString(trim):
			flush()
			m := reFigure.FindStringSubmatch(trim)
			x, _ := strconv.ParseFloat(m[2], 64)
			y, _ := strconv.ParseFloat(m[3], 64)
			fw, _ := strconv.ParseFloat(m[4], 64)
			fh, _ := strconv.ParseFloat(m[5], 64)
			bb := types.BBox{X0: x * scale, Y0: y * scale, X1: (x + fw) * scale, Y1: (y + fh) * scale}
			bb = types.BBox{X0: clamp(bb.X0, width), Y0: clamp(bb.Y0, height), X1: clamp(bb.X1, width), Y1: clamp(bb.Y1, height)}
			segs = append(segs, segment{class: "figure", text: m[1], box: &bb})
		case strings.HasPrefix(trim, "|"):
			start("table")
			cur = append(cur, line)
		case plainHeading(trim) != "":
			flush()
			class := plainHeading(trim)
			if class == "doc_title" {
				if titled {
					class = "paragraph_title"
				}
				titled = true
			}
			segs = append(segs, segment{class: class, text: trim})
		default:
			start("text")
			cur = append(cur, line)
		}
	}
	flush()
	return segs
}

// weight gives short segments (headings) a visible band.
func weight(s string) int { return max(utf8.RuneCountInString(s), 40) }

func clamp(v float64, hi int) float64 { return min(max(v, 0), float64(hi)) }
