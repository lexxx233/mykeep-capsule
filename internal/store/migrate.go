package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %q must be NNNN_name.sql", name)
		}
		v, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %q: bad version prefix: %w", name, err)
		}
		b, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: name, sql: string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

// migrate brings the in-RAM DB schema up to the highest embedded migration,
// forward-only and each migration in its own transaction. If the DB was written by
// a newer binary (version > highest embedded) it fails closed. Returns the number
// of migrations applied. (PLAN §10.5, M2.)
func (s *Store) migrate(binaryVersion string) (int, error) {
	ms, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	if len(ms) == 0 {
		return 0, errors.New("no embedded migrations")
	}
	maxV := ms[len(ms)-1].version

	cur, err := s.currentSchemaVersion()
	if err != nil {
		return 0, err
	}
	if cur > maxV {
		return 0, fmt.Errorf("this drive's data was written by a newer mykeep (schema v%d > supported v%d) — upgrade mykeep to open it", cur, maxV)
	}

	applied := 0
	for _, m := range ms {
		if m.version <= cur {
			continue
		}
		tx, err := s.conn.BeginTx(s.ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(s.ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return applied, fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(s.ctx, `DELETE FROM schema_version`); err != nil {
			_ = tx.Rollback()
			return applied, err
		}
		if _, err := tx.ExecContext(s.ctx,
			`INSERT INTO schema_version(version, min_binary, updated_at) VALUES(?,?,?)`,
			m.version, binaryVersion, time.Now().Unix()); err != nil {
			_ = tx.Rollback()
			return applied, err
		}
		if err := tx.Commit(); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// currentSchemaVersion returns the DB's schema version, or 0 if the schema_version
// table doesn't exist yet (a brand-new DB).
func (s *Store) currentSchemaVersion() (int, error) {
	var name string
	err := s.conn.QueryRowContext(s.ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var v sql.NullInt64
	if err := s.conn.QueryRowContext(s.ctx, `SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		return 0, err
	}
	if v.Valid {
		return int(v.Int64), nil
	}
	return 0, nil
}

// SchemaVersion exposes the current on-disk schema version (for doctor).
func (s *Store) SchemaVersion() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, _ := s.currentSchemaVersion()
	return v
}
