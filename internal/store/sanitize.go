package store

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Postgres text columns reject invalid UTF-8 and NUL, and jsonb rejects a NUL
// escape. Text reaching the store comes from places that cannot promise
// either: tool output (web pages and MCP servers in legacy code pages, bodies
// cut mid-character), model output, and user input. One rejected byte would
// fail the whole transaction and lose a finished turn, so everything is
// normalised here, at the single door into the database.

// cleanText returns s as valid UTF-8 without NUL: invalid bytes become U+FFFD
// and NUL is dropped. Clean input is returned untouched.
func cleanText(s string) string {
	if !strings.ContainsRune(s, 0) && strings.ToValidUTF8(s, "") == s {
		return s
	}
	return strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\x00", "")
}

// cleanPtr is cleanText for an optional column (nil stays nil).
func cleanPtr(p *string) *string {
	if p == nil {
		return nil
	}
	c := cleanText(*p)
	return &c
}

// cleanJSON marshals v for a jsonb column. encoding/json already turns invalid
// UTF-8 into U+FFFD; what remains is the NUL escape, which jsonb refuses, so
// it is removed from every string and key. Numbers keep their exact text.
func cleanJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || !bytes.Contains(b, []byte(`\u0000`)) {
		return b, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	return json.Marshal(stripNUL(tree))
}

func stripNUL(v any) any {
	switch t := v.(type) {
	case string:
		return strings.ReplaceAll(t, "\x00", "")
	case []any:
		for i := range t {
			t[i] = stripNUL(t[i])
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[strings.ReplaceAll(k, "\x00", "")] = stripNUL(x)
		}
		return out
	}
	return v
}
