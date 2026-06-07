package store

import (
	"testing"

	"joyvend.io/internal/domain"
)

func TestRelatedByEntities(t *testing.T) {
	s := openTestStore(t)
	ent := func(name string) []domain.EntityInput { return []domain.EntityInput{{Text: name}} }

	// id 1 & 2 share entity "Alice"; id 3 has "Bob"
	if _, err := s.Retain("b", []MemoryInput{{Content: "Alice climbs", Entities: ent("Alice")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retain("b", []MemoryInput{{Content: "a gym downtown", Entities: ent("Alice")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retain("b", []MemoryInput{{Content: "Bob sings", Entities: ent("Bob")}}); err != nil {
		t.Fatal(err)
	}

	related, err := s.RelatedByEntities("b", []int64{1}, nil, "any", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 1 || related[0] != 2 {
		t.Fatalf("RelatedByEntities(seed=1) = %v, want [2] (shares 'Alice', excludes seed and 'Bob')", related)
	}
}

// expansion must not cross a tag-isolation boundary (e.g. per-user scoping).
func TestRelatedByEntitiesRespectsTags(t *testing.T) {
	s := openTestStore(t)
	ent := func(n string) []domain.EntityInput { return []domain.EntityInput{{Text: n}} }

	// id 1 (seed, tag user_a), id 2 (tag user_a, shares Alice), id 3 (tag user_b, shares Alice)
	if _, err := s.Retain("b", []MemoryInput{{Content: "Alice climbs", Entities: ent("Alice"), Tags: []string{"user_a"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retain("b", []MemoryInput{{Content: "Alice's gym", Entities: ent("Alice"), Tags: []string{"user_a"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retain("b", []MemoryInput{{Content: "other Alice's salary", Entities: ent("Alice"), Tags: []string{"user_b"}}}); err != nil {
		t.Fatal(err)
	}

	// scoped to user_a: must return id 2, NOT id 3 (different tag)
	related, err := s.RelatedByEntities("b", []int64{1}, []string{"user_a"}, "any", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 1 || related[0] != 2 {
		t.Fatalf("tag-scoped expansion = %v, want [2] (must exclude user_b's id 3)", related)
	}
}
