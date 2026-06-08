package retrieval

import (
	"context"
	"strings"
	"testing"

	"mykeep.ai/internal/domain"
	"mykeep.ai/internal/embed"
	"mykeep.ai/internal/store"
)

func TestRecallExcludesCapturesByDefault(t *testing.T) {
	ctx := context.Background()
	s := newStore(t) // helper from recall_test.go
	e := embed.NewHashEmbedder(128)

	add := func(content string, tags []string) {
		vecs, err := e.EmbedDocuments(ctx, []string{content})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Retain("b", []store.MemoryInput{{
			Content: content, Embedding: vecs[0], EmbedModel: e.Name(), FactType: "experience", Tags: tags,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	const curated = "the deploy key rotates monthly"
	const capture = "user: when does the deploy key rotate"
	add(curated, nil)
	add(capture, []string{domain.CaptureTag})

	r := New(s, e)
	contains := func(rs []domain.RecallResult, text string) bool {
		for _, x := range rs {
			if strings.Contains(x.Text, text) {
				return true
			}
		}
		return false
	}

	// Default: curated visible, capture hidden.
	def, err := r.Recall(ctx, "b", domain.RecallRequest{Query: "deploy key rotate"})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(def.Results, curated) {
		t.Fatalf("default recall missing the curated memory: %v", def.Results)
	}
	if contains(def.Results, capture) {
		t.Fatalf("default recall leaked a capture row: %v", def.Results)
	}

	// include_captures: capture surfaces too.
	inc, err := r.Recall(ctx, "b", domain.RecallRequest{Query: "deploy key rotate", IncludeCaptures: true})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(inc.Results, capture) {
		t.Fatalf("include_captures should surface the capture: %v", inc.Results)
	}
}
