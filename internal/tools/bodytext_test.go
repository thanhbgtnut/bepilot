package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBodyText(t *testing.T) {
	// A response cut by the byte cap in the middle of a 3-byte character.
	cut := []byte(strings.Repeat("a", 10) + "ạ")[:12]
	if got := bodyText(cut, true); got != strings.Repeat("a", 10) {
		t.Errorf("cut character should be dropped, got %q", got)
	}
	// Not truncated: a lone bad byte becomes U+FFFD, valid text is untouched.
	if got := bodyText([]byte("5\xba tầng"), false); got != "5� tầng" || !utf8.ValidString(got) {
		t.Errorf("got %q", got)
	}
	if got := bodyText([]byte("ạ"), true); got != "ạ" {
		t.Errorf("a complete final character must be kept, got %q", got)
	}
	if got := bodyText(nil, true); got != "" {
		t.Errorf("got %q", got)
	}
}
