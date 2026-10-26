// Package textutil holds small, dependency-free text helpers shared by the
// parser, index and graph modules: Vietnamese-aware accent folding, string
// similarity and a rough token estimate.
package textutil

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Unaccent lower-cases s and strips diacritics, mapping đ/Đ to d. It matches
// the SQL function unaccent_vi closely enough for client-side comparisons.
func Unaccent(s string) string {
	s = strings.NewReplacer("đ", "d", "Đ", "D").Replace(s)
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	return strings.ToLower(out)
}

// CollapseSpace trims s and collapses runs of whitespace into one space.
func CollapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// Normalize is the comparison form: accent-folded, lower-case, collapsed.
func Normalize(s string) string { return CollapseSpace(Unaccent(s)) }

// Similarity returns 1 - levenshtein(a,b)/max(len) over runes, in [0,1].
func Similarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	d := levenshtein(ra, rb)
	return 1 - float64(d)/float64(max(len(ra), len(rb)))
}

func levenshtein(a, b []rune) int {
	if len(a) < len(b) {
		a, b = b, a
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// ContainsFold reports whether needle occurs in haystack after Normalize.
func ContainsFold(haystack, needle string) bool {
	return strings.Contains(Normalize(haystack), Normalize(needle))
}

// EstimateTokens is a cheap token estimate (~4 bytes per token for Latin
// scripts; Vietnamese diacritics inflate bytes, so runes/3 is used).
func EstimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return n/3 + 1
}

// RuneLen is utf8.RuneCountInString.
func RuneLen(s string) int { return utf8.RuneCountInString(s) }

// BadCharRatio is the share of runes that are replacement, private-use or
// control characters (excluding whitespace).
func BadCharRatio(s string) float64 {
	var bad, total int
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if r == utf8.RuneError || unicode.Is(unicode.Co, r) || unicode.IsControl(r) {
			bad++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(bad) / float64(total)
}

// Truncate cuts s to at most n runes, adding an ellipsis when cut.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// ValueKey is the comparison form of a field value (§6.9.2): accent-folded
// and lower-case; a value made of digits and separators keeps only its
// digits, so 15.000.000.000, "15 000 000 000" and 15000000000 compare equal,
// and so do 0101-234-567 and 0101234567; other punctuation and spacing is
// ignored.
func ValueKey(s string) string {
	s = Unaccent(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	digits, numeric := strings.Builder{}, true
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			digits.WriteRune(r)
		case strings.ContainsRune(" .,-/_'", r):
		default:
			numeric = false
		}
	}
	if numeric && digits.Len() > 0 {
		return digits.String()
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ValueIn reports whether value appears in text by ValueKey: numbers by their
// digits, text by its letters and digits.
func ValueIn(value, text string) bool {
	v := ValueKey(value)
	if v == "" {
		return false
	}
	var digits strings.Builder
	for _, r := range text {
		if unicode.IsDigit(r) {
			digits.WriteRune(r)
		}
	}
	if strings.Contains(digits.String(), v) {
		return true
	}
	var b strings.Builder
	for _, r := range Unaccent(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return strings.Contains(b.String(), v)
}

// ValueText is the text form of a JSON value (a field value, §6.9.3).
func ValueText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatFloat(x, 'f', 0, 64)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// ParseNumber reads a Vietnamese or English number: "15.000.000", "15,000,000",
// "1.234,5", "1,234.5", "15 000 000 đ", "-2.000 VND".
func ParseNumber(s string) (float64, bool) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsDigit(r), r == '.', r == ',', r == '-':
			b.WriteRune(r)
		case unicode.IsSpace(r):
		case unicode.IsLetter(r): // currency: đ, VND, đồng
		default:
			return 0, false
		}
	}
	t := b.String()
	if t == "" || strings.Trim(t, ".,-") == "" {
		return 0, false
	}
	dots, commas := strings.Count(t, "."), strings.Count(t, ",")
	switch {
	case dots > 0 && commas > 0: // the last one is the decimal mark
		if strings.LastIndex(t, ",") > strings.LastIndex(t, ".") {
			t = strings.ReplaceAll(t, ".", "")
			t = strings.ReplaceAll(t, ",", ".")
		} else {
			t = strings.ReplaceAll(t, ",", "")
		}
	case dots > 1:
		t = strings.ReplaceAll(t, ".", "")
	case commas > 1:
		t = strings.ReplaceAll(t, ",", "")
	case dots == 1 && len(t)-strings.Index(t, ".")-1 == 3: // 15.000: thousands
		t = strings.ReplaceAll(t, ".", "")
	case commas == 1 && len(t)-strings.Index(t, ",")-1 == 3:
		t = strings.ReplaceAll(t, ",", "")
	case commas == 1:
		t = strings.ReplaceAll(t, ",", ".")
	}
	x, err := strconv.ParseFloat(t, 64)
	return x, err == nil
}
