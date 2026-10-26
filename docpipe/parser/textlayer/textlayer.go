// Package textlayer scores a PDF page's own text layer and merges it into the
// OCR result line by line (§5.8): OCR keeps layout, blocks and reading order;
// the text layer contributes exact characters (diacritics, digits) and lines
// OCR missed. It is pure and golden-testable.
package textlayer

import (
	"html"
	"sort"
	"strings"
	"unicode"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

// Options tunes scoring and merging.
type Options struct {
	MinChars           int
	MaxBadCharRatio    float64
	MergeMinSimilarity float64
	// Trusted is set for PDF/A conformance a/u, whose text maps to Unicode.
	Trusted bool
	// MinQuality is the page score needed to use the layer (default 0.5).
	MinQuality float64
}

func (o Options) withDefaults() Options {
	if o.MinChars <= 0 {
		o.MinChars = 20
	}
	if o.MaxBadCharRatio <= 0 {
		o.MaxBadCharRatio = 0.02
	}
	if o.MergeMinSimilarity <= 0 {
		o.MergeMinSimilarity = 0.6
	}
	if o.MinQuality <= 0 {
		o.MinQuality = 0.5
	}
	return o
}

// Char is one text-layer character with its pixel box.
type Char struct {
	Text string
	BBox types.BBox
}

// WordsFromChars groups characters (in content order) into words: a word
// breaks on whitespace, on a jump to another line, or on a horizontal gap
// wider than the character height. Glyph boxes are tighter than character
// advances, so small gaps inside a word ("0101021398") must not split it;
// PDFium emits space characters for real word gaps.
func WordsFromChars(chars []Char) []types.TextWord {
	var words []types.TextWord
	var cur strings.Builder
	var box types.BBox
	var last types.BBox
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			words = append(words, types.TextWord{Text: t, BBox: box})
		}
		cur.Reset()
		box = types.BBox{}
	}
	for _, c := range chars {
		if c.Text == "" || strings.TrimSpace(c.Text) == "" {
			flush()
			continue
		}
		if cur.Len() > 0 {
			h := max(last.Height(), c.BBox.Height(), 1)
			// Punctuation (",", ".") sits low, so vertical overlap decides
			// the row, not the distance between centres.
			sameRow := overlapY(last, c.BBox) > 0 || abs(centerY(last)-centerY(c.BBox)) < h*0.5
			gap := c.BBox.X0 - last.X1
			if !sameRow || gap > h || gap < -h {
				flush()
			}
		}
		cur.WriteString(c.Text)
		box = box.Union(c.BBox)
		last = c.BBox
	}
	flush()
	return words
}

// Text joins the layer's words in content order.
func Text(layer *types.TextLayer) string {
	if layer == nil {
		return ""
	}
	parts := make([]string, len(layer.Words))
	for i, w := range layer.Words {
		parts[i] = w.Text
	}
	return strings.Join(parts, " ")
}

// Quality scores the layer in [0,1]. ocrText, when non-empty, adds agreement
// with OCR (order-insensitive word overlap) so a garbage hidden OCR layer or
// a font without ToUnicode scores low.
func Quality(layer *types.TextLayer, ocrText string, opt Options) float64 {
	opt = opt.withDefaults()
	text := Text(layer)
	if textutil.RuneLen(strings.ReplaceAll(text, " ", "")) < opt.MinChars {
		return 0
	}
	bad := textutil.BadCharRatio(text)
	if bad > opt.MaxBadCharRatio {
		return 0
	}
	q := letterValidity(text) * (1 - bad)
	if strings.TrimSpace(ocrText) == "" {
		return q
	}
	agree := wordOverlap(text, ocrText)
	if opt.Trusted {
		return q * (0.5 + 0.5*agree)
	}
	return q * agree
}

// Usable reports whether a quality score passes the threshold.
func Usable(q float64, opt Options) bool { return q >= opt.withDefaults().MinQuality }

// Stats reports what a merge changed.
type Stats struct {
	Replaced, Kept, Added int
}

