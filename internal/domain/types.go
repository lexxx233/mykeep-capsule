// Package domain holds the shared types that form mykeep's JSON/HTTP contract.
package domain

// ---- Retain ----

type RetainRequest struct {
	Items []MemoryItem `json:"items"` // REQUIRED, len>=1
}

type MemoryItem struct {
	Content    string            `json:"content"`             // REQUIRED, non-empty
	Type       *string           `json:"type,omitempty"`      // world|experience|observation|mental_model (default experience)
	Timestamp  *string           `json:"timestamp,omitempty"` // ISO8601 | nil(now) | "unset"
	Context    *string           `json:"context,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	DocumentID *string           `json:"document_id,omitempty"`
	Entities   []EntityInput     `json:"entities,omitempty"` // agent-supplied (mykeep has no LLM)
	Tags       []string          `json:"tags,omitempty"`
	Supersedes []string          `json:"supersedes,omitempty"` // memory ids this item replaces (deleted after insert)
}

type EntityInput struct {
	Text string  `json:"text"`
	Type *string `json:"type,omitempty"`
}

type RetainResponse struct {
	Success    bool   `json:"success"`
	BankID     string `json:"bank_id"`
	ItemsCount int    `json:"items_count"`
	Warning    string `json:"warning,omitempty"` // soft-cap notice when DB > soft_cap_mb
}

// CaptureTag is the reserved tag on auto-captured raw turns. recall/reflect exclude it
// by default; it scopes capture dedup and the distill read. Agents must not hand-use it.
const CaptureTag = "capture"

// ---- Capture (auto-retain safety net) ----

// CaptureRequest logs one raw conversation turn. Unlike retain, the agent does no
// reasoning: no type (forced to "experience"), no entities, no supersedes. Normally
// written by a host hook per turn, not by the agent itself.
type CaptureRequest struct {
	Role string   `json:"role,omitempty"` // "user"|"assistant"|""; advisory, prefixed into content
	Text string   `json:"text"`           // REQUIRED, the raw turn
	Tags []string `json:"tags,omitempty"` // optional scoping; the reserved "capture" tag is always added
}

// CaptureResponse reports whether the turn was stored or mechanically skipped (the
// skip is the point — the caller wants to know it was a deduped no-op, not an error).
type CaptureResponse struct {
	Stored  bool   `json:"stored"`
	Skipped string `json:"skipped,omitempty"` // "duplicate"|"trivial"|"too_short" when not stored
	ID      string `json:"id,omitempty"`      // memory id when stored
	Warning string `json:"warning,omitempty"` // soft-cap notice
}

// ---- Recall ----

type RecallRequest struct {
	Query           string   `json:"query"` // REQUIRED
	Types           []string `json:"types,omitempty"`
	MaxTokens       int      `json:"max_tokens,omitempty"` // default 4096
	Tags            []string `json:"tags,omitempty"`
	TagsMatch       string   `json:"tags_match,omitempty"`       // any|all|any_strict|all_strict (def any)
	IncludeCaptures bool     `json:"include_captures,omitempty"` // surface raw `capture` rows (excluded by default)
	QueryTimestamp  *string  `json:"query_timestamp,omitempty"`
	Trace           bool     `json:"trace,omitempty"`
}

type RecallResponse struct {
	Results []RecallResult         `json:"results"`
	Trace   map[string]interface{} `json:"trace,omitempty"`
}

// ReflectResponse is a broad, synthesis-oriented context bundle: more memories than
// recall, entity-expanded, with the distinct entities surfaced so the calling agent
// can organize and reason over them. mykeep gathers; the agent synthesizes (and may
// retain its conclusions). PLAN §0.0.
type ReflectResponse struct {
	Results  []RecallResult `json:"results"`
	Entities []string       `json:"entities,omitempty"`
}

type RecallResult struct {
	ID            string            `json:"id"`
	Text          string            `json:"text"`
	Type          *string           `json:"type,omitempty"`
	Entities      []string          `json:"entities,omitempty"`
	Context       *string           `json:"context,omitempty"`
	OccurredStart *string           `json:"occurred_start,omitempty"`
	OccurredEnd   *string           `json:"occurred_end,omitempty"`
	MentionedAt   *string           `json:"mentioned_at,omitempty"`
	DocumentID    *string           `json:"document_id,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
}

// ---- Banks / admin ----

type Bank struct {
	BankID    string  `json:"bank_id"`
	Name      *string `json:"name,omitempty"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

type BankSummary struct {
	BankID    string `json:"bank_id"`
	FactCount int    `json:"fact_count"`
	CreatedAt string `json:"created_at"`
}

type ListMemoriesResponse struct {
	Items  []RecallResult `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// ---- Lifecycle ----

type Settings struct {
	EmbeddingModel string `json:"embedding_model"`
	EmbeddingDim   int    `json:"embedding_dim"`
	Embedder       string `json:"embedder"` // "local:<model>" | "hash-fallback"
}

type HealthResponse struct {
	Status           string `json:"status"`
	Version          string `json:"version"`
	Portable         bool   `json:"portable"`
	ContentEncrypted bool   `json:"content_encrypted"`
	Embedder         string `json:"embedder"`
	MemoryCount      int    `json:"memory_count"`
	DBSizeBytes      int64  `json:"db_size_bytes"`
	FlushError       string `json:"flush_error,omitempty"` // last re-seal error, if persistence is failing
}
