// Package ingest internal tests: chunk() and parseTimestamp() are unexported, so
// these live in package ingest (not ingest_test). The end-to-end Retain test
// exercises the full chunk -> embed -> store path against a real encrypted store.
package ingest

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"joyvend.io/internal/domain"
	"joyvend.io/internal/embed"
	"joyvend.io/internal/secret"
	"joyvend.io/internal/store"
)

// strPtr is a small helper for the *string fields in MemoryItem/parseTimestamp.
func strPtr(s string) *string { return &s }

// makeWords builds a whitespace-separated string of n words, each `wordLen`
// runes long, so the total length comfortably exceeds maxChunkChars and there
// are natural break points for chunk() to cut on.
func makeWords(n, wordLen int) string {
	word := strings.Repeat("a", wordLen)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = word
	}
	return strings.Join(parts, " ")
}

func TestChunkShortStringSingleChunk(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"tiny", "hello world"},
		{"exactly_max", strings.Repeat("x", maxChunkChars)},
		{"trims_to_short", "   padded around   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := chunk(tc.in)
			if len(got) != 1 {
				t.Fatalf("chunk(%q) yielded %d chunks, want 1", tc.name, len(got))
			}
			if want := strings.TrimSpace(tc.in); got[0] != want {
				t.Fatalf("chunk single = %q, want %q", got[0], want)
			}
		})
	}
}

func TestChunkSplitsLargeStringNoOverlap(t *testing.T) {
	// ~7000 chars of space-separated 10-char words -> several chunks.
	const wordLen = 10
	text := makeWords(640, wordLen) // 640*10 + 639 spaces = 7039 chars
	if len(text) <= maxChunkChars {
		t.Fatalf("test setup: input length %d is not > maxChunkChars %d", len(text), maxChunkChars)
	}

	chunks := chunk(text)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for a %d-char input, got %d", len(text), len(chunks))
	}

	// Every chunk must respect the size cap and be trimmed (no leading/trailing
	// whitespace, no empty chunks).
	for i, c := range chunks {
		if len(c) > maxChunkChars {
			t.Errorf("chunk %d has length %d, exceeds maxChunkChars %d", i, len(c), maxChunkChars)
		}
		if c == "" {
			t.Errorf("chunk %d is empty", i)
		}
		if c != strings.TrimSpace(c) {
			t.Errorf("chunk %d is not whitespace-trimmed: %q...", i, c[:min(len(c), 20)])
		}
	}

	// No-overlap + lossless-on-words: re-tokenizing the joined chunks must yield
	// exactly the same word sequence as the original. (chunk breaks only at
	// whitespace for this input, so no word is split across a boundary.)
	wantWords := strings.Fields(text)
	gotWords := strings.Fields(strings.Join(chunks, " "))
	if len(gotWords) != len(wantWords) {
		t.Fatalf("reassembled word count = %d, want %d (overlap or loss)", len(gotWords), len(wantWords))
	}
	for i := range wantWords {
		if gotWords[i] != wantWords[i] {
			t.Fatalf("word %d mismatch after reassembly: got %q want %q", i, gotWords[i], wantWords[i])
		}
	}
}

// TestChunkBreaksOnWhitespace verifies the "break at whitespace when possible"
// behavior: with breakable input no word is ever cut in half.
func TestChunkBreaksOnWhitespace(t *testing.T) {
	text := makeWords(800, 5)
	chunks := chunk(text)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	word := strings.Repeat("a", 5)
	for i, c := range chunks {
		for _, w := range strings.Fields(c) {
			if w != word {
				t.Fatalf("chunk %d contains a split/partial word %q, want full %q", i, w, word)
			}
		}
	}
}

// TestChunkUnbreakableFallsBackToHardCut covers the branch where there is no
// usable whitespace in the back half of the window: chunk must still make
// progress and respect the cap (hard cut at maxChunkChars).
func TestChunkUnbreakableFallsBackToHardCut(t *testing.T) {
	text := strings.Repeat("z", maxChunkChars*2+250) // no whitespace at all
	chunks := chunk(text)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for unbreakable input, got %d", len(chunks))
	}
	total := 0
	for i, c := range chunks {
		if len(c) > maxChunkChars {
			t.Errorf("chunk %d length %d exceeds cap %d", i, len(c), maxChunkChars)
		}
		total += len(c)
	}
	// No whitespace means nothing is dropped by TrimSpace, so lengths must sum
	// exactly to the original (proves no overlap and no loss on the hard-cut path).
	if total != len(text) {
		t.Fatalf("hard-cut chunks total %d chars, want %d", total, len(text))
	}
	if strings.Join(chunks, "") != text {
		t.Fatalf("hard-cut reassembly does not match original")
	}
}

