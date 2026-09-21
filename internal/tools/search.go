package tools

import (
	"sort"
	"strings"
	"unicode"
)

const (
	defaultSearchResults = 5
	maxSearchResults     = 20
	selectPrefix         = "select:"
)

// Search looks up deferred tools in the session's catalog. It understands two
// query forms, mirroring Claude Code's ToolSearch:
//
//   - "select:a,b" loads exactly those tools by name (case-insensitive); names
//     that do not exist are returned in missing.
//   - anything else is a keyword search over tool names and descriptions. A
//     word prefixed with "+" is required to appear in the tool name.
//
// Matches are ordered best-first and capped at limit.
func (s *Session) Search(query string, limit int) (matches []*Entry, missing []string) {
	if limit <= 0 {
		limit = defaultSearchResults
	}
	if limit > maxSearchResults {
		limit = maxSearchResults
	}
	query = strings.TrimSpace(query)

	if len(query) >= len(selectPrefix) && strings.EqualFold(query[:len(selectPrefix)], selectPrefix) {
		return s.selectByName(query[len(selectPrefix):])
	}
	return s.keywordSearch(query, limit), nil
}

func (s *Session) selectByName(list string) (matches []*Entry, missing []string) {
	byLower := make(map[string]*Entry, len(s.catalog))
	for n, e := range s.catalog {
		byLower[strings.ToLower(n)] = e
	}
	seen := map[string]bool{}
	for _, raw := range strings.Split(list, ",") {
		name := strings.TrimSpace(raw)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		if e, ok := byLower[strings.ToLower(name)]; ok {
			matches = append(matches, e)
		} else {
			missing = append(missing, name)
		}
	}
	return matches, missing
}

type searchTerm struct {
	text     string
	required bool
}

func (s *Session) keywordSearch(query string, limit int) []*Entry {
	var terms []searchTerm
	for _, f := range strings.Fields(strings.ToLower(query)) {
		req := strings.HasPrefix(f, "+")
		f = strings.TrimLeft(f, "+")
		if f != "" {
			terms = append(terms, searchTerm{text: f, required: req})
		}
	}
	if len(terms) == 0 {
		return nil
	}

	type scored struct {
		e     *Entry
		score int
	}
	var hits []scored
	for _, e := range s.catalog {
		if sc := scoreEntry(e, terms); sc > 0 {
			hits = append(hits, scored{e, sc})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].e.Name < hits[j].e.Name
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]*Entry, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out
}

// scoreEntry ranks one tool against the terms: an exact name word beats a name
// substring, which beats a description hit. It returns 0 when a required term
// is missing from the name or nothing matches at all.
func scoreEntry(e *Entry, terms []searchTerm) int {
	name := strings.ToLower(e.Name)
	desc := strings.ToLower(e.Desc)
	nameWords := wordSet(name)
	descWords := wordSet(desc)

	total := 0
	for _, t := range terms {
		inName := strings.Contains(name, t.text)
		if t.required && !inName {
			return 0
		}
		switch {
		case nameWords[t.text]:
			total += 10
		case inName:
			total += 5
		}
		switch {
		case descWords[t.text]:
			total += 2
		case len(t.text) >= 3 && strings.Contains(desc, t.text):
			total++
		}
	}
	return total
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out[w] = true
	}
	return out
}
