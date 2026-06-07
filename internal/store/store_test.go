package store_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"joyvend.io/internal/embed"
	"joyvend.io/internal/secret"
	"joyvend.io/internal/store"
)

func TestEncryptedRoundTripAndSearch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blob := filepath.Join(dir, "joyvend.db.enc")

	env, dek, err := secret.NewEnvelope([]byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}

	hash := embed.NewHashEmbedder(64)
	const roommate = "Emily is the user's roommate since 2026-05-01"
	docs := []string{roommate, "The stove gets hot when it is turned on"}
	vecs, err := hash.EmbedDocuments(ctx, docs)
	if err != nil {
		t.Fatal(err)
	}

	// --- write side ---
	s, err := store.OpenEncrypted(blob, secret.NewKeyStore(dek), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	items := make([]store.MemoryInput, len(docs))
	for i, d := range docs {
		items[i] = store.MemoryInput{Content: d, Embedding: vecs[i], EmbedModel: "hash", Tags: []string{"user_a"}}
	}
	if n, err := s.Retain("default", items); err != nil || n != 2 {
		t.Fatalf("retain: n=%d err=%v", n, err)
	}
	if err := s.Close(); err != nil { // Close flushes (D19)
		t.Fatal(err)
	}

	// on-disk blob must be ciphertext only
	raw, err := os.ReadFile(blob)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("roommate")) {
		t.Fatal("plaintext 'roommate' found in encrypted blob")
	}

	// wrong passphrase must fail
	if _, err := env.Unwrap([]byte("wrong")); err != secret.ErrWrongPassphrase {
		t.Fatalf("expected ErrWrongPassphrase, got %v", err)
	}

	// --- read side: reopen with a freshly-unwrapped DEK ---
	dek2, err := env.Unwrap([]byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.OpenEncrypted(blob, secret.NewKeyStore(dek2), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	if n, _ := s2.MemoryCount(); n != 2 {
		t.Fatalf("memory count after reopen = %d, want 2", n)
	}

	// keyword search survives encryption round-trip
	kw, err := s2.KeywordSearch("default", "roommate", nil, "any", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(kw) == 0 {
		t.Fatal("keyword search returned nothing for 'roommate'")
	}

	// vector search ranks the roommate memory first for a related query
	qv, _ := hash.EmbedQuery(ctx, "who is my roommate")
	vs, err := s2.VectorSearch("default", "hash", qv, nil, "any", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 {
		t.Fatal("vector search returned nothing")
	}
	results, err := s2.LoadResults([]int64{vs[0].ID})
	if err != nil || len(results) != 1 {
		t.Fatalf("load results: %v", err)
	}
	if results[0].Text != roommate {
		t.Fatalf("top vector hit = %q, want roommate memory", results[0].Text)
	}
	if results[0].ID != strconv.FormatInt(vs[0].ID, 10) {
		t.Fatalf("result id mismatch")
	}
}