func TestParseTimestamp(t *testing.T) {
	t.Run("nil_is_now", func(t *testing.T) {
		before := time.Now().Unix()
		got := parseTimestamp(nil)
		after := time.Now().Unix()
		if got == nil {
			t.Fatal("parseTimestamp(nil) = nil, want ~now")
		}
		if *got < before || *got > after {
			t.Fatalf("parseTimestamp(nil) = %d, want in [%d,%d]", *got, before, after)
		}
	})

	t.Run("unset_is_nil", func(t *testing.T) {
		if got := parseTimestamp(strPtr("unset")); got != nil {
			t.Fatalf(`parseTimestamp("unset") = %v, want nil`, *got)
		}
	})

	t.Run("iso_layouts", func(t *testing.T) {
		cases := []struct {
			name string
			in   string
			want int64
		}{
			{"rfc3339", "2026-05-01T12:00:00Z", mustUnix(t, time.RFC3339, "2026-05-01T12:00:00Z")},
			{"rfc3339_offset", "2026-05-01T12:00:00+02:00", mustUnix(t, time.RFC3339, "2026-05-01T12:00:00+02:00")},
			{"date_only", "2026-05-01", mustUnix(t, "2006-01-02", "2026-05-01")},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := parseTimestamp(strPtr(tc.in))
				if got == nil {
					t.Fatalf("parseTimestamp(%q) = nil, want %d", tc.in, tc.want)
				}
				if *got != tc.want {
					t.Fatalf("parseTimestamp(%q) = %d, want %d", tc.in, *got, tc.want)
				}
			})
		}
	})

	t.Run("garbage_falls_back_to_now", func(t *testing.T) {
		before := time.Now().Unix()
		got := parseTimestamp(strPtr("not-a-timestamp"))
		after := time.Now().Unix()
		if got == nil {
			t.Fatal("parseTimestamp(garbage) = nil, want fallback to now")
		}
		if *got < before || *got > after {
			t.Fatalf("parseTimestamp(garbage) = %d, want in [%d,%d]", *got, before, after)
		}
	})
}

func mustUnix(t *testing.T, layout, value string) int64 {
	t.Helper()
	tm, err := time.Parse(layout, value)
	if err != nil {
		t.Fatalf("test setup: parse %q with %q: %v", value, layout, err)
	}
	return tm.Unix()
}

// newTestStore builds a real encrypted store in a temp dir, keyed by a random
// 32-byte DEK held in a secret.KeyStore (no passphrase/argon2 path needed for
// the ingest test — the DEK is what seals the DB).
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("rand dek: %v", err)
	}
	blob := filepath.Join(t.TempDir(), "joyvend.db.enc")
	s, err := store.OpenEncrypted(blob, secret.NewKeyStore(dek), store.Options{})
	if err != nil {
		t.Fatalf("OpenEncrypted: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestRetainEndToEnd(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	emb := embed.NewHashEmbedder(64)
	in := New(s, emb, 0) // softCap disabled

	ctxStr := "kitchen"
	req := domain.RetainRequest{
		Items: []domain.MemoryItem{
			{
				Content:   "Emily is the user's roommate since 2026-05-01",
				Timestamp: strPtr("2026-05-01"),
				Context:   &ctxStr,
				Tags:      []string{"user_a"},
			},
			{
				Content:  "The stove gets hot when it is turned on",
				Metadata: map[string]string{"source": "manual"},
			},
		},
	}

	resp, err := in.Retain(ctx, "default", req)
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Retain resp.Success = false, want true")
	}
	if resp.BankID != "default" {
		t.Fatalf("resp.BankID = %q, want %q", resp.BankID, "default")
	}
	// Both items are short -> exactly one chunk each -> 2 stored units.
	if resp.ItemsCount != 2 {
		t.Fatalf("resp.ItemsCount = %d, want 2", resp.ItemsCount)
	}
	if resp.Warning != "" {
		t.Fatalf("unexpected soft-cap warning with softCap disabled: %q", resp.Warning)
	}

	n, err := s.MemoryCount()
	if err != nil {
		t.Fatalf("MemoryCount: %v", err)
	}
	if n != 2 {
		t.Fatalf("store.MemoryCount() = %d, want 2", n)
	}
}

// TestRetainChunksLongItem proves a single oversized item fans out into multiple
// stored memories (ItemsCount and MemoryCount reflect chunk count, not item count).
func TestRetainChunksLongItem(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	in := New(s, embed.NewHashEmbedder(64), 0)

	long := makeWords(640, 10) // ~7039 chars -> multiple chunks
	wantChunks := len(chunk(long))
	if wantChunks < 2 {
		t.Fatalf("test setup: expected long content to chunk, got %d", wantChunks)
	}

	resp, err := in.Retain(ctx, "default", domain.RetainRequest{
		Items: []domain.MemoryItem{{Content: long}},
	})
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if resp.ItemsCount != wantChunks {
		t.Fatalf("ItemsCount = %d, want %d (chunk count)", resp.ItemsCount, wantChunks)
	}
	if n, _ := s.MemoryCount(); n != wantChunks {
		t.Fatalf("MemoryCount = %d, want %d", n, wantChunks)
	}
}

func TestRetainValidation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	in := New(s, embed.NewHashEmbedder(64), 0)

	t.Run("no_items", func(t *testing.T) {
		if _, err := in.Retain(ctx, "default", domain.RetainRequest{}); err == nil {
			t.Fatal("Retain with no items: want error, got nil")
		}
	})

	t.Run("blank_content", func(t *testing.T) {
		req := domain.RetainRequest{Items: []domain.MemoryItem{{Content: "   \n\t "}}}
		if _, err := in.Retain(ctx, "default", req); err == nil {
			t.Fatal("Retain with blank content: want error, got nil")
		}
		// A rejected request must not have written anything.
		if n, _ := s.MemoryCount(); n != 0 {
			t.Fatalf("MemoryCount = %d after rejected retain, want 0", n)
		}
	})
}

// TestRetainNilEmbedderStillStores: the embedder is optional (PLAN — embeddings
// are nil when no embedder). Retain must still persist the units.
func TestRetainNilEmbedderStillStores(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	in := New(s, nil, 0)

	resp, err := in.Retain(ctx, "default", domain.RetainRequest{
		Items: []domain.MemoryItem{{Content: "a memory without embeddings"}},
	})
	if err != nil {
		t.Fatalf("Retain (nil embedder): %v", err)
	}
	if resp.ItemsCount != 1 {
		t.Fatalf("ItemsCount = %d, want 1", resp.ItemsCount)
	}
	if n, _ := s.MemoryCount(); n != 1 {
		t.Fatalf("MemoryCount = %d, want 1", n)
	}
}
