// Package store is joyvend's encrypted-at-rest SQLite memory store (PLAN §5, §11.6).
//
// The live DB is an in-RAM SQLite database (modernc, pure-Go). At unlock the whole
// DB is decrypted from joyvend.db.enc and Deserialize'd into RAM; writes hit RAM and
// a debounced flush re-seals the whole blob (D19). No plaintext DB touches the stick.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"joyvend.io/internal/secret"
)

// serializer is implemented by modernc's *conn (exported methods on an unexported
// type), reached via sql.Conn.Raw (proven in PLAN §11.6.1).
type serializer interface {
	Serialize() ([]byte, error)
	Deserialize(buf []byte) error
}

// Options tune the debounced-flush cadence (D19) and migration metadata.
type Options struct {
	FlushIdle      time.Duration // re-seal after this much write inactivity
	FlushMaxWrites int           // ...or once this many unflushed writes accrue
	Version        string        // binary version, recorded as schema_version.min_binary
	VectorDim      int           // embedding dim, used to create the vec0 index (D1)
}

func defaultOptions() Options { return Options{FlushIdle: 4 * time.Second, FlushMaxWrites: 200} }

// Store is the single-writer, in-RAM, encrypted SQLite store.
type Store struct {
	blobPath string
	keys     *secret.KeyStore
	opt      Options
	ctx      context.Context

	mu           sync.Mutex
	db           *sql.DB
	conn         *sql.Conn
	dirty        bool
	writeCount   int
	writeGen     uint64 // bumped per write; tags each snapshot (under s.mu)
	flushTimer   *time.Timer
	closed       bool
	lastFlushErr error
	lock         *fileLock

	// flushMu single-flights the slow seal+disk-write (off s.mu) so a USB write
	// never blocks reads/writes; persistedGen keeps the on-disk blob monotonic.
	flushMu      sync.Mutex
	persistedGen uint64

	vecAvailable bool // vec0 KNN backend present (D1)
	vecCreated   bool // vec_idx table built
	vecDim       int
}

// OpenEncrypted opens (or initializes) the encrypted store. If blobPath exists it
// is decrypted with the keystore's DEK and deserialized into RAM; otherwise a fresh
// schema is created. The caller owns the keystore lifetime.
func OpenEncrypted(blobPath string, keys *secret.KeyStore, opt Options) (*Store, error) {
	if opt.FlushIdle == 0 {
		opt = defaultOptions()
	}
	ctx := context.Background()
	lock, err := acquireLock(blobPath + ".lock")
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		lock.release()
		return nil, err
	}
	db.SetMaxOpenConns(1) // single in-RAM connection holds the whole DB
	conn, err := db.Conn(ctx)
	if err != nil {
		lock.release()
		return nil, err
	}
	s := &Store{blobPath: blobPath, keys: keys, opt: opt, ctx: ctx, db: db, conn: conn, lock: lock}

	if _, err := os.Stat(blobPath); err == nil {
		if err := s.loadFromBlob(); err != nil {
			s.teardown()
			return nil, err
		}
	}
	if err := s.applyPragmas(); err != nil {
		s.teardown()
		return nil, err
	}
	// Bring the schema up to date (creates it on a fresh DB; forward-only;
	// fail-closed if the DB is newer than this binary).
	applied, err := s.migrate(opt.Version)
	if err != nil {
		s.teardown()
		return nil, err
	}
	if applied > 0 {
		s.dirty = true // a fresh/upgraded DB must be flushed
	}
	s.setupVec0(opt.VectorDim) // probe + build the vec0 KNN index (D1); brute-force otherwise
	return s, nil
}

func (s *Store) teardown() {
	_ = s.conn.Close()
	_ = s.db.Close()
	s.lock.release()
}

