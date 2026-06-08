package retrieval

import (
	"context"
	"testing"

	"mykeep.ai/internal/domain"
	"mykeep.ai/internal/embed"
	"mykeep.ai/internal/store"
)

func TestReflectReturnsResultsAndEntities(t *testing.T) {
	ctx := context.Background()
	s := newStore(t) // helper from recall_test.go
	e := embed.NewHashEmbedder(128)

	add := func(content, entity string) {
		vecs, err := e.EmbedDocuments(ctx, []string{content})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Retain("b", []store.MemoryInput{{
			Content: content, Embedding: vecs[0], EmbedModel: e.Name(),
			Entities: []domain.EntityInput{{Text: entity}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	add("Alice loves rock climbing on weekends", "Alice")
	add("Bob plays the electric guitar", "Bob")

	r := New(s, e)
	ref, err := r.Reflect(ctx, "b", domain.RecallRequest{Query: "Alice climbing hobby"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ref.Results) == 0 {
		t.Fatal("reflect returned no results")
	}
	found := false
	for _, ent := range ref.Entities {
		if ent == "Alice" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reflect did not surface entity 'Alice'; entities=%v", ref.Entities)
	}
}

func TestReflectPrioritizesMentalModels(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	e := embed.NewHashEmbedder(128)

	add := func(content, factType string) {
		vecs, err := e.EmbedDocuments(ctx, []string{content})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Retain("b", []store.MemoryInput{{
			Content: content, Embedding: vecs[0], EmbedModel: e.Name(), FactType: factType,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	// a raw fact and an agent-synthesized mental model, both about Alice
	add("Alice fixed a bug in the parser", "experience")
	add("Alice is the team's go-to engineer for the parser subsystem", "mental_model")

	r := New(s, e)
	ref, err := r.Reflect(ctx, "b", domain.RecallRequest{Query: "Alice parser engineer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ref.Results) == 0 {
		t.Fatal("no reflect results")
	}
	// the synthesis (mental_model) must come before the raw fact (hierarchy)
	if ref.Results[0].Type == nil || *ref.Results[0].Type != "mental_model" {
		t.Fatalf("reflect did not prioritize mental_model first; got types %v", typesOf(ref.Results))
	}
}

func typesOf(rs []domain.RecallResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		if r.Type != nil {
			out[i] = *r.Type
		}
	}
	return out
}
