package vlm

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/thanhenti/bepaylot/docpipe/parser"
)

// Number check modes (Config.NumberCheck). The VLM reads words and layout
// well but now and then misreads a digit; TurboOCR reads digits better. The
// check compares the numbers of both readings of a page:
//   - off: no check;
//   - detect: conflicts are recorded and every one is reported unverified,
//     the markdown is not changed;
//   - confirm: conflicts are put back to the VLM in the same conversation
//     that read the page, and a number is replaced only when the model is
//     sure of a value that one of the two readings has.
const (
	NumbersOff     = "off"
	NumbersDetect  = "detect"
	NumbersConfirm = "confirm"
)

// Conflict kinds.
const (
	ConflictMismatch = "mismatch" // both readings have a number there, with different values
	ConflictMDOnly   = "md_only"  // the markdown has a number OCR did not read
	ConflictOCROnly  = "ocr_only" // OCR read a number the markdown does not have
)

// DefaultConfirmPrompt asks the VLM, as the next turn of the conversation
// that read the page, to settle the conflicts listed at %s.
const DefaultConfirmPrompt = "Some numbers in your transcription of this page differ from what an OCR engine read on the same page. " +
	"Look at the page image again and give the value printed on the page for each item below.\n\n%s\n" +
	"Answer with a JSON array only, one object per item: " +
	`[{"id":"c1","value":"<the number exactly as printed, empty if it is not on the page>","source":"md|ocr|other","sure":true}]. ` +
	"source is md when your transcription was right, ocr when the OCR reading is right, other when both are wrong. " +
	"Set sure to false whenever the image does not let you tell for certain."

// NumberConflict is one place where the two readings disagree.
type NumberConflict struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// MD and OCR are the numbers as each reading wrote them; one is empty
	// for md_only / ocr_only.
	MD      string  `json:"md,omitempty"`
	OCR     string  `json:"ocr,omitempty"`
	OCRConf float64 `json:"ocr_conf,omitempty"`
	// Context is the line around the number (markdown line, or OCR line for
	// ocr_only), to find it on the page.
	Context string `json:"context,omitempty"`

	mdCanon, ocrCanon string
	mdStart, mdEnd    int // byte span of MD in the checked markdown
}

// NumberResolution is the model's answer for one conflict and what was done
// with it.
type NumberResolution struct {
	ID     string `json:"id"`
	Value  string `json:"value"`
	Source string `json:"source"`
	Sure   bool   `json:"sure"`
	// Verified is set when the answer settled the conflict; Before/After are
	// set when that changed the markdown.
	Verified bool   `json:"verified"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after,omitempty"`
}

// NumberReport is the number check of one page (kept in RawPage.Raw).
type NumberReport struct {
	Mode       string `json:"mode"`
	MDNumbers  int    `json:"md_numbers"`
	OCRNumbers int    `json:"ocr_numbers"`
	Agreed     int    `json:"agreed"`
	// Conflicts are what the comparison found; Resolutions what the model
	// answered (confirm); Unverified the conflicts still open.
	Conflicts   []NumberConflict   `json:"conflicts,omitempty"`
	Resolutions []NumberResolution `json:"resolutions,omitempty"`
	Unverified  []NumberConflict   `json:"unverified,omitempty"`
	// Truncated is set when the markdown was cut at max_tokens: numbers past
	// the cut were not compared.
	Truncated bool        `json:"truncated,omitempty"`
	Call      *CallResult `json:"call,omitempty"`
	Skipped   string      `json:"skipped,omitempty"`
}

// Note renders the unverified numbers as an HTML comment to append to the
// page markdown, or "" when there are none.
func (r *NumberReport) Note() string {
	if r == nil || len(r.Unverified) == 0 {
		return ""
	}
	items := make([]string, 0, len(r.Unverified))
	for _, c := range r.Unverified {
		switch c.Kind {
		case ConflictMismatch:
			items = append(items, fmt.Sprintf("%q (OCR read %q)", c.MD, c.OCR))
		case ConflictMDOnly:
			items = append(items, fmt.Sprintf("%q (not found by OCR)", c.MD))
		case ConflictOCROnly:
			items = append(items, fmt.Sprintf("OCR read %q near %q, missing from the text", c.OCR, c.Context))
		}
	}
	return "<!-- unverified numbers: " + strings.ReplaceAll(strings.Join(items, "; "), "--", "- -") + " -->"
}

