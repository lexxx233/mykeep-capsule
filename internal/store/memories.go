package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"mykeep.ai/internal/domain"
	"mykeep.ai/internal/vector"
)

var errClosed = errors.New("mykeep: store closed")

// MemoryInput is one unit to persist (the ingest layer computes the embedding).
type MemoryInput struct {
	Content    string
	FactType   string
	Context    *string
	DocumentID *string
	EventAt    *int64
	EventEnd   *int64
	Metadata   map[string]string
	Tags       []string
	Entities   []domain.EntityInput
	Embedding  []float32 // nil if no embedder
	EmbedModel string
	Enriched   bool
}

// Retain inserts a batch of memory units for one bank in a single transaction,
// then marks the store dirty (arming the debounced re-seal).
func (s *Store) Retain(bankID string, items []MemoryInput) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errClosed
	}
	// Ensure the vec0 index exists (created + backfilled outside the tx) before we
	// start inserting, so its creation isn't tied to this transaction's outcome.
	if s.vecAvailable && !s.vecCreated {
		for _, it := range items {
			if len(it.Embedding) > 0 {
				if err := s.ensureVecIndex(len(it.Embedding)); err != nil {
					return 0, err
				}
				break
			}
		}
	}
	tx, err := s.conn.BeginTx(s.ctx, nil)
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(s.ctx,
		`INSERT INTO bank(bank_id, created_at, updated_at) VALUES(?,?,?)
		 ON CONFLICT(bank_id) DO UPDATE SET updated_at=excluded.updated_at`,
		bankID, now, now); err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	count := 0
	for _, it := range items {
		if err := s.insertOne(tx, bankID, now, it); err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.markDirty()
	return count, nil
}

func (s *Store) insertOne(tx *sql.Tx, bankID string, now int64, it MemoryInput) error {
	ctx := s.ctx
	ft := it.FactType
	if ft == "" {
		ft = "experience"
	}
	var metaJSON *string
	if len(it.Metadata) > 0 {
		b, _ := json.Marshal(it.Metadata)
		s := string(b)
		metaJSON = &s
	}
	var embedder *string
	if it.EmbedModel != "" {
		embedder = &it.EmbedModel
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO memory(bank_id, content, fact_type, context, document_id, created_at, event_at, event_end, metadata, embedder, enriched)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		bankID, it.Content, ft, it.Context, it.DocumentID, now, it.EventAt, it.EventEnd, metaJSON, embedder, boolToInt(it.Enriched))
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	for _, tag := range dedupe(it.Tags) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_tag(memory_id, tag) VALUES(?,?)`, id, tag); err != nil {
			return err
		}
	}
	if it.Embedding != nil {
		nv := vector.Normalize(it.Embedding)
		blob := vector.Encode(nv)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO embedding(memory_id, bank_id, model, dim, vec) VALUES(?,?,?,?,?)`,
			id, bankID, it.EmbedModel, len(nv), blob); err != nil {
			return err
		}
		// Auto-captures are excluded from recall by default, so keep them OUT of the
		// vec0 index — that way default recall stays on the fast vec0 KNN path (no tag
		// anti-join → no brute-force). They remain in `embedding` for the brute-force
		// path used by include_captures + dedup.
		if s.vecAvailable && s.vecCreated && !hasTag(it.Tags, domain.CaptureTag) {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO vec_idx(memory_id, bank_id, model, embedding) VALUES(?,?,?,?)`,
				id, bankID, it.EmbedModel, blob); err != nil {
				return err
			}
		}
	}
	for _, ent := range it.Entities {
		if ent.Text == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO entity(bank_id, name, type) VALUES(?,?,?)`, bankID, ent.Text, ent.Type); err != nil {
			return err
		}
		var eid int64
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM entity WHERE bank_id=? AND name=? AND ((type IS NULL AND ? IS NULL) OR type=?)`,
			bankID, ent.Text, ent.Type, ent.Type).Scan(&eid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_entity(memory_id, entity_id) VALUES(?,?)`, id, eid); err != nil {
			return err
		}
	}
	return nil
}

// ListMemories returns memories in a bank, newest first, paginated.
func (s *Store) ListMemories(bankID string, limit, offset int) ([]domain.RecallResult, int, error) {
	return s.ListMemoriesFiltered(bankID, limit, offset, "", "")
}

