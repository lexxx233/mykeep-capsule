// Package config loads and saves mykeep.config.json beside the binary. Only the
// secret envelope's ciphertext is sensitive; everything else is plaintext (PLAN §11.2).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"mykeep.ai/internal/secret"
)

type Config struct {
	SchemaVersion int             `json:"schema_version"`
	Embedding     EmbeddingConfig `json:"embeddings"`
	Server        ServerConfig    `json:"server"`
	Runtime       RuntimeConfig   `json:"runtime"`
	Secret        secret.Envelope `json:"secret"`
}

type EmbeddingConfig struct {
	Model    string `json:"model"`
	Dim      int    `json:"dim"`
	Fallback string `json:"fallback"` // "hash"
}

type ServerConfig struct {
	Addr             string `json:"addr"`
	RequireToken     bool   `json:"require_token"`
	AllowNonLoopback bool   `json:"allow_nonloopback"`
}

type RuntimeConfig struct {
	SoftCapMB      int `json:"soft_cap_mb"`
	HardWarnMB     int `json:"hard_warn_mb"`
	FlushIdleMs    int `json:"flush_idle_ms"`
	FlushMaxWrites int `json:"flush_max_writes"`
}

// Default returns a config with sensible defaults (model + dim filled in by setup).
func Default() Config {
	return Config{
		SchemaVersion: 1,
		Embedding:     EmbeddingConfig{Fallback: "hash"},
		Server:        ServerConfig{Addr: "127.0.0.1:8765"},
		Runtime:       RuntimeConfig{SoftCapMB: 450, HardWarnMB: 1024, FlushIdleMs: 4000, FlushMaxWrites: 200},
	}
}

// Load reads and parses the config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the config atomically (temp + fsync + rename, 0600).
func Save(path string, c *Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".mykeep-cfg-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
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
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}
