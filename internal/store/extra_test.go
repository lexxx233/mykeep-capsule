package store_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"

	"joyvend.io/internal/domain"
	"joyvend.io/internal/embed"
	"joyvend.io/internal/secret"
	"joyvend.io/internal/store"
	"joyvend.io/internal/vector"
)

// newStore builds a fresh encrypted store backed by a random 32-byte DEK keystore,
// as specified by the task. It returns the store and the embedder used to compute
// vectors so callers can embed queries with the same model.
func newStore(t *testing.T) (*store.Store, *embed.HashEmbedder) {
	t.Helper()
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("rand dek: %v", err)
	}
	blob := filepath.Join(t.TempDir(), "joyvend.db.enc")
	s, err := store.OpenEncrypted(blob, secret.NewKeyStore(dek), store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, embed.NewHashEmbedder(64)
}

// retainOne is a small helper that retains a single memory with the given content,
// tags and an event timestamp (used to make ListMemories ordering deterministic).
func retainOne(t *testing.T, s *store.Store, bank, content string, eventAt int64, tags ...string) {
	t.Helper()
	h := embed.NewHashEmbedder(64)
	vec, err := h.EmbedQuery(context.Background(), content)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	ea := eventAt
	in := store.MemoryInput{
		Content:    content,
		Embedding:  vec,
		EmbedModel: h.Name(),
		Tags:       tags,
		EventAt:    &ea,
	}
	if n, err := s.Retain(bank, []store.MemoryInput{in}); err != nil || n != 1 {
		t.Fatalf("retain %q: n=%d err=%v", content, n, err)
	}
}

// TestListMemoriesPagination retains 5 memories and walks them with a page size of 2.
func TestListMemoriesPagination(t *testing.T) {
	s, _ := newStore(t)
	const bank = "paginate"

	// Retain 5 memories with strictly increasing event_at so newest-first ordering
	// (ORDER BY event_at DESC) is fully deterministic.
	contents := []string{"alpha one", "bravo two", "charlie three", "delta four", "echo five"}
	for i, c := range contents {
		retainOne(t, s, bank, c, int64(1000+i))
	}

	// Expected newest-first order: echo, delta, charlie, bravo, alpha.
	wantOrder := []string{"echo five", "delta four", "charlie three", "bravo two", "alpha one"}

	type page struct {
		name        string
		limit       int
		offset      int
		wantTexts   []string
		wantTotalEq int
	}
	pages := []page{
		{name: "first page", limit: 2, offset: 0, wantTexts: wantOrder[0:2], wantTotalEq: 5},
		{name: "second page", limit: 2, offset: 2, wantTexts: wantOrder[2:4], wantTotalEq: 5},
		{name: "last partial page", limit: 2, offset: 4, wantTexts: wantOrder[4:5], wantTotalEq: 5},
		{name: "offset past end", limit: 2, offset: 10, wantTexts: nil, wantTotalEq: 5},
	}

	for _, p := range pages {
		t.Run(p.name, func(t *testing.T) {
			got, total, err := s.ListMemories(bank, p.limit, p.offset)
			if err != nil {
				t.Fatalf("ListMemories: %v", err)
			}
			if total != p.wantTotalEq {
				t.Fatalf("total = %d, want %d", total, p.wantTotalEq)
			}
			if len(got) != len(p.wantTexts) {
				t.Fatalf("page len = %d, want %d (texts=%v)", len(got), len(p.wantTexts), texts(got))
			}
			for i := range got {
				if got[i].Text != p.wantTexts[i] {
					t.Fatalf("page[%d].Text = %q, want %q", i, got[i].Text, p.wantTexts[i])
				}
			}
		})
	}

	// Sanity: the union of the two main pages equals the whole set, no duplicates.
	page0, _, _ := s.ListMemories(bank, 2, 0)
	page2, _, _ := s.ListMemories(bank, 2, 2)
	seen := map[string]bool{}
	for _, r := range append(texts(page0), texts(page2)...) {
		if seen[r] {
			t.Fatalf("duplicate %q across pages", r)
		}
		seen[r] = true
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 distinct rows across the two pages, got %d", len(seen))
	}
}

