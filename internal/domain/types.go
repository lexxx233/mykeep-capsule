// Package domain holds the shared types that form joyvend's JSON/HTTP contract.
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
	Entities   []EntityInput     `json:"entities,omitempty"` // agent-supplied (joyvend has no LLM)
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

// ---- Recall ----

type RecallRequest struct {
	Query          string   `json:"query"` // REQUIRED
	Types          []string `json:"types,omitempty"`
	MaxTokens      int      `json:"max_tokens,omitempty"` // default 4096
	Tags           []string `json:"tags,omitempty"`
	TagsMatch      string   `json:"tags_match,omitempty"` // any|all|any_strict|all_strict (def any)
	QueryTimestamp *string  `json:"query_timestamp,omitempty"`
	Trace          bool     `json:"trace,omitempty"`
}

type RecallResponse struct {
	Results []RecallResult         `json:"results"`
	Trace   map[string]interface{} `json:"trace,omitempty"`
}

// ReflectResponse is a broad, synthesis-oriented context bundle: more memories than
// recall, entity-expanded, with the distinct entities surfaced so the calling agent
// can organize and reason over them. joyvend gathers; the agent synthesizes (and may
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
