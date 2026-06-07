package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"joyvend.io/internal/secret"
)

// TestDefault verifies Default() populates the documented field values.
func TestDefault(t *testing.T) {
	c := Default()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"schema_version", c.SchemaVersion, 1},
		{"embedding_fallback", c.Embedding.Fallback, "hash"},
		{"server_addr", c.Server.Addr, "127.0.0.1:8765"},
		{"runtime_soft_cap_mb", c.Runtime.SoftCapMB, 450},
		{"runtime_hard_warn_mb", c.Runtime.HardWarnMB, 1024},
		{"runtime_flush_idle_ms", c.Runtime.FlushIdleMs, 4000},
		{"runtime_flush_max_writes", c.Runtime.FlushMaxWrites, 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("Default() %s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}

	// Server defaults: loopback-only, no token required.
	if c.Server.RequireToken {
		t.Errorf("Default() Server.RequireToken = true, want false")
	}
	if c.Server.AllowNonLoopback {
		t.Errorf("Default() Server.AllowNonLoopback = true, want false")
	}

	// Per the doc comment, Model + Dim are filled in by setup, not Default().
	if c.Embedding.Model != "" {
		t.Errorf("Default() Embedding.Model = %q, want empty (filled by setup)", c.Embedding.Model)
	}
	if c.Embedding.Dim != 0 {
		t.Errorf("Default() Embedding.Dim = %d, want 0 (filled by setup)", c.Embedding.Dim)
	}

	// Default() must not carry a populated secret envelope.
	if !reflect.DeepEqual(c.Secret, secret.Envelope{}) {
		t.Errorf("Default() Secret = %+v, want zero Envelope", c.Secret)
	}
}

// newConfigWithSecret returns a fully-populated config (every field non-zero) plus
// the freshly-built envelope, so round-trip tests can assert against known values.
func newConfigWithSecret(t *testing.T) (Config, secret.Envelope) {
	t.Helper()
	env, dek, err := secret.NewEnvelope([]byte("pw-correct-horse-battery-staple"))
	if err != nil {
		t.Fatalf("secret.NewEnvelope: %v", err)
	}
	if len(dek) == 0 {
		t.Fatalf("secret.NewEnvelope returned empty DEK")
	}
	// Sanity: the envelope we build is itself well-formed.
	if len(env.KDF.Salt) == 0 {
		t.Fatalf("freshly built envelope has empty KDF.Salt")
	}
	if len(env.WrappedDEK.Ciphertext) == 0 {
		t.Fatalf("freshly built envelope has empty WrappedDEK.Ciphertext")
	}

	c := Config{
		SchemaVersion: 7,
		Embedding:     EmbeddingConfig{Model: "bge-small-en-v1.5", Dim: 384, Fallback: "hash"},
		Server:        ServerConfig{Addr: "127.0.0.1:9999", RequireToken: true, AllowNonLoopback: true},
		Runtime:       RuntimeConfig{SoftCapMB: 450, HardWarnMB: 2048, FlushIdleMs: 1000, FlushMaxWrites: 50},
		Secret:        env,
	}
	return c, env
}

// TestSaveLoadRoundTrip writes a fully-populated config and reads it back,
// asserting every field — including the secret.Envelope — survives intact.
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "joyvend.config.json")

	want, env := newConfigWithSecret(t)

	if err := Save(path, &want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Whole-struct equality is the strongest round-trip assertion: it catches any
	// field that fails to marshal/unmarshal, including nested byte slices.
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("round-trip mismatch:\n got = %+v\nwant = %+v", *got, want)
	}

	// Spell out the field-by-field checks too, so a failure points at the culprit.
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"SchemaVersion", got.SchemaVersion, want.SchemaVersion},
		{"Embedding.Model", got.Embedding.Model, want.Embedding.Model},
		{"Embedding.Dim", got.Embedding.Dim, want.Embedding.Dim},
		{"Embedding.Fallback", got.Embedding.Fallback, want.Embedding.Fallback},
		{"Server.Addr", got.Server.Addr, want.Server.Addr},
		{"Server.RequireToken", got.Server.RequireToken, want.Server.RequireToken},
		{"Server.AllowNonLoopback", got.Server.AllowNonLoopback, want.Server.AllowNonLoopback},
		{"Runtime.SoftCapMB", got.Runtime.SoftCapMB, want.Runtime.SoftCapMB},
		{"Runtime.HardWarnMB", got.Runtime.HardWarnMB, want.Runtime.HardWarnMB},
		{"Runtime.FlushIdleMs", got.Runtime.FlushIdleMs, want.Runtime.FlushIdleMs},
		{"Runtime.FlushMaxWrites", got.Runtime.FlushMaxWrites, want.Runtime.FlushMaxWrites},
		{"Secret.Version", got.Secret.Version, want.Secret.Version},
		{"Secret.KDF.Algo", got.Secret.KDF.Algo, want.Secret.KDF.Algo},
		{"Secret.KDF.Time", got.Secret.KDF.Time, want.Secret.KDF.Time},
		{"Secret.KDF.Memory", got.Secret.KDF.Memory, want.Secret.KDF.Memory},
		{"Secret.KDF.Threads", got.Secret.KDF.Threads, want.Secret.KDF.Threads},
		{"Secret.KDF.KeyLen", got.Secret.KDF.KeyLen, want.Secret.KDF.KeyLen},
	}
	for _, tt := range checks {
		if tt.got != tt.want {
			t.Errorf("round-trip %s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}

	// The byte-slice fields of the envelope must survive exactly.
	if !bytes.Equal(got.Secret.KDF.Salt, env.KDF.Salt) {
		t.Errorf("Secret.KDF.Salt = %x, want %x", got.Secret.KDF.Salt, env.KDF.Salt)
	}
	if len(got.Secret.KDF.Salt) == 0 {
		t.Errorf("Secret.KDF.Salt is empty after round-trip")
	}
	if !bytes.Equal(got.Secret.WrappedDEK.Nonce, env.WrappedDEK.Nonce) {
		t.Errorf("Secret.WrappedDEK.Nonce = %x, want %x", got.Secret.WrappedDEK.Nonce, env.WrappedDEK.Nonce)
	}
	if !bytes.Equal(got.Secret.WrappedDEK.Ciphertext, env.WrappedDEK.Ciphertext) {
		t.Errorf("Secret.WrappedDEK.Ciphertext = %x, want %x", got.Secret.WrappedDEK.Ciphertext, env.WrappedDEK.Ciphertext)
	}
	if len(got.Secret.WrappedDEK.Ciphertext) == 0 {
		t.Errorf("Secret.WrappedDEK.Ciphertext is empty after round-trip")
	}
}

// TestRoundTripEnvelopeStillUnwraps proves the secret survived round-trip in a
// usable form: the loaded envelope must unwrap to the SAME DEK under the
// original password and reject the wrong one. This is the real-world guarantee.
func TestRoundTripEnvelopeStillUnwraps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "joyvend.config.json")

	password := []byte("pw-a-very-secret-passphrase")
	env, dek, err := secret.NewEnvelope(password)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	c := Default()
	c.Secret = env
	if err := Save(path, &c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	gotDEK, err := got.Secret.Unwrap(password)
	if err != nil {
		t.Fatalf("Unwrap after round-trip: %v", err)
	}
	if !bytes.Equal(gotDEK, dek) {
		t.Errorf("unwrapped DEK after round-trip = %x, want %x", gotDEK, dek)
	}

	if _, err := got.Secret.Unwrap([]byte("wrong-passphrase")); err == nil {
		t.Errorf("Unwrap with wrong passphrase succeeded, want failure")
	}
}

// TestSaveNoLeftoverTmp asserts Save leaves only the final file behind — no
// stray ".tmp" temp file from the atomic temp+rename dance.
func TestSaveNoLeftoverTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "joyvend.config.json")

	c, _ := newConfigWithSecret(t)

	// Save twice to ensure neither the create nor the rename leaves residue.
	for i := 0; i < 2; i++ {
		if err := Save(path, &c); err != nil {
			t.Fatalf("Save #%d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover .tmp file in dir: %q", e.Name())
		}
		if strings.HasPrefix(e.Name(), ".joyvend-cfg-") {
			t.Errorf("leftover temp prefix file in dir: %q", e.Name())
		}
	}
	if len(names) != 1 || names[0] != "joyvend.config.json" {
		t.Errorf("dir contents = %v, want exactly [joyvend.config.json]", names)
	}
}

