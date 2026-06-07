// Package retrieval is joyvend's recall pipeline: parallel keyword + vector arms,
// fused with Reciprocal Rank Fusion (k=60, PLAN §5.4/§5.5), then a greedy token
// budget. (The temporal arm and cross-encoder rerank are deferred per the plan.)
package retrieval

import (
	"context"
	"math"
	"sort"
	"time"

	"joyvend.io/internal/domain"
	"joyvend.io/internal/embed"
	"joyvend.io/internal/store"
	"joyvend.io/internal/vector"
)

const (
	rrfK            = 60
	armLimit        = 300
	avgFactTokens   = 64
	defaultMaxToken = 4096
)

type Recaller struct {
	store    *store.Store
	embedder embed.Embedder
}

func New(s *store.Store, e embed.Embedder) *Recaller {
	return &Recaller{store: s, embedder: e}
}

func (r *Recaller) Recall(ctx context.Context, bankID string, req domain.RecallRequest) (domain.RecallResponse, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxToken
	}
	tagsMatch := req.TagsMatch
	if tagsMatch == "" {
		tagsMatch = "any"
	}

	fused, nArms, err := r.gatherRanked(ctx, bankID, req, tagsMatch, armLimit)
	if err != nil {
		return domain.RecallResponse{}, err
	}

	kFinal := clamp(maxTokens/avgFactTokens, 5, 100)
	take := kFinal * 2
	if take > len(fused) {
		take = len(fused)
	}
	ids := make([]int64, take)
	for i := 0; i < take; i++ {
		ids[i] = fused[i].ID
	}

	cands, err := r.store.LoadResults(ids)
	if err != nil {
		return domain.RecallResponse{}, err
	}

	// Greedy token budget: break (not skip) on first overflow (PLAN §5.4).
	var out []domain.RecallResult
	used := 0
	for _, c := range cands {
		t := len(c.Text)/4 + 1
		if used+t > maxTokens {
			break
		}
		out = append(out, c)
		used += t
	}

	resp := domain.RecallResponse{Results: out}
	if req.Trace {
		resp.Trace = map[string]interface{}{
			"arms":        nArms,
			"fused_count": len(fused),
			"k_final":     kFinal,
		}
	}
	return resp, nil
}

const (
	reflectArmLimit = 500
	reflectMaxToken = 8192
)

// Reflect assembles a broad, synthesis-oriented context bundle (PLAN §0.0): the same
// multi-arm retrieval as Recall but with a larger budget and seed set, plus
// associative expansion to memories sharing entities, and the distinct entities
// surfaced. joyvend gathers; the calling agent synthesizes and may retain its
// conclusions (e.g. tagged "reflection").
func (r *Recaller) Reflect(ctx context.Context, bankID string, req domain.RecallRequest) (domain.ReflectResponse, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = reflectMaxToken
	}
	tagsMatch := req.TagsMatch
	if tagsMatch == "" {
		tagsMatch = "any"
	}

	fused, _, err := r.gatherRanked(ctx, bankID, req, tagsMatch, reflectArmLimit)
	if err != nil {
		return domain.ReflectResponse{}, err
	}

	seedN := clamp(maxTokens/avgFactTokens, 10, 200)
	if seedN > len(fused) {
		seedN = len(fused)
	}
	seeds := make([]int64, seedN)
	for i := 0; i < seedN; i++ {
		seeds[i] = fused[i].ID
	}

	related, err := r.store.RelatedByEntities(bankID, seeds, req.Tags, tagsMatch, reflectArmLimit)
	if err != nil {
		return domain.ReflectResponse{}, err
	}

	seen := make(map[int64]bool, len(seeds)+len(related))
	ids := make([]int64, 0, len(seeds)+len(related))
	for _, id := range append(append([]int64{}, seeds...), related...) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	cands, err := r.store.LoadResults(ids)
	if err != nil {
		return domain.ReflectResponse{}, err
	}

	// Knowledge hierarchy (mirrors hindsight): surface the agent's stored syntheses
	// first — mental_model > observation > raw facts — preserving relevance order within
	// each tier, so reflect builds on prior conclusions rather than re-deriving them.
	sort.SliceStable(cands, func(i, j int) bool { return factTier(cands[i]) < factTier(cands[j]) })

	var out []domain.RecallResult
	entSeen := map[string]bool{}
	var entities []string
	used := 0
	for _, c := range cands {
		t := len(c.Text)/4 + 1
		if used+t > maxTokens {
			break
		}
		out = append(out, c)
		used += t
		for _, e := range c.Entities {
			if !entSeen[e] {
				entSeen[e] = true
				entities = append(entities, e)
			}
		}
	}
	return domain.ReflectResponse{Results: out, Entities: entities}, nil
}