var (
	reNumber = regexp.MustCompile(`\(?[-−]?\d(?:[\d.,]*\d)?\)?%?`)
	// Spans of the markdown whose digits are not page text: HTML tags
	// (colspan="2") and figure links (page_10_20_300_400.png).
	reNoNumbers = regexp.MustCompile(`<[^>]*>|!\[[^\]]*\]\([^)]*\)`)
	reTags      = regexp.MustCompile(`<[^>]*>`)
	reSpaces    = regexp.MustCompile(`\s+`)
)

// numTok is one number found in a reading.
type numTok struct {
	raw, canon string
	start, end int
	conf       float64
	context    string
	gap        int // matches before it in its sequence
}

// minDigits leaves out one-digit numbers (list items, note references):
// they are many, rarely matter, and differ in form between readings.
const minDigits = 2

// maxCells caps the alignment table (markdown numbers x OCR numbers).
const maxCells = 4_000_000

// CanonicalNumber returns the value of a number as written, in one form for
// every way of writing it: "1.234.567,89", "1,234,567.89" and "1234567.89"
// are "1234567.89"; "(1.234)" and "-1.234" are "-1234"; "12,5%" is "12.5%".
// A single separator followed by exactly three digits is a thousands
// separator ("1.234" is 1234 in Vietnamese and "1,234" in English). It
// returns "" when s has no digit.
func CanonicalNumber(s string) string {
	s = strings.TrimSpace(s)
	pct := strings.HasSuffix(s, "%")
	s = strings.TrimSuffix(s, "%")
	neg := strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")
	s = strings.Trim(s, "()")
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "−") {
		neg = true
		s = strings.TrimLeft(s, "-−")
	}
	dots, commas := strings.Count(s, "."), strings.Count(s, ",")
	dec := rune(0)
	switch {
	case dots > 0 && commas > 0:
		if strings.LastIndex(s, ".") > strings.LastIndex(s, ",") {
			dec = '.'
		} else {
			dec = ','
		}
	case dots+commas == 1:
		sep := "."
		if commas == 1 {
			sep = ","
		}
		if after := s[strings.Index(s, sep)+1:]; len(after) != 3 {
			dec = rune(sep[0])
		}
	}
	var intPart, frac strings.Builder
	cur := &intPart
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			cur.WriteRune(r)
		case r == dec:
			cur = &frac
		}
	}
	in := strings.TrimLeft(intPart.String(), "0")
	if in == "" {
		in = "0"
	}
	fr := strings.TrimRight(frac.String(), "0")
	if intPart.Len() == 0 && frac.Len() == 0 {
		return ""
	}
	out := in
	if fr != "" {
		out += "." + fr
	}
	if neg && out != "0" {
		out = "-" + out
	}
	if pct {
		out += "%"
	}
	return out
}

