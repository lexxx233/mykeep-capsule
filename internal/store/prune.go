package store

// pruneOrphansLocked deletes entity rows no longer referenced by any memory (mirrors
// hindsight's graph-maintenance orphan-entity prune). Caller must hold s.mu. Edges
// referencing pruned entities cascade away via the FK. Returns the count removed.
func (s *Store) pruneOrphansLocked() (int, error) {
	res, err := s.conn.ExecContext(s.ctx,
		`DELETE FROM entity WHERE id NOT IN (SELECT entity_id FROM memory_entity)`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PruneOrphans removes entity rows no memory references anymore (e.g. after deletes).
// Called automatically on delete; also exposed for `doctor` / manual cleanup.
func (s *Store) PruneOrphans() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errClosed
	}
	n, err := s.pruneOrphansLocked()
	if err == nil && n > 0 {
		s.markDirty()
	}
	return n, err
}
