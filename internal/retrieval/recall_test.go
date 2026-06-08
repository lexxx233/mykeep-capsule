// Package retrieval white-box tests: reciprocalRankFusion and clamp are
// unexported, so these live in package retrieval (not retrieval_test). The
// end-to-end Recall tests drive a real encrypted in-RAM *store.Store plus the
// deterministic HashEmbedder so no model download or network is required.
package retrieval

import (
	"context"
	"crypto/rand"
	"math"
	"path/filepath"
	"strconv"
	"testing"

	"mykeep.ai/internal/domain"
	"mykeep.ai/internal/embed"
	"mykeep.ai/internal/secret"
	"mykeep.ai/internal/store"
	"mykeep.ai/internal/vector"
)

const rrfEps = 1e-9

// --- GOLDEN reciprocal rank fusion --------------------------------------------

// rrf is the by-hand reference for 1/(k+rank) with k=60 and rank 0-indexed,
// matching reciprocalRankFusion's score(d) += 1/(rrfK+rank+1).
func rrf(rank int) float64 { return 1.0 / float64(rrfK+rank+1) }

func TestReciprocalRankFusionGolden(t *testing.T) {
	tests := []struct {
		name string
		arms [][]vector.Scored
		// wantOrder is the exact fused id order (score desc, id asc on tie).
		wantOrder []int64
		// wantScore is the hand-computed fused score per id.
		wantScore map[int64]float64
	}{
		{
			// Two arms; doc 1 leads arm A and trails arm B (and vice-versa for
			// doc 2), so 1 and 2 tie on score and split by ascending id. Docs 3
			// and 4 each appear in only ONE arm (at rank 2), so both land below
			// the docs present in both arms; they tie at 1/63 and split by id.
			name: "two arms, single-arm docs rank below shared docs",
			arms: [][]vector.Scored{
				{{ID: 1, Sim: 0.9}, {ID: 2, Sim: 0.8}, {ID: 3, Sim: 0.7}},   // arm A
				{{ID: 2, Sim: 0.95}, {ID: 1, Sim: 0.85}, {ID: 4, Sim: 0.6}}, // arm B
			},
			wantOrder: []int64{1, 2, 3, 4},
			wantScore: map[int64]float64{
				1: rrf(0) + rrf(1), // A rank0 + B rank1
				2: rrf(1) + rrf(0), // A rank1 + B rank0
				3: rrf(2),          // A only
				4: rrf(2),          // B only
			},
		},
		{
			// Asymmetric arms exercise strict score ordering (not just ties):
			// doc 1 is rank0 in BOTH arms (2/61, clear winner); docs 2 and 4 tie
			// at 1/62 and split by id; doc 3 appears only in arm A at rank2
			// (1/63) and is ranked LAST — below every doc that is in both arms.
			name: "asymmetric arms, strict winner then tie then single-arm tail",
			arms: [][]vector.Scored{
				{{ID: 1, Sim: 0.99}, {ID: 2, Sim: 0.5}, {ID: 3, Sim: 0.4}}, // arm A
				{{ID: 1, Sim: 0.97}, {ID: 4, Sim: 0.6}},                    // arm B
			},
			wantOrder: []int64{1, 2, 4, 3},
			wantScore: map[int64]float64{
				1: rrf(0) + rrf(0), // both arms rank0
				2: rrf(1),          // A rank1
				4: rrf(1),          // B rank1
				3: rrf(2),          // A rank2 — single-arm tail
			},
		},
		{
			name:      "no arms yields empty fusion",
			arms:      nil,
			wantOrder: []int64{},
			wantScore: map[int64]float64{},
		},
		{
			name: "empty arms contribute nothing",
			arms: [][]vector.Scored{
				{},
				{{ID: 7, Sim: 0.1}},
			},
			wantOrder: []int64{7},
			wantScore: map[int64]float64{7: rrf(0)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reciprocalRankFusion(tt.arms)

			if len(got) != len(tt.wantOrder) {
				t.Fatalf("fused length = %d, want %d (%+v)", len(got), len(tt.wantOrder), got)
			}

			gotOrder := make([]int64, len(got))
			for i, f := range got {
				gotOrder[i] = f.ID
			}
			for i := range tt.wantOrder {
				if gotOrder[i] != tt.wantOrder[i] {
					t.Fatalf("fused order = %v, want %v", gotOrder, tt.wantOrder)
				}
			}

			// Scores must match the hand-computed reference, and the slice must
			// be sorted strictly non-increasing.
			for i, f := range got {
				want, ok := tt.wantScore[f.ID]
				if !ok {
					t.Fatalf("unexpected id %d in fused result", f.ID)
				}
				if math.Abs(f.Score-want) > rrfEps {
					t.Fatalf("score(id=%d) = %.12f, want %.12f", f.ID, f.Score, want)
				}
				if i > 0 && got[i-1].Score < f.Score {
					t.Fatalf("fused not sorted desc at %d: %.12f < %.12f", i, got[i-1].Score, f.Score)
				}
			}
		})
	}
}

