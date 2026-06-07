package embed

import (
	"context"
	"math"
	"testing"

	"joyvend.io/internal/vector"
)

// HashEmbedder must satisfy the Embedder interface.
var _ Embedder = (*HashEmbedder)(nil)

const l2Tol = 1e-5

func l2Norm(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

func equalVec(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// allZero reports whether every component is exactly zero (the degenerate
// embedding produced by text with no word/3-gram features).
func allZero(v []float32) bool {
	for _, x := range v {
		if x != 0 {
			return false
		}
	}
	return true
}

func TestNewHashEmbedder_DimDefaulting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      int
		wantDim int
	}{
		{"explicit positive", 128, 128},
		{"explicit one", 1, 1},
		{"explicit 384", 384, 384},
		{"zero defaults to 384", 0, 384},
		{"negative defaults to 384", -7, 384},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := NewHashEmbedder(tt.in)
			if h == nil {
				t.Fatal("NewHashEmbedder returned nil")
			}
			if got := h.Dim(); got != tt.wantDim {
				t.Errorf("Dim() = %d, want %d", got, tt.wantDim)
			}
			if h.D != tt.wantDim {
				t.Errorf("D field = %d, want %d", h.D, tt.wantDim)
			}
		})
	}
}

func TestHashEmbedder_Name(t *testing.T) {
	t.Parallel()
	if got := NewHashEmbedder(384).Name(); got != "hash" {
		t.Errorf("Name() = %q, want %q", got, "hash")
	}
}

// Dim() must match the length of the vectors actually produced.
func TestHashEmbedder_DimMatchesOutputLength(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, dim := range []int{1, 8, 64, 384, 512} {
		h := NewHashEmbedder(dim)
		q, err := h.EmbedQuery(ctx, "the quick brown fox")
		if err != nil {
			t.Fatalf("dim=%d EmbedQuery error: %v", dim, err)
		}
		if len(q) != dim {
			t.Errorf("dim=%d: EmbedQuery len = %d, want %d", dim, len(q), dim)
		}
		if h.Dim() != len(q) {
			t.Errorf("dim=%d: Dim() = %d, want %d", dim, h.Dim(), len(q))
		}
		docs, err := h.EmbedDocuments(ctx, []string{"hello world"})
		if err != nil {
			t.Fatalf("dim=%d EmbedDocuments error: %v", dim, err)
		}
		if len(docs[0]) != dim {
			t.Errorf("dim=%d: EmbedDocuments[0] len = %d, want %d", dim, len(docs[0]), dim)
		}
	}
}

// The embedding must be deterministic: identical text yields identical vectors
// across repeated calls and across EmbedQuery / EmbedDocuments.
func TestHashEmbedder_Deterministic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := NewHashEmbedder(384)

	texts := []string{
		"the quick brown fox jumps over the lazy dog",
		"joyvend stores memories for ai agents",
		"UPPER and lower MiXeD case 123",
		"single",
		"",
	}

	for _, text := range texts {
		first, err := h.EmbedQuery(ctx, text)
		if err != nil {
			t.Fatalf("EmbedQuery(%q) error: %v", text, err)
		}
		// Repeated calls are stable.
		for i := 0; i < 5; i++ {
			again, err := h.EmbedQuery(ctx, text)
			if err != nil {
				t.Fatalf("EmbedQuery(%q) repeat error: %v", text, err)
			}
			if !equalVec(first, again) {
				t.Fatalf("EmbedQuery(%q) not deterministic on call %d", text, i)
			}
		}
		// EmbedDocuments must agree with EmbedQuery for the same text.
		docs, err := h.EmbedDocuments(ctx, []string{text})
		if err != nil {
			t.Fatalf("EmbedDocuments(%q) error: %v", text, err)
		}
		if !equalVec(first, docs[0]) {
			t.Errorf("EmbedDocuments(%q) disagrees with EmbedQuery", text)
		}
	}

	// A fresh embedder of the same dim must yield the same vectors (no hidden
	// per-instance state / no seeding from randomness).
	h2 := NewHashEmbedder(384)
	a, _ := h.EmbedQuery(ctx, "consistency across instances")
	b, _ := h2.EmbedQuery(ctx, "consistency across instances")
	if !equalVec(a, b) {
		t.Error("two HashEmbedder instances produced different vectors")
	}
}

// Non-empty text (with at least one word/3-gram feature) must be L2-normalized
// to unit length.
func TestHashEmbedder_L2Normalized(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := NewHashEmbedder(384)

	tests := []struct {
		name string
		text string
	}{
		{"sentence", "the quick brown fox jumps over the lazy dog"},
		{"short word", "cat"},
		{"two chars only word tokens", "hi yo ok"},
		{"digits", "year 2026 month 06"},
		{"punctuation separated", "alpha,beta;gamma.delta!epsilon"},
		{"repeated words", "buffalo buffalo buffalo buffalo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, err := h.EmbedQuery(ctx, tt.text)
			if err != nil {
				t.Fatalf("EmbedQuery error: %v", err)
			}
			n := l2Norm(v)
			if math.Abs(n-1.0) > l2Tol {
				t.Errorf("L2 norm = %v, want ~1.0 (tol %v) for %q", n, l2Tol, tt.text)
			}
		})
	}
}

