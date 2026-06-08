package store_test

import (
	"crypto/rand"
	"path/filepath"
	"testing"

	"mykeep.ai/internal/secret"
	"mykeep.ai/internal/store"
)

func TestSingleInstanceLock(t *testing.T) {
	blob := filepath.Join(t.TempDir(), "mykeep.db.enc")
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}
	// a fresh keystore over the SAME dek for each open (the store never zeroes it).
	keys := func() *secret.KeyStore { return secret.NewKeyStore(append([]byte(nil), dek...)) }

	s1, err := store.OpenEncrypted(blob, keys(), store.Options{})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}

	// a second instance on the same drive must be refused
	if _, err := store.OpenEncrypted(blob, keys(), store.Options{}); err != store.ErrAlreadyRunning {
		t.Fatalf("second open: got %v, want ErrAlreadyRunning", err)
	}

	// after the first closes (releasing the lock + flushing), a new instance opens
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := store.OpenEncrypted(blob, keys(), store.Options{})
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	_ = s2.Close()
}