// TestReciprocalRankFusionTieBreakByID pins the documented tie-break: equal
// scores order by ascending id, regardless of insertion order across arms.
func TestReciprocalRankFusionTieBreakByID(t *testing.T) {
	// Every doc is alone at rank0 in its own arm, so all share score 1/61.
	arms := [][]vector.Scored{
		{{ID: 30, Sim: 1}},
		{{ID: 10, Sim: 1}},
		{{ID: 20, Sim: 1}},
	}
	got := reciprocalRankFusion(arms)
	wantOrder := []int64{10, 20, 30}
	if len(got) != len(wantOrder) {
		t.Fatalf("len = %d, want %d", len(got), len(wantOrder))
	}
	for i, f := range got {
		if f.ID != wantOrder[i] {
			t.Fatalf("order = %v..., want id %d at %d", f.ID, wantOrder[i], i)
		}
		if math.Abs(f.Score-rrf(0)) > rrfEps {
			t.Fatalf("score(id=%d) = %.12f, want %.12f", f.ID, f.Score, rrf(0))
		}
	}
}

// TestReciprocalRankFusionAccumulatesAcrossManyArms checks that a doc appearing
// in three arms accumulates all three contributions.
func TestReciprocalRankFusionAccumulatesAcrossManyArms(t *testing.T) {
	arms := [][]vector.Scored{
		{{ID: 5, Sim: 1}},                                   // rank0
		{{ID: 9, Sim: 1}, {ID: 5, Sim: 1}},                  // 5 at rank1
		{{ID: 9, Sim: 1}, {ID: 8, Sim: 1}, {ID: 5, Sim: 1}}, // 5 at rank2
	}
	got := reciprocalRankFusion(arms)
	scores := map[int64]float64{}
	for _, f := range got {
		scores[f.ID] = f.Score
	}
	want5 := rrf(0) + rrf(1) + rrf(2)
	if math.Abs(scores[5]-want5) > rrfEps {
		t.Fatalf("score(5) = %.12f, want %.12f", scores[5], want5)
	}
	want9 := rrf(0) + rrf(0) // rank0 in arms 2 and 3
	if math.Abs(scores[9]-want9) > rrfEps {
		t.Fatalf("score(9) = %.12f, want %.12f", scores[9], want9)
	}
	// doc 5 (three contributions) must outrank doc 9 (two) here.
	if got[0].ID != 5 {
		t.Fatalf("expected id 5 first, got %d", got[0].ID)
	}
}

// --- clamp --------------------------------------------------------------------

func TestClamp(t *testing.T) {
	tests := []struct {
		name      string
		v, lo, hi int
		want      int
	}{
		{"below floor", 2, 5, 100, 5},
		{"at floor", 5, 5, 100, 5},
		{"inside range", 42, 5, 100, 42},
		{"at ceiling", 100, 5, 100, 100},
		{"above ceiling", 1000, 5, 100, 100},
		{"negative below floor", -7, 5, 100, 5},
		{"zero below floor", 0, 5, 100, 5},
		{"degenerate lo==hi", 50, 7, 7, 7},
		// When lo > hi the floor check wins first (matches the implementation).
		{"inverted bounds clamps to lo", 50, 100, 5, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clamp(tt.v, tt.lo, tt.hi); got != tt.want {
				t.Fatalf("clamp(%d,%d,%d) = %d, want %d", tt.v, tt.lo, tt.hi, got, tt.want)
			}
		})
	}
}

// --- end-to-end Recall --------------------------------------------------------

