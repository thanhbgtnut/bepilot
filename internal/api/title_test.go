package api

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSessionTitleNeverSplitsACharacter(t *testing.T) {
	if got := sessionTitle("ngắn", 60); got != "ngắn" {
		t.Errorf("short text changed: %q", got)
	}
	// 100 three-byte characters: a byte slice at 60 would land mid-character.
	long := strings.Repeat("ạ", 100)
	got := sessionTitle(long, 60)
	if !utf8.ValidString(got) {
		t.Fatalf("title is not valid UTF-8: %q", got)
	}
	if want := strings.Repeat("ạ", 60) + "…"; got != want {
		t.Errorf("got %d runes, want 60 + ellipsis", utf8.RuneCountInString(got))
	}
	// Trailing space before the cut is trimmed, as before.
	if got := sessionTitle(strings.Repeat("a", 59)+" "+strings.Repeat("b", 10), 60); strings.Contains(got, " …") {
		t.Errorf("space left before the ellipsis: %q", got)
	}
}
