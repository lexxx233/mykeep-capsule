package ingest

import (
	"context"
	"strings"
	"testing"

	"mykeep.ai/internal/domain"
	"mykeep.ai/internal/embed"
)

func TestCaptureStoresExperienceTaggedCapture(t *testing.T) {
	ctx := context.Background()
	s := newStore(t) // helper from supersede_test.go
	in := New(s, embed.NewHashEmbedder(64), 0)

	resp, err := in.Capture(ctx, "b", domain.CaptureRequest{Text: "the user prefers dark mode in the editor"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Stored {
		t.Fatalf("expected stored, got %+v", resp)
	}
	// stored as fact_type=experience and tagged `capture`
	items, total, err := s.ListMemoriesFiltered("b", 10, 0, "experience", domain.CaptureTag)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("ListMemoriesFiltered(experience,capture) total=%d items=%d, want 1/1", total, len(items))
	}
}

func TestCaptureGates(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	in := New(s, embed.NewHashEmbedder(64), 0)

	cases := []struct {
		text    string
		skipped string
	}{
		{"ok", "too_short"},                 // < 8 runes
		{"   !!! ??? ... ---  ", "trivial"}, // >= 8 runes, 0 alphanumeric
	}
	for _, c := range cases {
		resp, err := in.Capture(ctx, "b", domain.CaptureRequest{Text: c.text})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Stored || resp.Skipped != c.skipped {
			t.Errorf("Capture(%q) = %+v, want skipped=%q", c.text, resp, c.skipped)
		}
	}
	if n, _ := s.MemoryCount(); n != 0 {
		t.Fatalf("gated captures should store nothing; MemoryCount=%d", n)
	}
}

func TestCaptureDedup(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	in := New(s, embed.NewHashEmbedder(64), 0)

	first, _ := in.Capture(ctx, "b", domain.CaptureRequest{Text: "a unique sentence about orange cats"})
	if !first.Stored {
		t.Fatal("first capture should store")
	}
	dup, _ := in.Capture(ctx, "b", domain.CaptureRequest{Text: "a unique sentence about orange cats"})
	if dup.Stored || dup.Skipped != "duplicate" {
		t.Fatalf("exact repeat = %+v, want skipped=duplicate", dup)
	}
	fresh, _ := in.Capture(ctx, "b", domain.CaptureRequest{Text: "an entirely different thought regarding bicycles"})
	if !fresh.Stored {
		t.Fatal("distinct content should store")
	}
	if n, _ := s.MemoryCount(); n != 2 {
		t.Fatalf("MemoryCount=%d, want 2 (dup suppressed)", n)
	}
}

func TestCaptureRolePrefixAndTruncate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	in := New(s, embed.NewHashEmbedder(64), 0)

	if _, err := in.Capture(ctx, "b", domain.CaptureRequest{Role: "user", Text: "remember the budget meeting"}); err != nil {
		t.Fatal(err)
	}
	items, _, _ := s.ListMemoriesFiltered("b", 10, 0, "", domain.CaptureTag)
	if len(items) != 1 || !strings.HasPrefix(items[0].Text, "user: ") {
		t.Fatalf("role not prefixed: %v", items)
	}

	// over-length input truncates to ONE row (not chunked into many)
	long := strings.Repeat("alpha ", 1000) // ~6000 chars
	if _, err := in.Capture(ctx, "b", domain.CaptureRequest{Text: long}); err != nil {
		t.Fatal(err)
	}
	_, total, _ := s.ListMemoriesFiltered("b", 10, 0, "", domain.CaptureTag)
	if total != 2 {
		t.Fatalf("over-length capture should be 1 row; capture total=%d, want 2", total)
	}
}
