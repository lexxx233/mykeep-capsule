package store_test

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"mykeep.ai/internal/secret"
	"mykeep.ai/internal/store"
)

// TestResealConcurrentPersistsAll hammers the store with concurrent writes while
// flushes run, then reopens the blob and checks every write survived. It exercises
// the off-lock single-flight reseal (snapshot under s.mu, seal+write off it) — under
// `-race` it also proves writeGen/persistedGen/dirty are race-free.
func TestResealConcurrentPersistsAll(t *testing.T) {
	blob := filepath.Join(t.TempDir(), "db.enc")
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}
	keys := func() *secret.KeyStore { return secret.NewKeyStore(append([]byte(nil), dek...)) }

	s, err := store.OpenEncrypted(blob, keys(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}

	const workers, per = 8, 25 // 200 writes
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if _, err := s.Retain("b", []store.MemoryInput{{
					Content:  fmt.Sprintf("memory %d-%d", w, i),
					FactType: "experience",
				}}); err != nil {
					t.Errorf("retain: %v", err)
					return
				}
			}
		}(w)
	}
	// concurrent flushes racing the writers
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			if err := s.Flush(); err != nil {
				t.Errorf("flush: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	if err := s.Close(); err != nil { // final synchronous flush
		t.Fatal(err)
	}

	// Reopen: every write must be on disk (no loss from off-lock/coalesced reseals).
	s2, err := store.OpenEncrypted(blob, keys(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	n, err := s2.MemoryCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != workers*per {
		t.Fatalf("after reopen MemoryCount=%d, want %d — concurrent reseal lost or corrupted data", n, workers*per)
	}
}
