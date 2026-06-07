// Package app assembles the joyvend runtime (encrypted store + local embedder +
// ingest/recall pipelines) from a password, shared by the CLI `serve` flow and the
// GUI. It centralizes first-launch config creation and unlock.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"joyvend.io/internal/config"
	"joyvend.io/internal/embed"
	"joyvend.io/internal/ingest"
	"joyvend.io/internal/paths"
	"joyvend.io/internal/retrieval"
	"joyvend.io/internal/secret"
	"joyvend.io/internal/store"
)

// Runtime is the assembled, unlocked joyvend core.
type Runtime struct {
	Config   *config.Config
	Store    *store.Store
	Embedder embed.Embedder
	Ingest   *ingest.Ingestor
	Recall   *retrieval.Recaller
	keys     *secret.KeyStore
}

func (r *Runtime) EmbedderName() string { return r.Embedder.Name() }

// Close flushes + tears down the store and zeroizes the key.
func (r *Runtime) Close() error {
	err := r.Store.Close()
	r.keys.Zero()
	return err
}

// Open builds the runtime from a password. On first launch it creates the envelope
// + config; otherwise it loads the config and unwraps the DEK (returning
// secret.ErrWrongPassphrase on a bad password). The password slice is the caller's
// to wipe.
func Open(ctx context.Context, layout paths.Layout, password []byte, firstLaunch bool, version string) (*Runtime, error) {
	var (
		cfg *config.Config
		dek []byte
	)
	if firstLaunch {
		emb := BuildEmbedder(ctx, layout, nil)
		env, d, err := secret.NewEnvelope(password)
		if err != nil {
			return nil, err
		}
		c := config.Default()
		c.Embedding.Model = embed.DefaultModel
		c.Embedding.Dim = emb.Dim()
		c.Secret = env
		if err := config.Save(layout.ConfigPath(), &c); err != nil {
			return nil, err
		}
		cfg, dek = &c, d
	} else {
		c, err := config.Load(layout.ConfigPath())
		if err != nil {
			return nil, err
		}
		d, err := c.Secret.Unwrap(password)
		if err != nil {
			return nil, err // secret.ErrWrongPassphrase
		}
		cfg, dek = c, d
	}

	keys := secret.NewKeyStore(dek)
	emb := BuildEmbedder(ctx, layout, cfg)
	st, err := store.OpenEncrypted(layout.DBPath(), keys, store.Options{
		FlushIdle:      time.Duration(cfg.Runtime.FlushIdleMs) * time.Millisecond,
		FlushMaxWrites: cfg.Runtime.FlushMaxWrites,
		Version:        version,
		VectorDim:      cfg.Embedding.Dim,
	})
	if err != nil {
		keys.Zero()
		return nil, err
	}
	_ = st.Flush() // materialize the blob on a fresh DB

	return &Runtime{
		Config:   cfg,
		Store:    st,
		Embedder: emb,
		Ingest:   ingest.New(st, emb, cfg.Runtime.SoftCapMB),
		Recall:   retrieval.New(st, emb),
		keys:     keys,
	}, nil
}

// BuildEmbedder loads the local CPU model; on failure it falls back to the hash
// embedder so the system still runs (PLAN §9.2).
func BuildEmbedder(ctx context.Context, layout paths.Layout, cfg *config.Config) embed.Embedder {
	model := embed.DefaultModel
	dim := 384
	if cfg != nil {
		if cfg.Embedding.Model != "" {
			model = cfg.Embedding.Model
		}
		if cfg.Embedding.Dim > 0 {
			dim = cfg.Embedding.Dim
		}
	}
	le, err := embed.NewLocalEmbedder(ctx, filepath.Join(layout.DataDir, "models"), model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: local model unavailable (%v); using hash-fallback embedder\n", err)
		return embed.NewHashEmbedder(dim)
	}
	return le
}
