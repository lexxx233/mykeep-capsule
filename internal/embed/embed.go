// Package embed provides local, CPU-only, pure-Go text embeddings (PLAN §9).
// mykeep runs no LLM; the only model it runs is the embedder, and it's local.
package embed

import (
	"context"
	"hash/fnv"
	"strings"

	"mykeep.ai/internal/vector"
)

// Embedder turns text into fixed-dimension unit vectors.
type Embedder interface {
	Name() string
	Dim() int
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}

// HashEmbedder is the deterministic, model-free fallback used only when the local
// model file is missing/corrupt (PLAN §9.2, §9.4). FNV-1a feature hashing of word
// tokens + char 3-grams into signed buckets, then L2-normalized.
type HashEmbedder struct{ D int }

func NewHashEmbedder(dim int) *HashEmbedder {
	if dim <= 0 {
		dim = 384
	}
	return &HashEmbedder{D: dim}
}

func (h *HashEmbedder) Name() string { return "hash" }
func (h *HashEmbedder) Dim() int     { return h.D }

func (h *HashEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = h.embed(t)
	}
	return out, nil
}

func (h *HashEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	return h.embed(text), nil
}

func (h *HashEmbedder) embed(text string) []float32 {
	v := make([]float32, h.D)
	for _, feat := range features(text) {
		sum := fnv.New32a()
		_, _ = sum.Write([]byte(feat))
		hv := sum.Sum32()
		bucket := int(hv % uint32(h.D))
		if hv&0x80000000 != 0 {
			v[bucket]++
		} else {
			v[bucket]--
		}
	}
	return vector.Normalize(v)
}

// features yields lowercased word tokens plus character 3-grams.
func features(text string) []string {
	text = strings.ToLower(text)
	var feats []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			w := b.String()
			feats = append(feats, "w:"+w)
			r := []rune(w)
			for i := 0; i+3 <= len(r); i++ {
				feats = append(feats, "g:"+string(r[i:i+3]))
			}
			b.Reset()
		}
	}
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return feats
}
