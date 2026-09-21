package store

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"clean text is untouched", "Xin chào — bạn khỏe không? 🙂", "Xin chào — bạn khỏe không? 🙂"},
		{"single legacy-codepage byte", "5\xba tầng", "5� tầng"},
		{"character cut in half", "ạ"[:2], "�"},
		{"NUL is dropped", "a\x00b", "ab"},
		{"both", "\xff\x00\xba", "��"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		got := cleanText(c.in)
		if got != c.want || !utf8.ValidString(got) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCleanJSON(t *testing.T) {
	in := map[string]any{
		"text":   "a\x00b\xba",
		"k\x00y": []any{"x\x00", int64(9007199254740993), map[string]any{"n": "\x00"}},
		"big":    int64(1547961374470),
	}
	b, err := cleanJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, `\u0000`) || !utf8.Valid(b) {
		t.Fatalf("NUL or invalid UTF-8 survived: %s", s)
	}
	// Numbers must keep their exact digits even when the tree is rewritten.
	for _, want := range []string{`9007199254740993`, `1547961374470`, `"ky"`, `"ab�"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}

	// Values with nothing to strip take the fast path unchanged.
	b, _ = cleanJSON(map[string]any{"a": "b"})
	if string(b) != `{"a":"b"}` {
		t.Errorf("clean value changed: %s", b)
	}
}

func TestCleanPtr(t *testing.T) {
	if cleanPtr(nil) != nil {
		t.Error("nil must stay nil")
	}
	s := "a\x00"
	if got := cleanPtr(&s); *got != "a" {
		t.Errorf("got %q", *got)
	}
}
