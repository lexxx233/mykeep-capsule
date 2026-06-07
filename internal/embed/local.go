package embed

import (
	"context"
	"fmt"
	"sync"

	"github.com/nlpodyssey/cybertron/pkg/models/bert"
	"github.com/nlpodyssey/cybertron/pkg/tasks"
	"github.com/nlpodyssey/cybertron/pkg/tasks/textencoding"
	"github.com/nlpodyssey/spago/mat"

	"joyvend.io/internal/vector"
)

// DefaultModel is the verified default embedder (PLAN §9.2.1, D17): BERT/WordPiece,
// CLS pooling, 384-dim. all-MiniLM-L6-v2 is a lighter alternative.
const DefaultModel = "BAAI/bge-small-en-v1.5"

// LocalEmbedder runs a sentence-transformer locally on CPU via cybertron/spaGO
// (pure-Go, no CGo). Proven in PLAN §9.2.1.
type LocalEmbedder struct {
	model   string
	pooling int
	enc     textencoding.Interface
	dim     int
	mu      sync.Mutex // cybertron encode is not guaranteed goroutine-safe
}

// NewLocalEmbedder loads (downloading + converting on first use into modelsDir)
// the given model. bge-* models use CLS pooling; *MiniLM* use mean pooling.
func NewLocalEmbedder(ctx context.Context, modelsDir, model string) (*LocalEmbedder, error) {
	if model == "" {
		model = DefaultModel
	}
	enc, err := tasks.Load[textencoding.Interface](&tasks.Config{
		ModelsDir:        modelsDir,
		ModelName:        model,
		DownloadPolicy:   tasks.DownloadMissing,
		ConversionPolicy: tasks.ConvertMissing,
	})
	if err != nil {
		return nil, fmt.Errorf("load embedder %q: %w", model, err)
	}
	e := &LocalEmbedder{model: model, pooling: poolingFor(model), enc: enc}
	// Pin the dimension by encoding a probe.
	v, err := e.encode(ctx, "probe")
	if err != nil {
		return nil, err
	}
	e.dim = len(v)
	return e, nil
}

func poolingFor(model string) int {
	// sentence-transformers MiniLM use mean pooling; bge use CLS.
	if contains(model, "MiniLM") || contains(model, "minilm") {
		return int(bert.MeanPooling)
	}
	return int(bert.ClsTokenPooling)
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func (e *LocalEmbedder) Name() string { return "local:" + e.model }
func (e *LocalEmbedder) Dim() int     { return e.dim }

func (e *LocalEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := e.encode(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = vector.Normalize(v)
	}
	return out, nil
}

func (e *LocalEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	v, err := e.encode(ctx, text)
	if err != nil {
		return nil, err
	}
	return vector.Normalize(v), nil
}

func (e *LocalEmbedder) encode(ctx context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, err := e.enc.Encode(ctx, text, e.pooling)
	if err != nil {
		return nil, err
	}
	return toFloat32(r.Vector), nil
}

func toFloat32(m mat.Matrix) []float32 {
	switch v := m.(type) {
	case *mat.Dense[float32]:
		return append([]float32(nil), v.Data().F32()...)
	case *mat.Dense[float64]:
		f := v.Data().F64()
		out := make([]float32, len(f))
		for i, x := range f {
			out[i] = float32(x)
		}
		return out
	}
	return nil
}
