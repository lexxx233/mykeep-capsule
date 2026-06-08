package store

import (
	"database/sql"
	"errors"
	"time"

	"mykeep.ai/internal/domain"
)

// ListBanks returns a summary of every bank.
func (s *Store) ListBanks() ([]domain.BankSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.conn.QueryContext(s.ctx, `
		SELECT b.bank_id, b.created_at,
		       (SELECT COUNT(*) FROM memory m WHERE m.bank_id=b.bank_id)
		FROM bank b ORDER BY b.bank_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BankSummary
	for rows.Next() {
		var (
			id      string
			created int64
			count   int
		)
		if err := rows.Scan(&id, &created, &count); err != nil {
			return nil, err
		}
		out = append(out, domain.BankSummary{BankID: id, FactCount: count, CreatedAt: isoFromUnix(created)})
	}
	return out, rows.Err()
}

// PutBank upserts a bank's display name.
func (s *Store) PutBank(bankID string, name *string) (domain.Bank, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.Bank{}, errClosed
	}
	now := time.Now().Unix()
	if _, err := s.conn.ExecContext(s.ctx,
		`INSERT INTO bank(bank_id, name, created_at, updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(bank_id) DO UPDATE SET name=excluded.name, updated_at=excluded.updated_at`,
		bankID, name, now, now); err != nil {
		return domain.Bank{}, err
	}
	s.markDirty()
	var (
		gotName      sql.NullString
		created, upd int64
	)
	if err := s.conn.QueryRowContext(s.ctx,
		`SELECT name, created_at, updated_at FROM bank WHERE bank_id=?`, bankID).
		Scan(&gotName, &created, &upd); err != nil {
		return domain.Bank{}, err
	}
	b := domain.Bank{BankID: bankID, CreatedAt: isoFromUnix(created), UpdatedAt: isoFromUnix(upd)}
	if gotName.Valid {
		b.Name = ptr(gotName.String)
	}
	return b, nil
}

// DeleteBank removes a bank and all its memories (cascades).
func (s *Store) DeleteBank(bankID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, errClosed
	}
	res, err := s.conn.ExecContext(s.ctx, `DELETE FROM bank WHERE bank_id=?`, bankID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if s.vecAvailable && s.vecCreated {
			_, _ = s.conn.ExecContext(s.ctx, `DELETE FROM vec_idx WHERE bank_id=?`, bankID)
		}
		_, _ = s.pruneOrphansLocked()
		s.markDirty()
	}
	return n > 0, nil
}

// BankExists reports whether a bank has been created.
func (s *Store) BankExists(bankID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var one int
	err := s.conn.QueryRowContext(s.ctx, `SELECT 1 FROM bank WHERE bank_id=?`, bankID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