// Merge rewrites OCR line text with the layer where they agree and adds lines
// for layer words no OCR line covers. The caller must call assemble.Render
// afterwards to renumber lines and rebuild markdown.
func Merge(page *types.ParsedPage, layer *types.TextLayer, opt Options) Stats {
	opt = opt.withDefaults()
	var st Stats
	if layer == nil || len(layer.Words) == 0 {
		return st
	}
	used := make([]bool, len(layer.Words))
	blockType := map[int]types.BlockType{}
	for _, b := range page.Blocks {
		blockType[b.BlockNo] = b.Type
	}

	for li := range page.Lines {
		l := &page.Lines[li]
		box := expand(l.BBox, 0.15)
		var picked []int
		for wi, w := range layer.Words {
			if used[wi] {
				continue
			}
			if box.Contains((w.BBox.X0+w.BBox.X1)/2, centerY(w.BBox)) {
				picked = append(picked, wi)
			}
		}
		if len(picked) == 0 {
			continue
		}
		layerText := joinWords(layer.Words, picked)
		sim := textutil.Similarity(textutil.Normalize(l.Text), textutil.Normalize(layerText))
		if sim < opt.MergeMinSimilarity {
			l.TextLayer = layerText
			st.Kept++
			continue
		}
		for _, wi := range picked {
			used[wi] = true
		}
		if layerText != l.Text {
			if blockType[l.BlockNo] == types.BlockTable {
				rewriteTableCell(page, l.BlockNo, l.Text, layerText)
			}
			l.TextOCR = l.Text
			l.Text = layerText
			l.TextSource = types.TextSourceLayer
			st.Replaced++
		}
		l.LowConfidence = false
	}

	// Words no OCR line claimed become new lines.
	var rest []types.TextWord
	for wi, w := range layer.Words {
		if !used[wi] {
			rest = append(rest, w)
		}
	}
	for _, row := range rows(rest) {
		addLine(page, row)
		st.Added++
	}
	if st.Replaced+st.Added > 0 {
		page.TextSource = types.TextSourceMerged
	}
	return st
}

// PageFromLayer builds a page purely from the text layer, used when OCR
// failed for good but the layer is usable (§5.8 step 6).
func PageFromLayer(pageNo, width, height int, dpi float64, layer *types.TextLayer) *types.ParsedPage {
	page := &types.ParsedPage{PageNo: pageNo, Width: width, Height: height, DPI: dpi, Engine: "text_layer", TextSource: types.TextSourceLayerOnly}
	var prev *types.TextWord
	blockNo := -1
	for _, row := range rows(layer.Words) {
		line := rowLine(row)
		if prev == nil || line.BBox.Y0-prev.BBox.Y1 > 1.5*max(prev.BBox.Height(), 1) {
			blockNo++
			page.Blocks = append(page.Blocks, types.ParsedBlock{BlockNo: blockNo, SourceID: -1, Type: types.BlockParagraph, Confidence: 1})
		}
		b := &page.Blocks[blockNo]
		b.BBox = b.BBox.Union(line.BBox)
		line.BlockNo = blockNo
		page.Lines = append(page.Lines, line)
		w := types.TextWord{Text: line.Text, BBox: line.BBox}
		prev = &w
	}
	for i := range page.Blocks {
		var texts []string
		for _, l := range page.Lines {
			if l.BlockNo == page.Blocks[i].BlockNo {
				texts = append(texts, l.Text)
			}
		}
		page.Blocks[i].Text = strings.Join(texts, "\n")
	}
	return page
}

// rows clusters words into text rows (top-to-bottom, left-to-right).
func rows(words []types.TextWord) [][]types.TextWord {
	if len(words) == 0 {
		return nil
	}
	sorted := append([]types.TextWord(nil), words...)
	sort.SliceStable(sorted, func(a, b int) bool { return centerY(sorted[a].BBox) < centerY(sorted[b].BBox) })
	var out [][]types.TextWord
	for _, w := range sorted {
		n := len(out)
		if n > 0 {
			var row types.BBox
			for _, x := range out[n-1] {
				row = row.Union(x.BBox)
			}
			if overlapY(row, w.BBox) > min(row.Height(), w.BBox.Height())*0.3 {
				out[n-1] = append(out[n-1], w)
				continue
			}
		}
		out = append(out, []types.TextWord{w})
	}
	for _, r := range out {
		sort.SliceStable(r, func(a, b int) bool { return r[a].BBox.X0 < r[b].BBox.X0 })
	}
	return out
}

func rowLine(row []types.TextWord) types.ParsedLine {
	var box types.BBox
	parts := make([]string, len(row))
	for i, w := range row {
		parts[i] = w.Text
		box = box.Union(w.BBox)
	}
	return types.ParsedLine{
		Text: strings.Join(parts, " "), Confidence: 1, BBox: box, Quad: types.QuadFromBBox(box),
		TextSource: types.TextSourceLayerOnly, MdStart: -1, MdEnd: -1,
	}
}