// DBSizeBytes returns the size of the on-disk encrypted blob (0 if not yet flushed).
// Used as the soft-cap proxy (PLAN §16).
func (s *Store) DBSizeBytes() int64 {
	fi, err := os.Stat(s.blobPath)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func (s *Store) applyPragmas() error {
	for _, p := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA temp_store = MEMORY",
		"PRAGMA synchronous = OFF", // safe: durability is the periodic encrypted re-seal, not the RAM DB
	} {
		if _, err := s.conn.ExecContext(s.ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadFromBlob() error {
	blob, err := os.ReadFile(s.blobPath)
	if err != nil {
		return err
	}
	var sealed secret.Sealed
	if err := decodeSealed(blob, &sealed); err != nil {
		return err
	}
	var plain []byte
	if err := s.keys.Use(func(dek []byte) error {
		p, err := secret.OpenBlob(dek, sealed)
		if err != nil {
			return err
		}
		plain = p
		return nil
	}); err != nil {
		return fmt.Errorf("decrypt db: %w", err)
	}
	return s.conn.Raw(func(dc any) error {
		ser, ok := dc.(serializer)
		if !ok {
			return errors.New("driver does not support Deserialize")
		}
		return ser.Deserialize(plain)
	})
}

// --- write-tracking + debounced flush (D19) ---

// markDirty must be called under s.mu after a write; it arms the debounced flush
// and forces an immediate flush once FlushMaxWrites accrue. The idle timer is always
// (re)armed so a failed or forced flush is retried on idle rather than lost.
func (s *Store) markDirty() {
	s.dirty = true
	s.writeCount++
	s.writeGen++
	if s.flushTimer != nil {
		s.flushTimer.Stop()
	}
	s.flushTimer = time.AfterFunc(s.opt.FlushIdle, s.flushAsync)
	if s.writeCount >= s.opt.FlushMaxWrites {
		go s.flushAsync()
	}
}

// flushAsync re-seals and logs (does not swallow) any error; memories stay in RAM
// and the idle timer will retry (PLAN §11.6, D19).
func (s *Store) flushAsync() {
	if err := s.reseal(); err != nil {
		log.Printf("joyvend: re-seal failed (memories held in RAM, will retry): %v", err)
	}
}

// Flush synchronously re-seals the whole DB to the encrypted blob if dirty (blocks
// until persisted). Used on shutdown.
func (s *Store) Flush() error { return s.reseal() }

// reseal snapshots the in-RAM DB under s.mu (a fast memcpy), then encrypts and
// writes the snapshot WITHOUT holding s.mu — so the slow USB write never blocks
// reads/writes. flushMu single-flights the disk write, and a monotonic generation
// guard (persistedGen) ensures a stale snapshot can never overwrite a newer blob.
// On failure the store stays dirty and the idle timer retries; the error is
// surfaced via /v1/health.
func (s *Store) reseal() error {
	s.mu.Lock()
	if !s.dirty || s.closed {
		s.mu.Unlock()
		return nil
	}
	raw, err := s.serializeLocked()
	if err != nil {
		s.lastFlushErr = err
		s.mu.Unlock()
		return err
	}
	gen := s.writeGen
	s.dirty = false // optimistic: this snapshot covers writes up to `gen`
	s.writeCount = 0
	s.mu.Unlock()

	// Slow part (AES seal + USB write + fsync) runs off s.mu; flushMu serializes it.
	s.flushMu.Lock()
	if gen <= s.persistedGen { // a newer snapshot already won — discard this one
		s.flushMu.Unlock()
		return nil
	}
	werr := s.writeSealed(raw)
	if werr == nil {
		s.persistedGen = gen
	}
	s.flushMu.Unlock()

	s.mu.Lock()
	s.lastFlushErr = werr
	if werr != nil {
		s.dirty = true // failed → stay dirty so the idle timer retries
	}
	s.mu.Unlock()
	return werr
}

// serializeLocked snapshots the whole in-RAM DB to a fresh byte slice. modernc's
// Serialize returns a copy, so the result is safe to encrypt/write after s.mu is
// released. Caller holds s.mu.
func (s *Store) serializeLocked() ([]byte, error) {
	var raw []byte
	err := s.conn.Raw(func(dc any) error {
		ser, ok := dc.(serializer)
		if !ok {
			return errors.New("driver does not support Serialize")
		}
		b, serr := ser.Serialize()
		raw = b
		return serr
	})
	return raw, err
}

// writeSealed encrypts raw with the DEK and atomically writes the encrypted blob.
func (s *Store) writeSealed(raw []byte) error {
	var sealed secret.Sealed
	if err := s.keys.Use(func(dek []byte) error {
		sl, err := secret.SealBlob(dek, raw)
		sealed = sl
		return err
	}); err != nil {
		return err
	}
	return atomicWrite(s.blobPath, encodeSealed(sealed))
}

// LastFlushErr returns the most recent re-seal error (nil if the last flush
// succeeded), so the server can surface persistence failures via /v1/health.
func (s *Store) LastFlushErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastFlushErr
}

// EventAts returns each memory's temporal anchor (nil when timeless), for the
// recency rerank (PLAN §5.4).
func (s *Store) EventAts(ids []int64) map[int64]*int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int64]*int64, len(ids))
	for _, id := range ids {
		var ev sql.NullInt64
		if err := s.conn.QueryRowContext(s.ctx, `SELECT event_at FROM memory WHERE id=?`, id).Scan(&ev); err == nil {
			if ev.Valid {
				v := ev.Int64
				out[id] = &v
			} else {
				out[id] = nil
			}
		}
	}
	return out
}

// Close flushes and tears down the store.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if s.flushTimer != nil {
		s.flushTimer.Stop()
	}
	s.mu.Unlock()

	// Final synchronous re-seal (blocks until persisted) so a clean shutdown is
	// lossless; reseal's single-flight serializes it with any in-flight async flush.
	err := s.reseal()

	s.mu.Lock()
	s.closed = true
	_ = s.conn.Close()
	_ = s.db.Close()
	s.lock.release()
	s.mu.Unlock()
	return err
}

// atomicWrite writes via temp file + fsync + rename so a crash never corrupts the blob.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".joyvend-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil { // best-effort dir fsync
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
