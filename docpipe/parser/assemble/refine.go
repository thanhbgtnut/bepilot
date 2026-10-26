package assemble

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

// Refinement of a block by a VLM transcription (§5.9).
//
// The VLM reads the page and returns clean text, but no positions. OCR lines
// have positions but noisier text. The block renders the VLM text verbatim;
// OCR never replaces, trims or adds to it. refineBlock aligns the two word
// sequences (monotone, edit-distance DP) only to place the text: every OCR
// line that agrees with a span of the VLM text takes that span (the OCR
// reading stays in TextOCR), and its MdStart/MdEnd points at it, so "which
// page, which box" lookups keep working on the VLM text.

// maxAlignCells bounds the DP (OCR words × VLM words) per block.
const maxAlignCells = 4_000_000

var (
	reMdImage   = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	reMdHeading = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+`)
	reBlankRuns = regexp.MustCompile(`\n{3,}`)
)

// cleanRefined normalizes a transcription for a block type. Headings become
// one line without "#" markers (Render adds its own); figure placeholders the
// model invents are dropped.
func cleanRefined(t types.BlockType, text string) string {
	text = reMdImage.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	switch t {
	case types.BlockTitle, types.BlockHeading:
		text = reMdHeading.ReplaceAllString(text, "")
		return textutil.CollapseSpace(strings.ReplaceAll(text, "*", ""))
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(l, unicode.IsSpace)
	}
	return strings.TrimSpace(reBlankRuns.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// Word is one whitespace-separated token of a text.
type Word struct {
	Start, End int // rune offsets in the source text
	Key        string
	// Line is the OCR line index for OCR words; for VLM words any tag the
	// caller needs (-1 by default).
	Line int
}

// Words splits s on whitespace; keys are accent-folded with punctuation
// trimmed, so "Bích," matches "Bich".
func Words(s string, line int) []Word {
	var out []Word
	rs := []rune(s)
	pos, start := 0, -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		raw := string(rs[start:end])
		key := strings.TrimFunc(textutil.Unaccent(raw), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if key == "" {
			key = raw
		}
		out = append(out, Word{Start: start, End: end, Key: key, Line: line})
		start = -1
	}
	for _, r := range rs {
		if unicode.IsSpace(r) {
			flush(pos)
		} else if start < 0 {
			start = pos
		}
		pos++
	}
	flush(pos)
	return out
}

// subCost is 0 for identical words and 1 for unrelated ones.
func subCost(a, b Word) float64 {
	if a.Key == b.Key {
		return 0
	}
	sim := textutil.Similarity(a.Key, b.Key)
	if sim >= 0.5 {
		return 1 - sim
	}
	return 1
}

// MaxAlignCells bounds the DP (OCR words × VLM words) of one alignment.
const MaxAlignCells = maxAlignCells

// GoodMatch is the highest pairing cost that counts as an agreement.
const GoodMatch = 0.5

// Align returns, for every OCR word, the index of the VLM word it aligns to
// (-1 when it aligns to a gap) and the cost of that pairing. The alignment is
// monotone (edit distance): both sequences must be in reading order.
func Align(ocr, vlm []Word) ([]int, []float64) {
	const gap = 0.7
	n, m := len(ocr), len(vlm)
	w := m + 1
	d := make([]float64, (n+1)*w)
	for i := 1; i <= n; i++ {
		d[i*w] = float64(i) * gap
	}
	for j := 1; j <= m; j++ {
		d[j] = float64(j) * gap
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			best := d[(i-1)*w+j-1] + subCost(ocr[i-1], vlm[j-1])
			if v := d[(i-1)*w+j] + gap; v < best {
				best = v
			}
			if v := d[i*w+j-1] + gap; v < best {
				best = v
			}
			d[i*w+j] = best
		}
	}
	pair := make([]int, n)
	cost := make([]float64, n)
	for i := range pair {
		pair[i], cost[i] = -1, 1
	}
	i, j := n, m
	for i > 0 && j > 0 {
		c := subCost(ocr[i-1], vlm[j-1])
		switch {
		case d[i*w+j] == d[(i-1)*w+j-1]+c:
			pair[i-1], cost[i-1] = j-1, c
			i, j = i-1, j-1
		case d[i*w+j] == d[(i-1)*w+j]+gap:
			i--
		default:
			j--
		}
	}
	return pair, cost
}

// refineBlock puts a transcription on block b whose lines are
// page.Lines[from:to]. An OCR line none of whose words matches well (and that
// OCR does not itself mark low confidence) keeps its OCR text as reference
// data and is not rendered. A block without OCR lines gets synthetic lines
// spread over its box.
func refineBlock(page *types.ParsedPage, b *types.ParsedBlock, from, to int, text string) {
	text = cleanRefined(b.Type, text)
	if text == "" {
		return
	}
	b.Text, b.TextSource = text, types.TextSourceVLM
	if from == to {
		page.Lines = append(page.Lines, syntheticLines(b, text)...)
		return
	}

	var ocr []Word
	for li := from; li < to; li++ {
		ocr = append(ocr, Words(page.Lines[li].Text, li)...)
	}
	vlm := Words(text, -1)
	if len(ocr) == 0 || len(vlm) == 0 || len(ocr)*len(vlm) > maxAlignCells {
		return
	}
	pair, cost := Align(ocr, vlm)

	// A line with no well-matched word is not in the transcription (its
	// words only pair with unrelated ones), unless OCR itself doubts the
	// line: then the model's reading of it is the line's text.
	matched := make(map[int]bool, to-from)
	for i, ow := range ocr {
		if pair[i] >= 0 && cost[i] <= GoodMatch {
			matched[ow.Line] = true
		}
	}

	// Span of VLM words per OCR line.
	spanStart := make(map[int]int, to-from)
	spanEnd := make(map[int]int, to-from)
	for i, ow := range ocr {
		if pair[i] < 0 || !matched[ow.Line] && !page.Lines[ow.Line].LowConfidence {
			continue
		}
		vw := vlm[pair[i]]
		if s, ok := spanStart[ow.Line]; !ok || vw.Start < s {
			spanStart[ow.Line] = vw.Start
		}
		if e, ok := spanEnd[ow.Line]; !ok || vw.End > e {
			spanEnd[ow.Line] = vw.End
		}
	}

	runes := []rune(text)
	for li := from; li < to; li++ {
		s, ok := spanStart[li]
		if !ok {
			continue // OCR reference only
		}
		l := &page.Lines[li]
		l.TextOCR = l.Text
		l.Text = string(runes[s:spanEnd[li]])
		l.TextSource = types.TextSourceVLM
		l.LowConfidence = false
	}
}

// syntheticLines gives a transcription without OCR lines one line per text
// line, stacked evenly inside the block box.
func syntheticLines(b *types.ParsedBlock, text string) []types.ParsedLine {
	parts := strings.Split(text, "\n")
	var keep []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			keep = append(keep, p)
		}
	}
	out := make([]types.ParsedLine, 0, len(keep))
	h := b.BBox.Height() / float64(max(len(keep), 1))
	for i, p := range keep {
		bb := types.BBox{X0: b.BBox.X0, Y0: b.BBox.Y0 + float64(i)*h, X1: b.BBox.X1, Y1: b.BBox.Y0 + float64(i+1)*h}
		out = append(out, types.ParsedLine{
			SourceID: -1, BlockNo: b.BlockNo, Text: p, Confidence: 1, Quad: types.QuadFromBBox(bb), BBox: bb,
			InFigure: b.Type == types.BlockFigure, TextSource: types.TextSourceVLM,
		})
	}
	return out
}

// Refined reports whether any block of the page carries a VLM transcription.
func Refined(page *types.ParsedPage) bool {
	for _, b := range page.Blocks {
		if b.TextSource == types.TextSourceVLM {
			return true
		}
	}
	return false
}

// locateLines sets MdStart/MdEnd of the lines of a block rendered verbatim
// from md, which starts at rune offset base of the page markdown: VLM lines,
// and OCR lines whose text md happens to hold verbatim. Lines are searched
// in order so repeated phrases resolve to the right place.
func locateLines(page *types.ParsedPage, lines []int, md string, base int) {
	cursor := 0 // byte offset in md
	for _, li := range lines {
		l := &page.Lines[li]
		if l.Text == "" {
			continue
		}
		i := strings.Index(md[cursor:], l.Text)
		if i < 0 {
			continue
		}
		start := base + utf8.RuneCountInString(md[:cursor+i])
		l.MdStart, l.MdEnd = start, start+utf8.RuneCountInString(l.Text)
		cursor += i + len(l.Text)
	}
}

// tableRowLines returns one synthetic line per table row ("cell | cell"),
// from the block's HTML or, failing that, a markdown pipe table.
func tableRowLines(b *types.ParsedBlock, md string) []types.ParsedLine {
	var rows []string
	if b.HTML != "" {
		for _, r := range TableRows(b.HTML) {
			rows = append(rows, strings.Join(r, " | "))
		}
	} else {
		for _, l := range strings.Split(md, "\n") {
			l = strings.TrimSpace(l)
			if !strings.HasPrefix(l, "|") || strings.Trim(l, "|-: ") == "" {
				continue
			}
			var cells []string
			for _, c := range strings.Split(strings.Trim(l, "|"), "|") {
				cells = append(cells, strings.TrimSpace(c))
			}
			rows = append(rows, strings.Join(cells, " | "))
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return syntheticLines(b, strings.Join(rows, "\n"))
}
