package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite/vec" // pure-Go sqlite-vec: registers the vec0 vtable (D1)

	"mykeep.ai/internal/domain"
	"mykeep.ai/internal/vector"
)

// setupVec0 probes for vec0 (the pure-Go modernc/sqlite/vec backend). If present it
// becomes the default KNN path; the brute-force scan over the embedding BLOBs is the
// fallback (and handles tag-filtered queries). The vec0 index is created + backfilled
// from the embedding table on first enablement, then persists in the encrypted blob.
func (s *Store) setupVec0(dim int) {
	if _, err := s.conn.ExecContext(s.ctx, `CREATE VIRTUAL TABLE _vecprobe USING vec0(e float[2])`); err != nil {
		return // vec0 unavailable -> brute-force only (still exact)
	}
	_, _ = s.conn.ExecContext(s.ctx, `DROP TABLE _vecprobe`)
	s.vecAvailable = true

	if s.tableExists("vec_idx") { // carried over in the serialized DB from a prior session
		s.vecCreated = true
		return
	}
	if dim <= 0 { // infer from any existing embedding
		var d sql.NullInt64
		_ = s.conn.QueryRowContext(s.ctx, `SELECT dim FROM embedding LIMIT 1`).Scan(&d)
		if d.Valid {
			dim = int(d.Int64)
		}
	}
	if dim > 0 {
		if err := s.ensureVecIndex(dim); err != nil {
			s.vecAvailable = false
		}
	}
}

func (s *Store) tableExists(name string) bool {
	var got string
	return s.conn.QueryRowContext(s.ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got) == nil
}

// ensureVecIndex creates vec_idx (if missing) and backfills it from the embedding
// table. Runs outside any retain transaction so vecCreated is consistent.
func (s *Store) ensureVecIndex(dim int) error {
	if s.vecCreated {
		return nil
	}
	if _, err := s.conn.ExecContext(s.ctx, fmt.Sprintf(
		`CREATE VIRTUAL TABLE IF NOT EXISTS vec_idx USING vec0(memory_id integer primary key, bank_id text, model text, embedding float[%d] distance_metric=cosine)`, dim)); err != nil {
		return err
	}
	// Backfill curated rows only — auto-captures stay out of the index (see insertOne).
	if _, err := s.conn.ExecContext(s.ctx,
		`INSERT INTO vec_idx(memory_id,bank_id,model,embedding)
		 SELECT memory_id,bank_id,model,vec FROM embedding
		 WHERE memory_id NOT IN (SELECT memory_id FROM memory_tag WHERE tag=?)`, domain.CaptureTag); err != nil {
		return err
	}
	s.vecCreated = true
	s.vecDim = dim
	return nil
}

// vec0KNN runs a metadata-filtered KNN over vec_idx for one bank+model. Tags are not
// supported here (the caller routes tag-filtered queries to brute force).
func (s *Store) vec0KNN(bankID, model string, query []float32, limit int) ([]vector.Scored, error) {
	blob := vector.Encode(vector.Normalize(query))
	rows, err := s.conn.QueryContext(s.ctx,
		`SELECT memory_id, distance FROM vec_idx WHERE embedding MATCH ? AND k=? AND bank_id=? AND model=? ORDER BY distance`,
		blob, limit, bankID, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []vector.Scored
	for rows.Next() {
		var id int64
		var dist float64
		if err := rows.Scan(&id, &dist); err != nil {
			return nil, err
		}
		out = append(out, vector.Scored{ID: id, Sim: 1.0 - dist}) // cosine distance -> similarity
	}
	return out, rows.Err()
}

// VecAvailable reports whether the vec0 KNN backend is active (for doctor).
func (s *Store) VecAvailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vecAvailable
}