// ListMemoriesFiltered is ListMemories with optional fact_type and tag filters (e.g.
// type=experience&tag=capture to read the raw-capture substrate for distillation).
func (s *Store) ListMemoriesFiltered(bankID string, limit, offset int, factType, tag string) ([]domain.RecallResult, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 { // bound the amplified read (one sub-query set per row)
		limit = 1000
	}
	if offset < 0 { // a negative offset yields a SQLite error → 500
		offset = 0
	}
	where := ` WHERE bank_id=?`
	args := []any{bankID}
	if factType != "" {
		where += ` AND fact_type=?`
		args = append(args, factType)
	}
	if tag != "" {
		where += ` AND id IN (SELECT memory_id FROM memory_tag WHERE tag=?)`
		args = append(args, tag)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var total int
	if err := s.conn.QueryRowContext(s.ctx, `SELECT COUNT(*) FROM memory`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	listArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.conn.QueryContext(s.ctx,
		`SELECT id FROM memory`+where+` ORDER BY event_at DESC, created_at DESC LIMIT ? OFFSET ?`,
		listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	out := make([]domain.RecallResult, 0, len(ids))
	for _, id := range ids {
		r, ok, err := s.loadResult(id)
		if err != nil {
			return nil, 0, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, total, nil
}

// MemoryCount returns the total number of stored memories.
func (s *Store) MemoryCount() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	err := s.conn.QueryRowContext(s.ctx, `SELECT COUNT(*) FROM memory`).Scan(&n)
	return n, err
}

// DeleteMemory removes one memory (cascades to tags/embeddings/entities/fts).
func (s *Store) DeleteMemory(bankID string, id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, errClosed
	}
	res, err := s.conn.ExecContext(s.ctx, `DELETE FROM memory WHERE id=? AND bank_id=?`, id, bankID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if s.vecAvailable && s.vecCreated { // vec0 isn't covered by FK cascade
			_, _ = s.conn.ExecContext(s.ctx, `DELETE FROM vec_idx WHERE memory_id=?`, id)
		}
		_, _ = s.pruneOrphansLocked()
		s.markDirty()
	}
	return n > 0, nil
}

// LoadResults fetches recall-result rows for the given memory ids, preserving order.
func (s *Store) LoadResults(ids []int64) ([]domain.RecallResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.RecallResult, 0, len(ids))
	for _, id := range ids {
		r, ok, err := s.loadResult(id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Store) loadResult(id int64) (domain.RecallResult, bool, error) {
	var (
		content   string
		factType  string
		ctxv      sql.NullString
		docID     sql.NullString
		eventAt   sql.NullInt64
		eventEnd  sql.NullInt64
		createdAt int64
		metaJSON  sql.NullString
	)
	err := s.conn.QueryRowContext(s.ctx,
		`SELECT content, fact_type, context, document_id, event_at, event_end, created_at, metadata FROM memory WHERE id=?`, id).
		Scan(&content, &factType, &ctxv, &docID, &eventAt, &eventEnd, &createdAt, &metaJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RecallResult{}, false, nil
	}
	if err != nil {
		return domain.RecallResult{}, false, err
	}
	r := domain.RecallResult{ID: strconv.FormatInt(id, 10), Text: content}
	r.Type = ptr(factType)
	if ctxv.Valid {
		r.Context = ptr(ctxv.String)
	}
	if docID.Valid {
		r.DocumentID = ptr(docID.String)
	}
	if eventAt.Valid {
		r.OccurredStart = ptr(isoFromUnix(eventAt.Int64))
	}
	if eventEnd.Valid {
		r.OccurredEnd = ptr(isoFromUnix(eventEnd.Int64))
	}
	r.MentionedAt = ptr(isoFromUnix(createdAt))
	if metaJSON.Valid && metaJSON.String != "" {
		_ = json.Unmarshal([]byte(metaJSON.String), &r.Metadata)
	}
	r.Tags = s.tagsFor(id)
	r.Entities = s.entitiesFor(id)
	return r, true, nil
}

func (s *Store) tagsFor(id int64) []string {
	rows, err := s.conn.QueryContext(s.ctx, `SELECT tag FROM memory_tag WHERE memory_id=? ORDER BY tag`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			out = append(out, t)
		}
	}
	return out
}

func (s *Store) entitiesFor(id int64) []string {
	rows, err := s.conn.QueryContext(s.ctx,
		`SELECT e.name FROM entity e JOIN memory_entity me ON me.entity_id=e.id WHERE me.memory_id=? ORDER BY e.name`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out = append(out, n)
		}
	}
	return out
}
