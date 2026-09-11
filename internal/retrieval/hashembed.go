package retrieval

import (
	"context"
	"hash/fnv"
	"math"
	"regexp"
	"strings"
)

// HashEmbedder is a dependency-free, deterministic embedder based on the
// hashing trick: tokens are hashed into buckets with sub-linear term weighting,
// then the vector is L2-normalised. Quality is modest but stable, which makes
// it ideal for local development and reproducible tests. Cosine similarity
// between related skill descriptions and queries is meaningfully positive.
type HashEmbedder struct{ dim int }

// NewHashEmbedder returns a HashEmbedder with the given dimension (default 1536).
func NewHashEmbedder(dim int) *HashEmbedder {
	if dim <= 0 {
		dim = 1536
	}
	return &HashEmbedder{dim: dim}
}

func (h *HashEmbedder) Dim() int     { return h.dim }
func (h *HashEmbedder) Name() string { return "hash" }

var tokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

func (h *HashEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = h.embedOne(t)
	}
	return out, nil
}

func (h *HashEmbedder) embedOne(text string) []float32 {
	vec := make([]float32, h.dim)
	counts := map[string]int{}
	for _, tok := range tokenRe.FindAllString(strings.ToLower(text), -1) {
		if len(tok) < 2 {
			continue
		}
		counts[tok]++
	}
	for tok, n := range counts {
		// sub-linear term frequency
		w := 1 + math.Log(float64(n))
		// two hashed features per token (unigram + character 3-gram signature)
		for _, feat := range []string{tok, "3g:" + charTrigramSig(tok)} {
			bucket, sign := hashBucket(feat, h.dim)
			vec[bucket] += float32(sign) * float32(w)
		}
	}
	l2Normalize(vec)
	return vec
}

func hashBucket(s string, dim int) (bucket int, sign int) {
	fh := fnv.New64a()
	_, _ = fh.Write([]byte(s))
	sum := fh.Sum64()
	bucket = int(sum % uint64(dim))
	if sum&(1<<63) != 0 {
		return bucket, -1
	}
	return bucket, 1
}

func charTrigramSig(tok string) string {
	if len(tok) <= 3 {
		return tok
	}
	fh := fnv.New32a()
	for i := 0; i+3 <= len(tok); i++ {
		_, _ = fh.Write([]byte(tok[i : i+3]))
	}
	var b [4]byte
	v := fh.Sum32()
	b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	return string(b[:])
}

func l2Normalize(vec []float32) {
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= inv
	}
}
