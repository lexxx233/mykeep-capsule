package ingest

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"mykeep.ai/internal/domain"
	"mykeep.ai/internal/store"
)

// CaptureTag marks auto-captured raw turns (single-sourced in domain).
const CaptureTag = domain.CaptureTag

const (
	dedupThreshold  = 0.97 // cosine ≥ this vs an existing capture ⇒ skip as duplicate
	dedupProbe      = 8    // nearest capture neighbors to check
	minCaptureChars = 8    // shorter turns are noise ("ok", "yes", "thanks")
)

// Capture logs one raw conversation turn as a low-tier `experience` memory tagged
// `capture`, with mechanical (no-LLM) hygiene: trivial/length gating and near-duplicate
// suppression against other captures. The trigger is automatic (a host hook per turn);
// the judgment — what to keep, what to promote — stays the agent's. This is the
// safety net that fixes silent under-retention without putting reasoning in mykeep.
func (in *Ingestor) Capture(ctx context.Context, bankID string, req domain.CaptureRequest) (domain.CaptureResponse, error) {
	content := strings.TrimSpace(req.Text)
	if req.Role != "" {
		content = strings.TrimSpace(req.Role + ": " + content)
	}

	// Trivial / length gates (before paying for an embed).
	if utf8.RuneCountInString(content) < minCaptureChars {
		return domain.CaptureResponse{Stored: false, Skipped: "too_short"}, nil
	}
	if !hasAlnum(content) {
		return domain.CaptureResponse{Stored: false, Skipped: "trivial"}, nil
	}
	// Truncate (don't chunk) so one capture == one row == one dedup vector.
	content = truncateRunes(content, maxChunkChars)

	now := time.Now().Unix()
	u := store.MemoryInput{
		Content:  content,
		FactType: "experience",
		EventAt:  &now,
		Tags:     append(append([]string{}, req.Tags...), CaptureTag),
	}

	if in.embedder != nil {
		vec, err := in.embedder.EmbedQuery(ctx, content)
		if err != nil {
			return domain.CaptureResponse{}, err
		}
		// Near-duplicate suppression vs other captures only (tag scopes the search to
		// captured rows; a curated memory must never suppress a capture or vice-versa).
		hits, err := in.store.VectorSearch(bankID, in.embedder.Name(), vec, []string{CaptureTag}, "any", dedupProbe)
		if err != nil {
			return domain.CaptureResponse{}, err
		}
		for _, h := range hits {
			if h.Sim >= dedupThreshold {
				return domain.CaptureResponse{Stored: false, Skipped: "duplicate"}, nil
			}
		}
		u.Embedding = vec
		u.EmbedModel = in.embedder.Name()
	}

	if _, err := in.store.Retain(bankID, []store.MemoryInput{u}); err != nil {
		return domain.CaptureResponse{}, err
	}

	resp := domain.CaptureResponse{Stored: true}
	if in.softCap > 0 {
		if sz := in.store.DBSizeBytes(); sz > in.softCap {
			resp.Warning = "approaching comfortable capacity; consider distilling and pruning captures"
		}
	}
	return resp, nil
}

func hasAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// truncateRunes returns the first n runes of s (trimmed), never splitting a rune.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for idx := range s {
		if i == n {
			return strings.TrimSpace(s[:idx])
		}
		i++
	}
	return s
}
