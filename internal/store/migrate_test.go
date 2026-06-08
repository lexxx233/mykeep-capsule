package store

import (
	"crypto/rand"
	"path/filepath"
	"strings"
	"testing"

	"mykeep.ai/internal/secret"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}
	s, err := OpenEncrypted(filepath.Join(t.TempDir(), "db.enc"), secret.NewKeyStore(dek), Options{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrateFreshToLatestAndIdempotent(t *testing.T) {
	s := openTestStore(t)
	if v := s.SchemaVersion(); v != 2 {
		t.Fatalf("fresh schema version = %d, want 2", v)
	}
	// the v2 edge table must exist and be usable
	if _, err := s.conn.ExecContext(s.ctx, `SELECT count(*) FROM edge`); err != nil {
		t.Fatalf("edge table missing after migrate: %v", err)
	}
	// running migrate again applies nothing
	applied, err := s.migrate("test")
	if err != nil || applied != 0 {
		t.Fatalf("re-migrate applied=%d err=%v, want 0/nil", applied, err)
	}
}

func TestMigrateFailClosedOnNewerDB(t *testing.T) {
	s := openTestStore(t)
	// simulate a DB written by a newer binary
	if _, err := s.conn.ExecContext(s.ctx,
		`DELETE FROM schema_version; INSERT INTO schema_version(version,min_binary,updated_at) VALUES(99,'future',0)`); err != nil {
		t.Fatal(err)
	}
	_, err := s.migrate("test")
	if err == nil || !strings.Contains(err.Error(), "newer mykeep") {
		t.Fatalf("expected fail-closed error, got %v", err)
	}
}

func TestMigrateForwardV1toV2PreservesData(t *testing.T) {
	s := openTestStore(t)
	// pre-existing data at the (simulated) v1 state
	if _, err := s.conn.ExecContext(s.ctx, `INSERT INTO bank(bank_id,created_at,updated_at) VALUES('b',0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.ExecContext(s.ctx, `INSERT INTO memory(bank_id,content,created_at) VALUES('b','remember me',0)`); err != nil {
		t.Fatal(err)
	}
	// regress to v1: drop the v2 artifact and set version back
	if _, err := s.conn.ExecContext(s.ctx, `DROP TABLE edge`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.ExecContext(s.ctx,
		`DELETE FROM schema_version; INSERT INTO schema_version(version,min_binary,updated_at) VALUES(1,'',0)`); err != nil {
		t.Fatal(err)
	}

	applied, err := s.migrate("test")
	if err != nil || applied != 1 {
		t.Fatalf("forward migrate: applied=%d err=%v, want 1/nil", applied, err)
	}
	if v := s.SchemaVersion(); v != 2 {
		t.Fatalf("after forward migrate, version=%d want 2", v)
	}
	if _, err := s.conn.ExecContext(s.ctx, `SELECT count(*) FROM edge`); err != nil {
		t.Fatalf("edge not recreated: %v", err)
	}
	var n int
	if err := s.conn.QueryRowContext(s.ctx, `SELECT count(*) FROM memory`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("pre-existing data lost: count=%d err=%v", n, err)
	}
}
