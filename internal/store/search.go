package store

import (
	"fmt"
	"sort"
	"strings"

	"mykeep.ai/internal/domain"
	"mykeep.ai/internal/vector"
)

// KeywordSearch runs an FTS5/BM25 query over a bank, best-first. Returns up to
// limit memory ids (Sim = -bm25 so higher is better).
func (s *Store) KeywordSearch(bankID, query string, tags []string, tagsMatch string, limit int, excludeTags ...string) ([]vector.Scored, error) {
	toks := tokenize(query)
	if len(toks) == 0 {
		return nil, nil
	}
	match := ftsQuery(toks)
	tagClause, tagArgs := tagFilter("m.id", tags, tagsMatch)
	exClause, exArgs := tagExcludeFilter("m.id", excludeTags)

	q := `SELECT f.rowid, bm25(memory_fts) AS score
	      FROM memory_fts f JOIN memory m ON m.id = f.rowid
	      WHERE memory_fts MATCH ? AND m.bank_id = ?` + tagClause + exClause + `
	      ORDER BY score ASC LIMIT ?`
	args := append([]any{match, bankID}, tagArgs...)
	args = append(args, exArgs...)
	args = append(args, limit)

	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.conn.QueryContext(s.ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []vector.Scored
	for rows.Next() {
		var id int64
		var bm float64
		if err := rows.Scan(&id, &bm); err != nil {
			return nil, err
		}
		out = append(out, vector.Scored{ID: id, Sim: -bm})
	}
	return out, rows.Err()
}

// VectorSearch returns the nearest memories by cosine. Default path is vec0 KNN
// (D1), filtered by bank+model; it falls back to an exact brute-force scan when vec0
// is unavailable or when tags are present (tags need the join). Both are exact.
func (s *Store) VectorSearch(bankID, model string, query []float32, tags []string, tagsMatch string, limit int, excludeTags ...string) ([]vector.Scored, error) {
	// vec0 KNN can't express a tag join/anti-join. BUT auto-`capture` rows are never
	// inserted into vec_idx, so excluding ONLY the capture tag needs no anti-join — vec0
	// already returns curated-only. Any include-tag, or any other exclude-tag, → brute-force.
	if len(dedupe(tags)) == 0 && excludesOnlyCaptures(excludeTags) {
		s.mu.Lock()
		useVec := s.vecAvailable && s.vecCreated
		s.mu.Unlock()
		if useVec {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.vec0KNN(bankID, model, query, limit)
		}
	}
	return s.bruteForceSearch(bankID, model, query, tags, tagsMatch, limit, excludeTags)
}

// VectorSearchExact always scans the full embedding table (exact brute-force), so it
// covers rows absent from vec_idx — i.e. auto-captures. Used for include_captures recall.
func (s *Store) VectorSearchExact(bankID, model string, query []float32, tags []string, tagsMatch string, limit int, excludeTags ...string) ([]vector.Scored, error) {
	return s.bruteForceSearch(bankID, model, query, tags, tagsMatch, limit, excludeTags)
}

// excludesOnlyCaptures reports whether excludeTags is empty or exactly {capture} — the
// case where vec0 stays valid because captures aren't indexed.
func excludesOnlyCaptures(excludeTags []string) bool {
	for _, t := range dedupe(excludeTags) {
		if t != domain.CaptureTag {
			return false
		}
	}
	return true
}

func (s *Store) bruteForceSearch(bankID, model string, query []float32, tags []string, tagsMatch string, limit int, excludeTags []string) ([]vector.Scored, error) {
	tagClause, tagArgs := tagFilter("e.memory_id", tags, tagsMatch)
	exClause, exArgs := tagExcludeFilter("e.memory_id", excludeTags)
	q := `SELECT e.memory_id, e.vec FROM embedding e
	      WHERE e.bank_id = ? AND e.model = ?` + tagClause + exClause
	args := append([]any{bankID, model}, tagArgs...)
	args = append(args, exArgs...)

	qn := vector.Normalize(query)

	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.conn.QueryContext(s.ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scored []vector.Scored
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		v := vector.Decode(blob)
		scored = append(scored, vector.Scored{ID: id, Sim: vector.Dot(qn, v)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Sim > scored[j].Sim })
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

// TemporalSearch returns memories whose event_at falls in [start,end], most-recent
// first (the temporal recall arm, PLAN §5.4/D14).
func (s *Store) TemporalSearch(bankID string, start, end int64, tags []string, tagsMatch string, limit int, excludeTags ...string) ([]vector.Scored, error) {
	tagClause, tagArgs := tagFilter("m.id", tags, tagsMatch)
	exClause, exArgs := tagExcludeFilter("m.id", excludeTags)
	q := `SELECT m.id FROM memory m
	      WHERE m.bank_id=? AND m.event_at IS NOT NULL AND m.event_at>=? AND m.event_at<=?` + tagClause + exClause + `
	      ORDER BY m.event_at DESC LIMIT ?`
	args := append([]any{bankID, start, end}, tagArgs...)
	args = append(args, exArgs...)
	args = append(args, limit)

	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.conn.QueryContext(s.ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []vector.Scored
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, vector.Scored{ID: id, Sim: 1})
	}
	return out, rows.Err()
}

// RelatedByEntities finds other memories in a bank that share an entity with any of
// the seed memories (associative expansion for reflect, PLAN §0.0). The tag filter is
// applied to the *related* memories so expansion never crosses a tag-isolation
// boundary (e.g. per-user scoping). Empty when the agent supplied no entities.
func (s *Store) RelatedByEntities(bankID string, seedIDs []int64, tags []string, tagsMatch string, limit int) ([]int64, error) {
	if len(seedIDs) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(seedIDs)), ",")
	tagClause, tagArgs := tagFilter("me2.memory_id", tags, tagsMatch)
	q := `SELECT DISTINCT me2.memory_id
	      FROM memory_entity me1
	      JOIN memory_entity me2 ON me1.entity_id = me2.entity_id
	      JOIN memory m ON m.id = me2.memory_id
	      WHERE m.bank_id=? AND me1.memory_id IN (` + ph + `) AND me2.memory_id NOT IN (` + ph + `)` + tagClause + `
	      LIMIT ?`
	args := make([]any, 0, 2*len(seedIDs)+len(tagArgs)+2)
	args = append(args, bankID)
	for _, id := range seedIDs {
		args = append(args, id)
	}
	for _, id := range seedIDs {
		args = append(args, id)
	}
	args = append(args, tagArgs...)
	args = append(args, limit)

	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.conn.QueryContext(s.ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// tagFilter builds a SQL fragment restricting idCol to memories matching tags.
// tagsMatch: "all"/"all_strict" require every tag; otherwise any (>=1). Empty tags
// → no filter.
func tagFilter(idCol string, tags []string, tagsMatch string) (string, []any) {
	clean := dedupe(tags)
	if len(clean) == 0 {
		return "", nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(clean)), ",")
	args := make([]any, 0, len(clean)+1)
	for _, t := range clean {
		args = append(args, t)
	}
	if tagsMatch == "all" || tagsMatch == "all_strict" {
		args = append(args, len(clean))
		return fmt.Sprintf(
			" AND %s IN (SELECT memory_id FROM memory_tag WHERE tag IN (%s) GROUP BY memory_id HAVING COUNT(DISTINCT tag)=?)",
			idCol, placeholders), args
	}
	return fmt.Sprintf(" AND %s IN (SELECT memory_id FROM memory_tag WHERE tag IN (%s))", idCol, placeholders), args
}

// tagExcludeFilter builds a SQL fragment excluding any memory carrying one of tags
// (used to keep auto-`capture` rows out of recall/reflect by default). Empty → no filter.
func tagExcludeFilter(idCol string, tags []string) (string, []any) {
	clean := dedupe(tags)
	if len(clean) == 0 {
		return "", nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(clean)), ",")
	args := make([]any, 0, len(clean))
	for _, t := range clean {
		args = append(args, t)
	}
	return fmt.Sprintf(" AND %s NOT IN (SELECT memory_id FROM memory_tag WHERE tag IN (%s))", idCol, placeholders), args
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	var toks []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			toks = append(toks, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return toks
}

func ftsQuery(toks []string) string {
	parts := make([]string, len(toks))
	for i, t := range toks {
		parts[i] = `"` + t + `"`
	}
	return strings.Join(parts, " OR ")
}
