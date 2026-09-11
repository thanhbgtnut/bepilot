package skills

import "testing"

func TestParse(t *testing.T) {
	src := []byte("---\nname: PDF Forms\ndescription: Fill and flatten PDF forms.\nallowed_tools:\n  - http_fetch\n  - http_fetch\n---\n# Body\n\nSome instructions.\n")
	p, err := Parse("pdf-forms", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Name != "PDF Forms" || p.Slug != "pdf-forms" {
		t.Fatalf("unexpected name/slug: %+v", p)
	}
	if p.Description != "Fill and flatten PDF forms." {
		t.Fatalf("description: %q", p.Description)
	}
	if len(p.AllowedTools) != 1 || p.AllowedTools[0] != "http_fetch" {
		t.Fatalf("allowed tools not deduped: %v", p.AllowedTools)
	}
	if p.Body != "# Body\n\nSome instructions." {
		t.Fatalf("body: %q", p.Body)
	}
	if p.Checksum == "" {
		t.Fatal("empty checksum")
	}
}

func TestParseRejectsMissingFrontmatter(t *testing.T) {
	if _, err := Parse("x", []byte("no frontmatter here")); err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseRejectsMissingDescription(t *testing.T) {
	if _, err := Parse("x", []byte("---\nname: X\n---\nbody")); err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestParseChecksumStable(t *testing.T) {
	src := []byte("---\ndescription: d\n---\nbody")
	a, _ := Parse("s", src)
	b, _ := Parse("s", src)
	if a.Checksum != b.Checksum {
		t.Fatal("checksum not stable")
	}
}