// TestListMemoriesDefaultLimit verifies the limit<=0 path clamps to the default and
// returns everything when there are fewer rows than the default.
func TestListMemoriesDefaultLimit(t *testing.T) {
	s, _ := newStore(t)
	const bank = "defaults"
	for i := 0; i < 3; i++ {
		retainOne(t, s, bank, fmt.Sprintf("note number %d", i), int64(i))
	}
	got, total, err := s.ListMemories(bank, 0, 0) // limit<=0 -> default 100
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if total != 3 || len(got) != 3 {
		t.Fatalf("default-limit list: len=%d total=%d, want 3/3", len(got), total)
	}
}

// TestDeleteMemoryCascades verifies a delete drops the row, lowers MemoryCount, and
// removes it from keyword search (the FTS delete trigger fires on cascade).
func TestDeleteMemoryCascades(t *testing.T) {
	s, _ := newStore(t)
	const bank = "deletes"

	retainOne(t, s, bank, "the unique zarquon keyword lives here", 1, "topic")
	retainOne(t, s, bank, "an unrelated banana memory", 2, "topic")

	before, err := s.MemoryCount()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if before != 2 {
		t.Fatalf("count before delete = %d, want 2", before)
	}

	// Find the target row's id via list (RecallResult.ID is the decimal rowid).
	target := findID(t, s, bank, "the unique zarquon keyword lives here")

	// Pre-condition: keyword search finds it.
	if hits, err := s.KeywordSearch(bank, "zarquon", nil, "any", 10); err != nil || len(hits) == 0 {
		t.Fatalf("pre-delete keyword search: hits=%d err=%v", len(hits), err)
	}

	ok, err := s.DeleteMemory(bank, target)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !ok {
		t.Fatalf("delete reported no row removed")
	}

	after, err := s.MemoryCount()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if after != before-1 {
		t.Fatalf("count after delete = %d, want %d", after, before-1)
	}

	// The FTS cascade must have removed the deleted row from keyword results.
	hits, err := s.KeywordSearch(bank, "zarquon", nil, "any", 10)
	if err != nil {
		t.Fatalf("post-delete keyword search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("post-delete keyword search returned %d hits, want 0", len(hits))
	}

	// The surviving memory is still searchable.
	if hits, err := s.KeywordSearch(bank, "banana", nil, "any", 10); err != nil || len(hits) != 1 {
		t.Fatalf("survivor keyword search: hits=%d err=%v", len(hits), err)
	}

	// Deleting a non-existent / wrong-bank id is a no-op (false, nil).
	if ok, err := s.DeleteMemory(bank, target); err != nil || ok {
		t.Fatalf("re-delete: ok=%v err=%v, want false/nil", ok, err)
	}
	if ok, err := s.DeleteMemory("other-bank", findID(t, s, bank, "an unrelated banana memory")); err != nil || ok {
		t.Fatalf("cross-bank delete: ok=%v err=%v, want false/nil", ok, err)
	}
}

// TestTagsMatch covers the tag-filter semantics for both KeywordSearch and
// VectorSearch with "all"/"all_strict" (every tag) vs "any"/other (at least one).
func TestTagsMatch(t *testing.T) {
	s, h := newStore(t)
	const bank = "tags"

	// Three memories sharing one common search term "report" so the FTS MATCH and
	// the vector scan both return all three before tag filtering. Tags differ:
	//   m1: {red}        m2: {red, blue}        m3: {blue}
	retainOne(t, s, bank, "quarterly report from finance", 1, "red")
	retainOne(t, s, bank, "quarterly report from sales", 2, "red", "blue")
	retainOne(t, s, bank, "quarterly report from ops", 3, "blue")

	id := func(content string) string { return strconv.FormatInt(findID(t, s, bank, content), 10) }
	idRed := id("quarterly report from finance")
	idBoth := id("quarterly report from sales")
	idBlue := id("quarterly report from ops")

	qvec, err := h.EmbedQuery(context.Background(), "quarterly report")
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}

	type tc struct {
		name      string
		tags      []string
		tagsMatch string
		wantIDs   []string // sorted
	}
	cases := []tc{
		{name: "single tag any -> red+both", tags: []string{"red"}, tagsMatch: "any", wantIDs: sortedStrings(idRed, idBoth)},
		{name: "single tag all -> red+both", tags: []string{"red"}, tagsMatch: "all", wantIDs: sortedStrings(idRed, idBoth)},
		{name: "two tags any -> all three", tags: []string{"red", "blue"}, tagsMatch: "any", wantIDs: sortedStrings(idRed, idBoth, idBlue)},
		{name: "two tags all -> only both", tags: []string{"red", "blue"}, tagsMatch: "all", wantIDs: sortedStrings(idBoth)},
		{name: "two tags all_strict -> only both", tags: []string{"red", "blue"}, tagsMatch: "all_strict", wantIDs: sortedStrings(idBoth)},
		{name: "no tags -> all three", tags: nil, tagsMatch: "any", wantIDs: sortedStrings(idRed, idBoth, idBlue)},
		{name: "unknown tag any -> none", tags: []string{"green"}, tagsMatch: "any", wantIDs: nil},
		{name: "duplicate tags all collapses -> red+both", tags: []string{"red", "red"}, tagsMatch: "all", wantIDs: sortedStrings(idRed, idBoth)},
	}

	for _, c := range cases {
		t.Run("keyword/"+c.name, func(t *testing.T) {
			hits, err := s.KeywordSearch(bank, "report", c.tags, c.tagsMatch, 50)
			if err != nil {
				t.Fatalf("KeywordSearch: %v", err)
			}
			if got := scoredIDs(hits); !equalStringSets(got, c.wantIDs) {
				t.Fatalf("keyword ids = %v, want %v", got, c.wantIDs)
			}
		})
		t.Run("vector/"+c.name, func(t *testing.T) {
			hits, err := s.VectorSearch(bank, h.Name(), qvec, c.tags, c.tagsMatch, 50)
			if err != nil {
				t.Fatalf("VectorSearch: %v", err)
			}
			if got := scoredIDs(hits); !equalStringSets(got, c.wantIDs) {
				t.Fatalf("vector ids = %v, want %v", got, c.wantIDs)
			}
		})
	}
}

