// Package ingest is joyvend's retain pipeline: chunk -> local embed -> store
// (PLAN §1.3, M4). joyvend runs no LLM, so there is no extraction step; the agent
// may pass structured entities in MemoryItem.Entities.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"joyvend.io/internal/domain"
	"joyvend.io/internal/embed"
	"joyvend.io/internal/store"
)

const maxChunkChars = 3000

type Ingestor struct {
	store    *store.Store
	embedder embed.Embedder
	softCap  int64 // bytes; 0 disables the warning
}

func New(s *store.Store, e embed.Embedder, softCapMB int) *Ingestor {
	return &Ingestor{store: s, embedder: e, softCap: int64(softCapMB) * 1024 * 1024}
}

// Retain chunks each item, embeds the chunks locally, and stores them.
func (in *Ingestor) Retain(ctx context.Context, bankID string, req domain.RetainRequest) (domain.RetainResponse, error) {
	if len(req.Items) == 0 {
		return domain.RetainResponse{}, errors.New("retain: items is required")
	}
	var (
		texts []string
		units []store.MemoryInput
	)
	for _, item := range req.Items {
		if strings.TrimSpace(item.Content) == "" {
			return domain.RetainResponse{}, errors.New("retain: item content is required")
		}
		factType, err := resolveFactType(item.Type)
		if err != nil {
			return domain.RetainResponse{}, err
		}
		eventAt := parseTimestamp(item.Timestamp)
		for _, chunk := range chunk(item.Content) {
			u := store.MemoryInput{
				Content:    chunk,
				FactType:   factType,
				Context:    item.Context,
				DocumentID: item.DocumentID,
				EventAt:    eventAt,
				Metadata:   item.Metadata,
				Tags:       item.Tags,
				Entities:   item.Entities,
			}
			units = append(units, u)
			texts = append(texts, chunk)
		}
	}

	if in.embedder != nil {
		vecs, err := in.embedder.EmbedDocuments(ctx, texts)
		if err != nil {
			return domain.RetainResponse{}, err
		}
		if len(vecs) != len(texts) {
			return domain.RetainResponse{}, fmt.Errorf("embedder returned %d vectors for %d chunks", len(vecs), len(texts))
		}
		for i := range units {
			units[i].Embedding = vecs[i]
			units[i].EmbedModel = in.embedder.Name()
		}
	}

	n, err := in.store.Retain(bankID, units)
	if err != nil {
		return domain.RetainResponse{}, err
	}

	// Supersession: after inserting the new memory (e.g. an updated mental_model), delete
	// the memories it replaces. The agent decides what supersedes what — the relocated
	// equivalent of hindsight's LLM-adjudicated merge. Orphan entities are pruned by Delete.
	for _, item := range req.Items {
		for _, sid := range item.Supersedes {
			if id, perr := strconv.ParseInt(sid, 10, 64); perr == nil {
				_, _ = in.store.DeleteMemory(bankID, id)
			}
		}
	}

	resp := domain.RetainResponse{Success: true, BankID: bankID, ItemsCount: n}
	if in.softCap > 0 {
		if sz := in.store.DBSizeBytes(); sz > in.softCap {
			resp.Warning = "approaching comfortable capacity; recall and re-seal will slow"
		}
	}
	return resp, nil
}

// validFactTypes are the knowledge-hierarchy tiers (PLAN: borrowed from hindsight's
// raw-facts -> observations -> mental-models). The agent supplies them; joyvend stores
// them and reflect prioritizes the syntheses.
var validFactTypes = map[string]bool{
	"world": true, "experience": true, "observation": true, "mental_model": true,
}

func resolveFactType(t *string) (string, error) {
	if t == nil || *t == "" {
		return "experience", nil
	}
	if !validFactTypes[*t] {
		return "", errors.New("retain: invalid type (want world|experience|observation|mental_model)")
	}
	return *t, nil
}

// parseTimestamp: nil -> now; "unset" -> nil (timeless); ISO8601 -> unix seconds.
func parseTimestamp(ts *string) *int64 {
	if ts == nil {
		n := time.Now().Unix()
		return &n
	}
	if *ts == "unset" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02"} {
		if t, err := time.Parse(layout, *ts); err == nil {
			u := t.Unix()
			return &u
		}
	}
	n := time.Now().Unix()
	return &n
}

// chunk splits text into <=maxChunkChars pieces, breaking at whitespace when possible.
func chunk(text string) []string {
	text = strings.TrimSpace(text)
	if len(text) <= maxChunkChars {
		return []string{text}
	}
	var out []string
	for len(text) > maxChunkChars {
		cut := maxChunkChars
		if idx := strings.LastIndexAny(text[:maxChunkChars], " \n\t"); idx > maxChunkChars/2 {
			cut = idx
		}
		out = append(out, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}