func digitCount(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// numbersIn returns the numbers of text, outside the excluded spans.
func numbersIn(text string, skip [][]int) []numTok {
	var out []numTok
	for _, m := range reNumber.FindAllStringIndex(text, -1) {
		if inSpans(m[0], skip) {
			continue
		}
		raw := text[m[0]:m[1]]
		// "(5" or "5)" is not a negative number: drop the lone parenthesis.
		if strings.HasPrefix(raw, "(") != strings.HasSuffix(strings.TrimSuffix(raw, "%"), ")") {
			if strings.HasPrefix(raw, "(") {
				raw, m[0] = raw[1:], m[0]+1
			} else {
				k := strings.IndexByte(raw, ')')
				raw, m[1] = raw[:k], m[0]+k
			}
		}
		if digitCount(raw) < minDigits {
			continue
		}
		if c := CanonicalNumber(raw); c != "" {
			out = append(out, numTok{raw: raw, canon: c, start: m[0], end: m[1]})
		}
	}
	return out
}

func inSpans(pos int, spans [][]int) bool {
	for _, s := range spans {
		if pos >= s[0] && pos < s[1] {
			return true
		}
	}
	return false
}

// markdownNumbers returns the numbers of the page markdown with the line
// each one is on.
func markdownNumbers(md string) []numTok {
	toks := numbersIn(md, reNoNumbers.FindAllStringIndex(md, -1))
	for i := range toks {
		lo := strings.LastIndexByte(md[:toks[i].start], '\n') + 1
		hi := strings.IndexByte(md[toks[i].end:], '\n')
		if hi < 0 {
			hi = len(md)
		} else {
			hi += toks[i].end
		}
		toks[i].context = clip(reSpaces.ReplaceAllString(reTags.ReplaceAllString(md[lo:hi], " "), " "))
	}
	return toks
}

// ocrNumbers returns the numbers of the OCR lines in reading order.
func ocrNumbers(page *parser.RawPage) []numTok {
	var out []numTok
	for _, li := range readingOrder(page) {
		l := page.Lines[li]
		for _, t := range numbersIn(l.Text, nil) {
			t.conf, t.context = l.Confidence, clip(l.Text)
			out = append(out, t)
		}
	}
	return out
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= 160 {
		return s
	}
	r := []rune(s)
	return string(r[:160]) + "…"
}

// DetectNumbers compares the numbers of the page markdown with the numbers
// OCR read on the page. It never changes the markdown.
//
// The two sequences are aligned in reading order (longest common
// subsequence on the values). A value left over on one side that is also
// left over on the other is the same number read in another order (a table
// read by rows against by columns): it agrees. What is left after that is
// paired by position between the same two aligned numbers; a pair of similar
// length is a mismatch, the rest is md_only or ocr_only.
func DetectNumbers(md string, page *parser.RawPage) *NumberReport {
	a, b := markdownNumbers(md), ocrNumbers(page)
	rep := &NumberReport{MDNumbers: len(a), OCRNumbers: len(b)}
	if len(a)*len(b) > maxCells {
		rep.Skipped = fmt.Sprintf("too many numbers to align (%d x %d)", len(a), len(b))
		return rep
	}
	matchA, matchB := lcs(a, b)

	// Leftovers of the same value on both sides: read in another order.
	left := map[string][]int{}
	for j := range b {
		if !matchB[j] {
			left[b[j].canon] = append(left[b[j].canon], j)
		}
	}
	for i := range a {
		if matchA[i] {
			continue
		}
		if js := left[a[i].canon]; len(js) > 0 {
			matchA[i], matchB[js[0]] = true, true
			left[a[i].canon] = js[1:]
		}
	}
	for i := range a {
		if matchA[i] {
			rep.Agreed++
		}
	}

	byGapA, byGapB := map[int][]int{}, map[int][]int{}
	var gaps []int
	seen := map[int]bool{}
	for i := range a {
		if !matchA[i] {
			byGapA[a[i].gap] = append(byGapA[a[i].gap], i)
			if !seen[a[i].gap] {
				seen[a[i].gap], gaps = true, append(gaps, a[i].gap)
			}
		}
	}
	for j := range b {
		if !matchB[j] {
			byGapB[b[j].gap] = append(byGapB[b[j].gap], j)
			if !seen[b[j].gap] {
				seen[b[j].gap], gaps = true, append(gaps, b[j].gap)
			}
		}
	}
	sort.Ints(gaps)
	add := func(c NumberConflict) {
		c.ID = fmt.Sprintf("c%d", len(rep.Conflicts)+1)
		rep.Conflicts = append(rep.Conflicts, c)
	}
	mdConflict := func(t numTok) NumberConflict {
		return NumberConflict{MD: t.raw, mdCanon: t.canon, mdStart: t.start, mdEnd: t.end, Context: t.context}
	}
	for _, g := range gaps {
		as, bs := byGapA[g], byGapB[g]
		k := 0
		for ; k < len(as) && k < len(bs); k++ {
			ta, tb := a[as[k]], b[bs[k]]
			if d := digitCount(ta.raw) - digitCount(tb.raw); d < -1 || d > 1 {
				break
			}
			c := mdConflict(ta)
			c.Kind, c.OCR, c.ocrCanon, c.OCRConf = ConflictMismatch, tb.raw, tb.canon, tb.conf
			add(c)
		}
		for _, i := range as[k:] {
			c := mdConflict(a[i])
			c.Kind = ConflictMDOnly
			add(c)
		}
		for _, j := range bs[k:] {
			add(NumberConflict{Kind: ConflictOCROnly, OCR: b[j].raw, ocrCanon: b[j].canon, OCRConf: b[j].conf, Context: b[j].context, mdStart: -1, mdEnd: -1})
		}
	}
	return rep
}

// lcs aligns a and b on their values and marks the matched tokens; every
// token's gap is set to the number of matches before it.
func lcs(a, b []numTok) (matchA, matchB []bool) {
	n, m := len(a), len(b)
	matchA, matchB = make([]bool, n), make([]bool, m)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i].canon == b[j].canon {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	i, j, g := 0, 0, 0
	for i < n && j < m {
		switch {
		case a[i].canon == b[j].canon:
			a[i].gap, b[j].gap = g, g
			matchA[i], matchB[j] = true, true
			g++
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			a[i].gap = g
			i++
		default:
			b[j].gap = g
			j++
		}
	}
	for ; i < n; i++ {
		a[i].gap = g
	}
	for ; j < m; j++ {
		b[j].gap = g
	}
	return matchA, matchB
}

// confirmPrompt lists the conflicts for the follow-up turn.
func confirmPrompt(tmpl string, conflicts []NumberConflict) string {
	var b strings.Builder
	for _, c := range conflicts {
		switch c.Kind {
		case ConflictMismatch:
			fmt.Fprintf(&b, "- %s: you wrote %q, OCR read %q. Context: %q\n", c.ID, c.MD, c.OCR, c.Context)
		case ConflictMDOnly:
			fmt.Fprintf(&b, "- %s: you wrote %q, OCR found no such number. Context: %q\n", c.ID, c.MD, c.Context)
		case ConflictOCROnly:
			fmt.Fprintf(&b, "- %s: OCR read %q, which your transcription does not contain. Context: %q\n", c.ID, c.OCR, c.Context)
		}
	}
	if !strings.Contains(tmpl, "%s") {
		return tmpl + "\n\n" + b.String()
	}
	return fmt.Sprintf(tmpl, b.String())
}

type confirmAnswer struct {
	ID     string          `json:"id"`
	Value  json.RawMessage `json:"value"`
	Source string          `json:"source"`
	Sure   bool            `json:"sure"`
}

// parseConfirm reads the model's JSON array of answers, keyed by id.
func parseConfirm(text string) (map[string]NumberResolution, error) {
	_, text = SplitFrontMatter(text)
	lo, hi := strings.IndexByte(text, '['), strings.LastIndexByte(text, ']')
	if lo < 0 || hi < lo {
		return nil, errors.New("the answer has no JSON array")
	}
	var raw []confirmAnswer
	if err := json.Unmarshal([]byte(text[lo:hi+1]), &raw); err != nil {
		return nil, fmt.Errorf("the answer is not valid JSON: %w", err)
	}
	out := make(map[string]NumberResolution, len(raw))
	for _, r := range raw {
		v := strings.TrimSpace(string(r.Value))
		var s string
		if err := json.Unmarshal(r.Value, &s); err == nil {
			v = strings.TrimSpace(s)
		}
		if v == "null" {
			v = ""
		}
		out[strings.TrimSpace(r.ID)] = NumberResolution{ID: strings.TrimSpace(r.ID), Value: v, Source: strings.ToLower(strings.TrimSpace(r.Source)), Sure: r.Sure}
	}
	return out, nil
}

// applyConfirm settles the conflicts with the model's answers. A number is
// replaced only when the model is sure and its value is the other reading;
// every conflict not settled is returned unverified.
func applyConfirm(md string, conflicts []NumberConflict, answers map[string]NumberResolution) (string, []NumberResolution, []NumberConflict) {
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	var res []NumberResolution
	var open []NumberConflict
	for _, c := range conflicts {
		ans, ok := answers[c.ID]
		if !ok {
			open = append(open, c)
			continue
		}
		v := CanonicalNumber(ans.Value)
		switch {
		case !ans.Sure:
		case c.Kind == ConflictMismatch && v == c.mdCanon:
			ans.Verified = true
		case c.Kind == ConflictMismatch && v == c.ocrCanon:
			ans.Verified, ans.Before, ans.After = true, c.MD, ans.Value
			edits = append(edits, edit{c.mdStart, c.mdEnd, ans.Value})
		case c.Kind == ConflictMDOnly && ans.Source == "md" && v == c.mdCanon:
			ans.Verified = true
		case c.Kind == ConflictOCROnly && ans.Source == "md":
			// OCR read something that is not a number of the page.
			ans.Verified = true
		}
		res = append(res, ans)
		if !ans.Verified {
			open = append(open, c)
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, e := range edits {
		md = md[:e.start] + e.text + md[e.end:]
	}
	return md, res, open
}
