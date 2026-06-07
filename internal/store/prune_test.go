package store

import (
	"testing"

	"joyvend.io/internal/domain"
)

func TestPruneOrphansOnDelete(t *testing.T) {
	s := openTestStore(t)
	ent := func(names ...string) []domain.EntityInput {
		var es []domain.EntityInput
		for _, n := range names {
			es = append(es, domain.EntityInput{Text: n})
		}
		return es
	}
	countEntities := func() int {
		var n int
		_ = s.conn.QueryRowContext(s.ctx, `SELECT COUNT(*) FROM entity`).Scan(&n)
		return n
	}

	// id 1 references "Zoe" only; id 2 references "Alice" + "Bob"
	if _, err := s.Retain("b", []MemoryInput{{Content: "only mention of Zoe", Entities: ent("Zoe")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retain("b", []MemoryInput{{Content: "Alice and Bob met", Entities: ent("Alice", "Bob")}}); err != nil {
		t.Fatal(err)
	}
	if countEntities() != 3 {
		t.Fatalf("entities = %d, want 3", countEntities())
	}

	// deleting memory 1 orphans "Zoe" -> auto-pruned
	if _, err := s.DeleteMemory("b", 1); err != nil {
		t.Fatal(err)
	}
	if countEntities() != 2 {
		t.Fatalf("after delete, entities = %d, want 2 (Zoe pruned)", countEntities())
	}

	// nothing left to prune
	if n, err := s.PruneOrphans(); err != nil || n != 0 {
		t.Fatalf("PruneOrphans = %d, %v; want 0/nil", n, err)
	}
}