// addLine inserts a layer-only line into the block containing it, or into a
// new paragraph block placed before the first block that starts below it.
func addLine(page *types.ParsedPage, row []types.TextWord) {
	line := rowLine(row)
	cx, cy := (line.BBox.X0+line.BBox.X1)/2, centerY(line.BBox)
	target := -1
	var bestArea float64
	for _, b := range page.Blocks {
		if b.BBox.Contains(cx, cy) {
			area := b.BBox.Width() * b.BBox.Height()
			if target < 0 || area < bestArea {
				target, bestArea = b.BlockNo, area
			}
		}
	}
	if target < 0 {
		target = nextTempBlockNo(page)
		nb := types.ParsedBlock{BlockNo: target, SourceID: -1, Type: types.BlockParagraph, Confidence: 1, BBox: line.BBox, Text: line.Text}
		pos := len(page.Blocks)
		for i, b := range page.Blocks {
			if b.BBox.Y0 > line.BBox.Y0 && overlapX(b.BBox, line.BBox) > 0 {
				pos = i
				break
			}
		}
		page.Blocks = append(page.Blocks[:pos], append([]types.ParsedBlock{nb}, page.Blocks[pos:]...)...)
	} else {
		for i := range page.Blocks {
			if page.Blocks[i].BlockNo == target && page.Blocks[i].Type == types.BlockFigure {
				line.InFigure = true
			}
		}
	}
	line.BlockNo = target

	// Insert after the last line of the target block that starts above it.
	pos, seen := len(page.Lines), false
	for i, l := range page.Lines {
		if l.BlockNo != target {
			if seen {
				pos = i
				break
			}
			continue
		}
		if !seen {
			pos = i
			seen = true
		}
		if before(l.BBox, line.BBox) {
			pos = i + 1
		}
	}
	page.Lines = append(page.Lines[:pos], append([]types.ParsedLine{line}, page.Lines[pos:]...)...)
}

// before reports whether a reads before b: same row (centres within half a
// line height) compares x, otherwise y.
func before(a, b types.BBox) bool {
	if abs(centerY(a)-centerY(b)) < max(a.Height(), b.Height(), 1)*0.5 {
		return a.X0 <= b.X0
	}
	return centerY(a) < centerY(b)
}

func nextTempBlockNo(page *types.ParsedPage) int {
	n := 100000
	for _, b := range page.Blocks {
		if b.BlockNo >= n {
			n = b.BlockNo + 1
		}
	}
	return n
}

func rewriteTableCell(page *types.ParsedPage, blockNo int, oldText, newText string) {
	for i := range page.Blocks {
		b := &page.Blocks[i]
		if b.BlockNo != blockNo || b.HTML == "" {
			continue
		}
		oldEsc, newEsc := html.EscapeString(oldText), html.EscapeString(newText)
		if strings.Contains(b.HTML, oldEsc) {
			b.HTML = strings.Replace(b.HTML, oldEsc, newEsc, 1)
		} else if strings.Contains(b.HTML, oldText) {
			b.HTML = strings.Replace(b.HTML, oldText, newEsc, 1)
		}
	}
}

func joinWords(words []types.TextWord, idx []int) string {
	sort.SliceStable(idx, func(a, b int) bool {
		wa, wb := words[idx[a]].BBox, words[idx[b]].BBox
		if abs(centerY(wa)-centerY(wb)) > max(wa.Height(), wb.Height())*0.5 {
			return centerY(wa) < centerY(wb)
		}
		return wa.X0 < wb.X0
	})
	parts := make([]string, len(idx))
	for i, wi := range idx {
		parts[i] = words[wi].Text
	}
	return strings.Join(parts, " ")
}

// wordOverlap is the share of OCR words (accent-folded) found in the layer.
func wordOverlap(layerText, ocrText string) float64 {
	lw := map[string]int{}
	for _, w := range strings.Fields(textutil.Normalize(layerText)) {
		lw[w]++
	}
	ow := strings.Fields(textutil.Normalize(ocrText))
	if len(ow) == 0 {
		return 0
	}
	hit := 0
	for _, w := range ow {
		if lw[w] > 0 {
			lw[w]--
			hit++
		}
	}
	return float64(hit) / float64(len(ow))
}

// letterValidity is the share of letters that are Latin script (which covers
// Vietnamese); symbol-font garbage and mis-mapped glyphs score low.
func letterValidity(s string) float64 {
	var letters, ok int
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if unicode.Is(unicode.Latin, r) {
			ok++
		}
	}
	if letters == 0 {
		return 0
	}
	return float64(ok) / float64(letters)
}

func expand(b types.BBox, f float64) types.BBox {
	d := b.Height() * f
	return types.BBox{X0: b.X0 - d, Y0: b.Y0 - d, X1: b.X1 + d, Y1: b.Y1 + d}
}

func centerY(b types.BBox) float64 { return (b.Y0 + b.Y1) / 2 }

func overlapX(a, b types.BBox) float64 { return max(0, min(a.X1, b.X1)-max(a.X0, b.X0)) }

func overlapY(a, b types.BBox) float64 { return max(0, min(a.Y1, b.Y1)-max(a.Y0, b.Y0)) }

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