// TestConcurrentRetainAndSearch hammers a single store from many goroutines with
// interleaved Retain and KeywordSearch calls. It asserts no error surfaces and that
// the final memory count equals the number of successful retains.
func TestConcurrentRetainAndSearch(t *testing.T) {
	s, _ := newStore(t)
	const bank = "concurrent"
	const (
		workers       = 16
		perWorker     = 25
		totalExpected = workers * perWorker
	)

	h := embed.NewHashEmbedder(64)
	var wg sync.WaitGroup
	errCh := make(chan error, workers*perWorker*2)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				content := fmt.Sprintf("worker %d item %d shared token", w, i)
				vec, err := h.EmbedQuery(context.Background(), content)
				if err != nil {
					errCh <- err
					return
				}
				if n, err := s.Retain(bank, []store.MemoryInput{{
					Content:    content,
					Embedding:  vec,
					EmbedModel: h.Name(),
					Tags:       []string{fmt.Sprintf("w%d", w)},
				}}); err != nil {
					errCh <- err
					return
				} else if n != 1 {
					errCh <- fmt.Errorf("retain n=%d, want 1", n)
					return
				}
				// Interleave a read against the same store.
				if _, err := s.KeywordSearch(bank, "shared token", nil, "any", 10); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent op error: %v", err)
		}
	}

	count, err := s.MemoryCount()
	if err != nil {
		t.Fatalf("final count: %v", err)
	}
	if count != totalExpected {
		t.Fatalf("final memory count = %d, want %d", count, totalExpected)
	}

	// Every memory is also findable by the shared token (limit covers all).
	hits, err := s.KeywordSearch(bank, "shared token", nil, "any", totalExpected+10)
	if err != nil {
		t.Fatalf("final keyword search: %v", err)
	}
	if len(hits) != totalExpected {
		t.Fatalf("final keyword hits = %d, want %d", len(hits), totalExpected)
	}
}

// ---- local helpers ----

func texts(rs []domain.RecallResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Text
	}
	return out
}

func findID(t *testing.T, s *store.Store, bank, content string) int64 {
	t.Helper()
	all, _, err := s.ListMemories(bank, 1000, 0)
	if err != nil {
		t.Fatalf("ListMemories(find): %v", err)
	}
	for _, r := range all {
		if r.Text == content {
			id, err := strconv.ParseInt(r.ID, 10, 64)
			if err != nil {
				t.Fatalf("parse id %q: %v", r.ID, err)
			}
			return id
		}
	}
	t.Fatalf("memory %q not found in bank %q", content, bank)
	return 0
}

func scoredIDs(hits []vector.Scored) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, strconv.FormatInt(h.ID, 10))
	}
	sort.Strings(out)
	return out
}

func sortedStrings(in ...string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
