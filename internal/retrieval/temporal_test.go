package retrieval

import (
	"context"
	"testing"
	"time"

	"joyvend.io/internal/domain"
	"joyvend.io/internal/embed"
	"joyvend.io/internal/store"
)

func ymd(y int, m time.Month, d int) int64 {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()
}
func endOf(y int, m time.Month, d int) int64 {
	return time.Date(y, m, d, 23, 59, 59, 0, time.UTC).Unix()
}

func TestExtractTemporalWindow(t *testing.T) {
	now := time.Date(2026, time.June, 15, 12, 0, 0, 0, time.UTC).Unix() // a Monday

	cases := []struct {
		q                string
		ok               bool
		wantStart, wantE int64
	}{
		{"what happened on 2026-05-01", true, ymd(2026, 5, 1), endOf(2026, 5, 1)},
		{"notes from in May 2026 please", true, ymd(2026, 5, 1), endOf(2026, 5, 31)},
		{"what did I do this month", true, ymd(2026, 6, 1), endOf(2026, 6, 30)},
		{"events last month", true, ymd(2026, 5, 1), endOf(2026, 5, 31)},
		{"what about yesterday", true, ymd(2026, 6, 14), endOf(2026, 6, 14)},
		{"3 days ago", true, ymd(2026, 6, 12), endOf(2026, 6, 12)},
		{"stuff from 2025", true, ymd(2025, 1, 1), endOf(2025, 12, 31)},
		{"this year summary", true, ymd(2026, 1, 1), endOf(2026, 12, 31)},
		{"where are my keys", false, 0, 0},
		{"tell me about alice", false, 0, 0},
	}
	for _, c := range cases {
		w, ok := extractTemporalWindow(c.q, now)
		if ok != c.ok {
			t.Errorf("%q: ok=%v want %v", c.q, ok, c.ok)
			continue
		}
		if ok && (w.start != c.wantStart || w.end != c.wantE) {
			t.Errorf("%q: window [%d,%d] want [%d,%d]", c.q, w.start, w.end, c.wantStart, c.wantE)
		}
	}
}

func TestRecallTemporalArm(t *testing.T) {
	ctx := context.Background()
	s := newStore(t) // helper from recall_test.go (same package)
	e := embed.NewHashEmbedder(256)

	docs := []struct {
		content string
		at      int64
	}{
		{"went hiking in the green hills", time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC).Unix()},
		{"bought a brand new laptop", time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC).Unix()},
	}
	for _, d := range docs {
		vecs, err := e.EmbedDocuments(ctx, []string{d.content})
		if err != nil {
			t.Fatal(err)
		}
		at := d.at
		if _, err := s.Retain("b", []store.MemoryInput{{
			Content: d.content, Embedding: vecs[0], EmbedModel: e.Name(), EventAt: &at,
		}}); err != nil {
			t.Fatal(err)
		}
	}

	r := New(s, e)
	// a purely temporal query (no lexical/semantic overlap with the content)
	resp, err := r.Recall(ctx, "b", domain.RecallRequest{Query: "what happened in May 2026"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 || resp.Results[0].Text != "went hiking in the green hills" {
		t.Fatalf("temporal recall top result = %v, want the May memory", resp.Results)
	}
}

func TestWeekWindowMondayStart(t *testing.T) {
	// now = Monday 2026-06-15; "this week" should start that Monday.
	now := time.Date(2026, time.June, 17, 9, 0, 0, 0, time.UTC).Unix() // Wednesday
	w, ok := extractTemporalWindow("what happened this week", now)
	if !ok || w.start != ymd(2026, 6, 15) || w.end != endOf(2026, 6, 21) {
		t.Fatalf("this week => ok=%v [%d,%d], want [%d,%d]", ok, w.start, w.end, ymd(2026, 6, 15), endOf(2026, 6, 21))
	}
}