// newStore spins up a fresh encrypted in-RAM store with a random DEK.
func newStore(t *testing.T) *store.Store {
	t.Helper()
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("rand dek: %v", err)
	}
	blob := filepath.Join(t.TempDir(), "mykeep.db.enc")
	s, err := store.OpenEncrypted(blob, secret.NewKeyStore(dek), store.Options{})
	if err != nil {
		t.Fatalf("OpenEncrypted: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// retain inserts each doc with a HashEmbedder embedding, mirroring the ingest
// path: EmbedModel is set to the embedder's Name() so VectorSearch can find it.
func retain(t *testing.T, s *store.Store, e embed.Embedder, bank string, docs []string) {
	t.Helper()
	ctx := context.Background()
	vecs, err := e.EmbedDocuments(ctx, docs)
	if err != nil {
		t.Fatalf("embed docs: %v", err)
	}
	items := make([]store.MemoryInput, len(docs))
	for i, d := range docs {
		items[i] = store.MemoryInput{
			Content:    d,
			Embedding:  vecs[i],
			EmbedModel: e.Name(),
		}
	}
	if n, err := s.Retain(bank, items); err != nil || n != len(docs) {
		t.Fatalf("retain: n=%d err=%v", n, err)
	}
}

func TestRecallRanksMatchingDocFirst(t *testing.T) {
	const bank = "default"
	s := newStore(t)
	e := embed.NewHashEmbedder(256)

	// Four docs with disjoint vocabulary so both the keyword arm (FTS5) and the
	// hash-vector arm concentrate on a single target for a focused query.
	const target = "the quartz crystal oscillator resonates at a precise frequency"
	docs := []string{
		"a recipe for sourdough bread requires patience and a warm kitchen",
		target,
		"the migratory albatross glides across the southern ocean for days",
		"compound interest accrues on the principal balance over many years",
	}
	retain(t, s, e, bank, docs)

	r := New(s, e)
	resp, err := r.Recall(context.Background(), bank, domain.RecallRequest{
		Query: "quartz crystal oscillator frequency",
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("Recall returned no results")
	}
	if resp.Results[0].Text != target {
		t.Fatalf("top result = %q, want target %q", resp.Results[0].Text, target)
	}
	// The target must also actually be present (defensive; rank-0 above implies it).
	found := false
	for _, res := range resp.Results {
		if res.Text == target {
			found = true
		}
	}
	if !found {
		t.Fatal("target doc not present in results")
	}
}

func TestRecallTraceReportsArms(t *testing.T) {
	const bank = "default"
	s := newStore(t)
	e := embed.NewHashEmbedder(128)
	retain(t, s, e, bank, []string{
		"violet sunsets over the desert mesa",
		"the violet flower blooms in early spring",
	})

	r := New(s, e)
	resp, err := r.Recall(context.Background(), bank, domain.RecallRequest{
		Query: "violet",
		Trace: true,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if resp.Trace == nil {
		t.Fatal("expected trace, got nil")
	}
	// Both keyword and semantic arms should fire ("violet" matches via FTS and
	// the hash embedder is configured), so arms == 2.
	if arms, ok := resp.Trace["arms"].(int); !ok || arms != 2 {
		t.Fatalf("trace arms = %v, want 2", resp.Trace["arms"])
	}
	if _, ok := resp.Trace["fused_count"]; !ok {
		t.Fatal("trace missing fused_count")
	}
	if _, ok := resp.Trace["k_final"]; !ok {
		t.Fatal("trace missing k_final")
	}
}

// TestRecallMaxTokensBudget asserts the greedy token budget truncates: with a
// budget large enough for only the first result, exactly one is returned; with
// a generous budget, all matching results come back.
func TestRecallMaxTokensBudget(t *testing.T) {
	const bank = "default"
	s := newStore(t)
	e := embed.NewHashEmbedder(256)

	// Each doc is the same length and shares the query word so all four match
	// the keyword arm; cost per result is len/4+1 tokens.
	docs := []string{
		"signal alpha alpha alpha alpha alpha alpha alpha",
		"signal bravo bravo bravo bravo bravo bravo bravo",
		"signal delta delta delta delta delta delta delta",
		"signal gamma gamma gamma gamma gamma gamma gamma",
	}
	retain(t, s, e, bank, docs)
	r := New(s, e)

	// Per-result token cost: each doc is 48 chars -> 48/4+1 = 13 tokens.
	const docLen = 48
	const perDoc = docLen/4 + 1 // 13
	for _, d := range docs {
		if len(d) != docLen {
			t.Fatalf("doc length assumption broke: %q is %d chars, want %d", d, len(d), docLen)
		}
	}

	// Budget for exactly one doc: first fits (13 <= 13), second overflows
	// (13+13 > 13) and the loop breaks -> exactly one result.
	tight, err := r.Recall(context.Background(), bank, domain.RecallRequest{
		Query:     "signal",
		MaxTokens: perDoc, // 13
	})
	if err != nil {
		t.Fatalf("Recall tight: %v", err)
	}
	if len(tight.Results) != 1 {
		t.Fatalf("tight budget returned %d results, want 1", len(tight.Results))
	}

	// Budget for exactly two docs.
	two, err := r.Recall(context.Background(), bank, domain.RecallRequest{
		Query:     "signal",
		MaxTokens: 2 * perDoc, // 26
	})
	if err != nil {
		t.Fatalf("Recall two: %v", err)
	}
	if len(two.Results) != 2 {
		t.Fatalf("two-doc budget returned %d results, want 2", len(two.Results))
	}

	// Generous budget: all four matching docs returned.
	all, err := r.Recall(context.Background(), bank, domain.RecallRequest{
		Query:     "signal",
		MaxTokens: 4096,
	})
	if err != nil {
		t.Fatalf("Recall all: %v", err)
	}
	if len(all.Results) != len(docs) {
		t.Fatalf("generous budget returned %d results, want %d", len(all.Results), len(docs))
	}
}

// TestRecallKeywordOnlyWhenNoEmbedder verifies Recall still works (keyword arm
// only) when no embedder is configured, and that ids round-trip as strings.
func TestRecallKeywordOnlyWhenNoEmbedder(t *testing.T) {
	const bank = "default"
	s := newStore(t)
	// Retain with a hash embedder so rows exist, but recall WITHOUT one.
	e := embed.NewHashEmbedder(128)
	retain(t, s, e, bank, []string{
		"the lighthouse keeper logs the tides each morning",
		"penguins huddle together for warmth in the antarctic winter",
	})

	r := New(s, nil) // no embedder -> semantic arm skipped
	resp, err := r.Recall(context.Background(), bank, domain.RecallRequest{
		Query: "lighthouse tides",
		Trace: true,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("keyword-only recall returned nothing")
	}
	if resp.Results[0].Text != "the lighthouse keeper logs the tides each morning" {
		t.Fatalf("top keyword result = %q", resp.Results[0].Text)
	}
	if arms, _ := resp.Trace["arms"].(int); arms != 1 {
		t.Fatalf("trace arms = %v, want 1 (keyword only)", resp.Trace["arms"])
	}
	// id must parse as the int64 row id.
	if _, err := strconv.ParseInt(resp.Results[0].ID, 10, 64); err != nil {
		t.Fatalf("result id %q is not a numeric row id: %v", resp.Results[0].ID, err)
	}
}

// TestRecallNoMatchesEmpty confirms a query whose tokens match no document
// yields an empty (non-error) result set when no embedder is present.
func TestRecallNoMatchesEmpty(t *testing.T) {
	const bank = "default"
	s := newStore(t)
	e := embed.NewHashEmbedder(64)
	retain(t, s, e, bank, []string{"apples and oranges in the fruit bowl"})

	r := New(s, nil)
	resp, err := r.Recall(context.Background(), bank, domain.RecallRequest{
		Query: "zzzznonexistenttoken",
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("expected no results, got %d", len(resp.Results))
	}
}

// TestRecallEmptyBankNoError ensures recalling from an empty bank is a clean
// no-op rather than an error.
func TestRecallEmptyBankNoError(t *testing.T) {
	s := newStore(t)
	r := New(s, embed.NewHashEmbedder(64))
	resp, err := r.Recall(context.Background(), "empty-bank", domain.RecallRequest{Query: "anything"})
	if err != nil {
		t.Fatalf("Recall empty bank: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("empty bank returned %d results", len(resp.Results))
	}
}