// TestSavePermissions verifies the file is written 0600 (secret ciphertext lives
// inside, so the file should not be world/group readable).
func TestSavePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "joyvend.config.json")

	c, _ := newConfigWithSecret(t)
	if err := Save(path, &c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file perm = %o, want 600", perm)
	}
}

// TestSavedJSONStructure inspects the on-disk JSON. There is no API key by design,
// so we confirm the structure (expected keys present, secret block shaped right)
// and that no obvious cleartext secret strings leak. The DEK is only ever present
// as ciphertext, never as a readable field.
func TestSavedJSONStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "joyvend.config.json")

	c, _ := newConfigWithSecret(t)
	if err := Save(path, &c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// It must be valid JSON and unmarshal into the expected top-level shape.
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}
	for _, key := range []string{"schema_version", "embeddings", "server", "runtime", "secret"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("saved JSON missing top-level key %q", key)
		}
	}

	// The secret block must expose only kdf / wrapped_dek / version — and crucially
	// no field named like a plaintext key/password/dek/api_key.
	var sec map[string]json.RawMessage
	if err := json.Unmarshal(generic["secret"], &sec); err != nil {
		t.Fatalf("secret block is not valid JSON object: %v", err)
	}
	for _, key := range []string{"kdf", "wrapped_dek", "version"} {
		if _, ok := sec[key]; !ok {
			t.Errorf("secret block missing key %q", key)
		}
	}

	// No design API key — assert no such field name appears anywhere in the JSON,
	// and no obvious cleartext secret labels leak.
	lower := strings.ToLower(string(raw))
	for _, banned := range []string{"api_key", "apikey", "\"password\"", "\"dek\"", "plaintext", "passphrase"} {
		if strings.Contains(lower, banned) {
			t.Errorf("saved JSON unexpectedly contains sensitive token %q", banned)
		}
	}

	// The DEK never appears in cleartext: the raw 32-byte DEK is unrecoverable from
	// the file (only its AES-GCM ciphertext is stored). We can at least assert the
	// passphrase we used is nowhere in the bytes.
	if strings.Contains(string(raw), "pw-correct-horse-battery-staple") {
		t.Errorf("saved JSON leaked the passphrase in cleartext")
	}
}

// TestLoadErrors covers the failure paths of Load.
func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file", func(t *testing.T) {
		if _, err := Load(filepath.Join(dir, "does-not-exist.json")); err == nil {
			t.Errorf("Load(missing) = nil error, want error")
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		bad := filepath.Join(dir, "bad.json")
		if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, err := Load(bad); err == nil {
			t.Errorf("Load(malformed) = nil error, want error")
		}
	})
}

// TestSaveOverwritesAtomically confirms an existing config is replaced (not
// appended/corrupted) and remains loadable after a second Save with new values.
func TestSaveOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "joyvend.config.json")

	first := Default()
	if err := Save(path, &first); err != nil {
		t.Fatalf("Save first: %v", err)
	}

	second := Default()
	second.Server.Addr = "127.0.0.1:1234"
	second.Runtime.SoftCapMB = 900
	if err := Save(path, &second); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Server.Addr != "127.0.0.1:1234" {
		t.Errorf("after overwrite Server.Addr = %q, want 127.0.0.1:1234", got.Server.Addr)
	}
	if got.Runtime.SoftCapMB != 900 {
		t.Errorf("after overwrite Runtime.SoftCapMB = %d, want 900", got.Runtime.SoftCapMB)
	}
}
