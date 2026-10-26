package textutil

import "testing"

func TestUnaccent(t *testing.T) {
	cases := map[string]string{
		"UBND XÃ NHA BÍCH": "ubnd xa nha bich",
		"Đồng Nai":         "dong nai",
		"Độc lập - Tự do":  "doc lap - tu do",
		"Nguyễn Văn Tình":  "nguyen van tinh",
		"HS-2026-000123":   "hs-2026-000123",
	}
	for in, want := range cases {
		if got := Unaccent(in); got != want {
			t.Errorf("Unaccent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSimilarity(t *testing.T) {
	if s := Similarity("NHA BÍCH", "NHA BÍCH"); s != 1 {
		t.Fatalf("identical = %v", s)
	}
	if s := Similarity(Normalize("NHA BỊCH"), Normalize("NHA BÍCH")); s != 1 {
		t.Fatalf("accent-folded = %v", s)
	}
	if s := Similarity("abc", "xyz"); s != 0 {
		t.Fatalf("disjoint = %v", s)
	}
}

func TestBadCharRatio(t *testing.T) {
	if r := BadCharRatio("abc�"); r != 0.25 {
		t.Fatalf("ratio = %v", r)
	}
	if r := BadCharRatio("Tiếng Việt"); r != 0 {
		t.Fatalf("ratio = %v", r)
	}
}