// Empty text and text with no extractable features yield a zero vector (norm 0),
// which Normalize must return as-is rather than dividing by zero (NaN guard).
func TestHashEmbedder_FeaturelessIsZeroVector(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := NewHashEmbedder(384)

	for _, text := range []string{"", "   ", "!!! ... ???", "\t\n  -- "} {
		v, err := h.EmbedQuery(ctx, text)
		if err != nil {
			t.Fatalf("EmbedQuery(%q) error: %v", text, err)
		}
		if len(v) != 384 {
			t.Fatalf("EmbedQuery(%q) len = %d, want 384", text, len(v))
		}
		if !allZero(v) {
			t.Errorf("EmbedQuery(%q) expected zero vector for featureless text", text)
		}
		for i, x := range v {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				t.Errorf("EmbedQuery(%q)[%d] = %v, want finite", text, i, x)
			}
		}
	}
}

// The core similarity property: a document that shares words with the query
// scores higher (cosine via vector.Dot of unit vectors) than an unrelated
// document.
func TestHashEmbedder_SimilarityRanksSharedWordsHigher(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := NewHashEmbedder(384)

	tests := []struct {
		name      string
		query     string
		related   string
		unrelated string
	}{
		{
			name:      "fox sentence",
			query:     "the quick brown fox",
			related:   "a quick brown fox ran across the field",
			unrelated: "stock market interest rates rose sharply today",
		},
		{
			name:      "memory domain",
			query:     "store memories for ai agents",
			related:   "joyvend can store and recall memories for agents",
			unrelated: "the weather in paris was sunny and warm",
		},
		{
			name:      "exact word overlap",
			query:     "database encryption password",
			related:   "encryption of the database with a password",
			unrelated: "bananas grow in tropical climates near beaches",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			qv, err := h.EmbedQuery(ctx, tt.query)
			if err != nil {
				t.Fatalf("EmbedQuery error: %v", err)
			}
			docs, err := h.EmbedDocuments(ctx, []string{tt.related, tt.unrelated})
			if err != nil {
				t.Fatalf("EmbedDocuments error: %v", err)
			}
			simRelated := vector.Dot(qv, docs[0])
			simUnrelated := vector.Dot(qv, docs[1])

			if simRelated <= simUnrelated {
				t.Errorf("expected related sim (%v) > unrelated sim (%v)\n  query:     %q\n  related:   %q\n  unrelated: %q",
					simRelated, simUnrelated, tt.query, tt.related, tt.unrelated)
			}
			// Sanity: identical text to the query must score >= a merely related doc.
			selfDocs, err := h.EmbedDocuments(ctx, []string{tt.query})
			if err != nil {
				t.Fatalf("EmbedDocuments(self) error: %v", err)
			}
			simSelf := vector.Dot(qv, selfDocs[0])
			if simSelf < simRelated-l2Tol {
				t.Errorf("self-similarity (%v) should be >= related (%v)", simSelf, simRelated)
			}
			// Self-similarity of unit vectors is ~1.
			if math.Abs(simSelf-1.0) > 1e-4 {
				t.Errorf("self-similarity = %v, want ~1.0", simSelf)
			}
		})
	}
}

// EmbedDocuments output length and ordering must match its input slice exactly,
// including the empty-input edge case.
func TestHashEmbedder_EmbedDocumentsLengthMatchesInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := NewHashEmbedder(64)

	tests := []struct {
		name  string
		texts []string
	}{
		{"empty slice", []string{}},
		{"single", []string{"alpha"}},
		{"several", []string{"alpha", "beta", "gamma", "delta"}},
		{"with empty strings", []string{"alpha", "", "gamma"}},
		{"duplicates", []string{"same", "same", "same"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := h.EmbedDocuments(ctx, tt.texts)
			if err != nil {
				t.Fatalf("EmbedDocuments error: %v", err)
			}
			if len(out) != len(tt.texts) {
				t.Fatalf("len(out) = %d, want %d", len(out), len(tt.texts))
			}
			for i, v := range out {
				if len(v) != h.Dim() {
					t.Errorf("out[%d] len = %d, want %d", i, len(v), h.Dim())
				}
			}
			// Per-input correspondence: each output equals embedding that text alone.
			for i, txt := range tt.texts {
				want, err := h.EmbedQuery(ctx, txt)
				if err != nil {
					t.Fatalf("EmbedQuery error: %v", err)
				}
				if !equalVec(out[i], want) {
					t.Errorf("out[%d] does not match EmbedQuery(%q)", i, txt)
				}
			}
		})
	}

	// Identical inputs must produce identical (duplicate) outputs.
	dup, err := h.EmbedDocuments(ctx, []string{"repeat", "repeat"})
	if err != nil {
		t.Fatalf("EmbedDocuments error: %v", err)
	}
	if !equalVec(dup[0], dup[1]) {
		t.Error("duplicate inputs produced differing outputs")
	}
}

// Case-insensitivity: features() lowercases, so case must not change the vector.
func TestHashEmbedder_CaseInsensitive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := NewHashEmbedder(128)
	lower, _ := h.EmbedQuery(ctx, "the quick brown fox")
	upper, _ := h.EmbedQuery(ctx, "THE QUICK BROWN FOX")
	if !equalVec(lower, upper) {
		t.Error("embedding is case-sensitive; expected case-insensitive features")
	}
}
