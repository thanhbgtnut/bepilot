package retrieval

import (
	"context"
	"math"
	"testing"
)

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func TestHashEmbedderDeterministic(t *testing.T) {
	e := NewHashEmbedder(256)
	v1, _ := e.Embed(context.Background(), []string{"fill in a pdf form"})
	v2, _ := e.Embed(context.Background(), []string{"fill in a pdf form"})
	for i := range v1[0] {
		if v1[0][i] != v2[0][i] {
			t.Fatalf("not deterministic at %d", i)
		}
	}
}

func TestHashEmbedderSemanticOrdering(t *testing.T) {
	e := NewHashEmbedder(2048)
	q, _ := e.Embed(context.Background(), []string{"help me complete a fillable pdf form with my details"})
	docs, _ := e.Embed(context.Background(), []string{
		"PDF Forms: inspect, fill and flatten acroform pdf form fields",
		"Tabular Data Cleanup: deduplicate rows and normalize messy csv categories",
	})
	relevant := cosine(q[0], docs[0])
	irrelevant := cosine(q[0], docs[1])
	if relevant <= irrelevant {
		t.Fatalf("expected pdf doc to score higher: relevant=%.3f irrelevant=%.3f", relevant, irrelevant)
	}
}