// factTier ranks a memory by the knowledge hierarchy: agent-curated mental models
// first, then observations, then raw facts (PLAN §0.0 reflect).
func factTier(r domain.RecallResult) int {
	if r.Type == nil {
		return 2
	}
	switch *r.Type {
	case "mental_model":
		return 0
	case "observation":
		return 1
	default:
		return 2
	}
}

// gatherRanked runs the keyword + semantic + temporal arms, fuses them with RRF,
// applies the recency rerank, and returns candidates sorted by weight plus the number
// of arms that fired. Shared by Recall and Reflect.
func (r *Recaller) gatherRanked(ctx context.Context, bankID string, req domain.RecallRequest, tagsMatch string, limit int) ([]fusedScore, int, error) {
	var arms [][]vector.Scored

	kw, err := r.store.KeywordSearch(bankID, req.Query, req.Tags, tagsMatch, limit)
	if err != nil {
		return nil, 0, err
	}
	if len(kw) > 0 {
		arms = append(arms, kw)
	}
	if r.embedder != nil {
		qv, err := r.embedder.EmbedQuery(ctx, req.Query)
		if err != nil {
			return nil, 0, err
		}
		vs, err := r.store.VectorSearch(bankID, r.embedder.Name(), qv, req.Tags, tagsMatch, limit)
		if err != nil {
			return nil, 0, err
		}
		if len(vs) > 0 {
			arms = append(arms, vs)
		}
	}
	if win, ok := extractTemporalWindow(req.Query, temporalNow(req.QueryTimestamp)); ok {
		ts, err := r.store.TemporalSearch(bankID, win.start, win.end, req.Tags, tagsMatch, limit)
		if err != nil {
			return nil, 0, err
		}
		if len(ts) > 0 {
			arms = append(arms, ts)
		}
	}

	fused := reciprocalRankFusion(arms)
	if len(fused) > limit {
		fused = fused[:limit]
	}
	n := len(fused)
	allIDs := make([]int64, n)
	for i := range fused {
		allIDs[i] = fused[i].ID
	}
	events := r.store.EventAts(allIDs)
	now := time.Now().Unix()
	for i := range fused {
		base := 1.0 - 0.9*float64(i)/math.Max(1, float64(n-1))
		rec := recencyScore(events[fused[i].ID], now)
		fused[i].Weight = base * (1 + 0.2*(rec-0.5))
	}
	sort.Slice(fused, func(i, j int) bool {
		if fused[i].Weight == fused[j].Weight {
			return fused[i].ID < fused[j].ID
		}
		return fused[i].Weight > fused[j].Weight
	})
	return fused, len(arms), nil
}

type fusedScore struct {
	ID     int64
	Score  float64
	Weight float64
}

const yearSecs = 365 * 24 * 3600

// recencyScore is a linear 365-day decay to [0.1, 1.0]; nil (timeless) -> 0.5;
// future timestamps -> 1.0 (PLAN §5.4).
func recencyScore(eventAt *int64, now int64) float64 {
	if eventAt == nil {
		return 0.5
	}
	age := now - *eventAt
	if age < 0 {
		return 1.0
	}
	rec := 1.0 - float64(age)/float64(yearSecs)
	if rec < 0.1 {
		return 0.1
	}
	return rec
}

// reciprocalRankFusion merges ranked arms: score(d) += 1/(k+rank), rank from 1
// (mirrors hindsight fusion.py, verified PLAN §5.5).
func reciprocalRankFusion(arms [][]vector.Scored) []fusedScore {
	acc := make(map[int64]float64)
	for _, arm := range arms {
		for rank, doc := range arm {
			acc[doc.ID] += 1.0 / float64(rrfK+rank+1)
		}
	}
	out := make([]fusedScore, 0, len(acc))
	for id, sc := range acc {
		out = append(out, fusedScore{ID: id, Score: sc})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].ID < out[j].ID
		}
		return out[i].Score > out[j].Score
	})
	return out
}

// temporalNow resolves the reference instant for temporal parsing: the optional
// query_timestamp (ISO8601) or now.
func temporalNow(ts *string) int64 {
	if ts != nil {
		for _, layout := range []string{time.RFC3339, "2006-01-02"} {
			if t, err := time.Parse(layout, *ts); err == nil {
				return t.Unix()
			}
		}
	}
	return time.Now().Unix()
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
