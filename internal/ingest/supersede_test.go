package ingest

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"testing"

	"joyvend.io/internal/domain"
	"joyvend.io/internal/embed"
	"joyvend.io/internal/secret"
	"joyvend.io/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}
	s, err := store.OpenEncrypted(filepath.Join(t.TempDir(), "db.enc"), secret.NewKeyStore(dek), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSupersedeReplacesMentalModel(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	in := New(s, embed.NewHashEmbedder(64), 0)

	// v1 of a mental model
	if _, err := in.Retain(ctx, "b", domain.RetainRequest{Items: []domain.MemoryItem{
		{Content: "v1: Alice prefers email", Type: ptr("mental_model")},
	}}); err != nil {
		t.Fatal(err)
	}
	list, total, err := s.ListMemories("b", 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("after v1: total=%d err=%v", total, err)
	}
	v1ID := list[0].ID

	// v2 supersedes v1
	if _, err := in.Retain(ctx, "b", domain.RetainRequest{Items: []domain.MemoryItem{
		{Content: "v2: Alice prefers Slack now", Type: ptr("mental_model"), Supersedes: []string{v1ID}},
	}}); err != nil {
		t.Fatal(err)
	}

	list2, total2, err := s.ListMemories("b", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total2 != 1 || list2[0].Text != "v2: Alice prefers Slack now" {
		t.Fatalf("after supersede: total=%d, items=%v; want only v2", total2, textsOf(list2))
	}
}

func textsOf(rs []domain.RecallResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Text
	}
	return out
}
