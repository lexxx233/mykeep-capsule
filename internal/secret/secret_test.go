package secret

import (
	"bytes"
	"errors"
	"testing"
)

// testPassword is reused across cases; a non-trivial byte slice.
var testPassword = []byte("correct horse battery staple")

// TestNewEnvelopeUnwrapRoundTrip verifies that the DEK produced by NewEnvelope is
// exactly the DEK recovered by Unwrap with the same password.
func TestNewEnvelopeUnwrapRoundTrip(t *testing.T) {
	env, dek, err := NewEnvelope(testPassword)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	if len(dek) != int(DefaultKDF().KeyLen) {
		t.Fatalf("DEK length = %d, want %d", len(dek), DefaultKDF().KeyLen)
	}
	if env.Version != 1 {
		t.Errorf("Version = %d, want 1", env.Version)
	}
	if len(env.WrappedDEK.Ciphertext) == 0 {
		t.Error("WrappedDEK.Ciphertext is empty")
	}
	if len(env.WrappedDEK.Nonce) == 0 {
		t.Error("WrappedDEK.Nonce is empty")
	}

	got, err := env.Unwrap(testPassword)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Errorf("Unwrap DEK = %x, want %x", got, dek)
	}

	// The wrapped DEK must not be the cleartext DEK sitting in the envelope.
	if bytes.Contains(env.WrappedDEK.Ciphertext, dek) {
		t.Error("WrappedDEK.Ciphertext contains cleartext DEK")
	}
}

// TestUnwrapWrongPassphrase verifies that any password other than the original
// fails the GCM tag and surfaces as ErrWrongPassphrase.
func TestUnwrapWrongPassphrase(t *testing.T) {
	env, _, err := NewEnvelope(testPassword)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	cases := []struct {
		name string
		pw   []byte
	}{
		{"different password", []byte("incorrect horse battery staple")},
		{"empty password", []byte("")},
		{"nil password", nil},
		{"prefix of correct", testPassword[:len(testPassword)-1]},
		{"one byte off", append(append([]byte{}, testPassword[:len(testPassword)-1]...), 'X')},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dek, err := env.Unwrap(tc.pw)
			if !errors.Is(err, ErrWrongPassphrase) {
				t.Errorf("Unwrap err = %v, want ErrWrongPassphrase", err)
			}
			if dek != nil {
				t.Errorf("Unwrap returned non-nil DEK %x on failure", dek)
			}
		})
	}
}

// TestUnwrapTamperedCiphertext verifies that flipping any byte of the wrapped DEK
// ciphertext is detected by the AEAD tag and reported as ErrWrongPassphrase even
// when the correct password is supplied.
func TestUnwrapTamperedCiphertext(t *testing.T) {
	env, _, err := NewEnvelope(testPassword)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	// Flip the first byte of the ciphertext.
	tamperedCipher := env
	tamperedCipher.WrappedDEK.Ciphertext = append([]byte(nil), env.WrappedDEK.Ciphertext...)
	tamperedCipher.WrappedDEK.Ciphertext[0] ^= 0xFF
	if _, err := tamperedCipher.Unwrap(testPassword); !errors.Is(err, ErrWrongPassphrase) {
		t.Errorf("tampered ciphertext: Unwrap err = %v, want ErrWrongPassphrase", err)
	}

	// Flip the last byte of the ciphertext (often part of the GCM tag region).
	tamperedTag := env
	tamperedTag.WrappedDEK.Ciphertext = append([]byte(nil), env.WrappedDEK.Ciphertext...)
	last := len(tamperedTag.WrappedDEK.Ciphertext) - 1
	tamperedTag.WrappedDEK.Ciphertext[last] ^= 0x01
	if _, err := tamperedTag.Unwrap(testPassword); !errors.Is(err, ErrWrongPassphrase) {
		t.Errorf("tampered tag: Unwrap err = %v, want ErrWrongPassphrase", err)
	}

	// Tamper the nonce.
	tamperedNonce := env
	tamperedNonce.WrappedDEK.Nonce = append([]byte(nil), env.WrappedDEK.Nonce...)
	tamperedNonce.WrappedDEK.Nonce[0] ^= 0xFF
	if _, err := tamperedNonce.Unwrap(testPassword); !errors.Is(err, ErrWrongPassphrase) {
		t.Errorf("tampered nonce: Unwrap err = %v, want ErrWrongPassphrase", err)
	}
}

// TestUnwrapTamperedAAD verifies that altering KDF params / version (bound as AAD)
// breaks the open, since the AAD no longer matches what was sealed.
func TestUnwrapTamperedAAD(t *testing.T) {
	env, _, err := NewEnvelope(testPassword)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(e *Envelope)
	}{
		{"version", func(e *Envelope) { e.Version = 2 }},
		{"algo", func(e *Envelope) { e.KDF.Algo = "scrypt" }},
		{"time", func(e *Envelope) { e.KDF.Time = e.KDF.Time + 1 }},
		{"key_len", func(e *Envelope) { e.KDF.KeyLen = e.KDF.KeyLen + 1 }},
		{"salt byte", func(e *Envelope) {
			e.KDF.Salt = append([]byte(nil), e.KDF.Salt...)
			e.KDF.Salt[0] ^= 0xFF
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Copy so the salt change does not alter derived KEK in a way that hides
			// the AAD effect; for non-salt fields the KEK is unchanged and only AAD differs.
			tampered := env
			tampered.KDF.Salt = append([]byte(nil), env.KDF.Salt...)
			tc.mutate(&tampered)
			if _, err := tampered.Unwrap(testPassword); !errors.Is(err, ErrWrongPassphrase) {
				t.Errorf("Unwrap after mutating %s: err = %v, want ErrWrongPassphrase", tc.name, err)
			}
		})
	}
}

// TestNewEnvelopeUniqueSaltAndNonce verifies that distinct NewEnvelope calls use
// fresh randomness: different salts, different nonces, and different wrapped DEKs.
func TestNewEnvelopeUniqueSaltAndNonce(t *testing.T) {
	const n = 8
	salts := make(map[string]bool, n)
	nonces := make(map[string]bool, n)
	ciphers := make(map[string]bool, n)
	deks := make(map[string]bool, n)

	for i := 0; i < n; i++ {
		env, dek, err := NewEnvelope(testPassword)
		if err != nil {
			t.Fatalf("NewEnvelope #%d: %v", i, err)
		}
		s := string(env.KDF.Salt)
		no := string(env.WrappedDEK.Nonce)
		c := string(env.WrappedDEK.Ciphertext)
		d := string(dek)
		if salts[s] {
			t.Errorf("duplicate salt at iteration %d", i)
		}
		if nonces[no] {
			t.Errorf("duplicate nonce at iteration %d", i)
		}
		if ciphers[c] {
			t.Errorf("duplicate wrapped DEK ciphertext at iteration %d", i)
		}
		if deks[d] {
			t.Errorf("duplicate DEK at iteration %d", i)
		}
		salts[s] = true
		nonces[no] = true
		ciphers[c] = true
		deks[d] = true
	}
}

// TestSealOpenBlobRoundTrip verifies SealBlob/OpenBlob round-trip for a range of
// payload sizes, including empty.
func TestSealOpenBlobRoundTrip(t *testing.T) {
	_, dek, err := NewEnvelope(testPassword)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	cases := []struct {
		name      string
		plaintext []byte
	}{
		{"empty", []byte{}},
		{"small", []byte("hello joyvend")},
		{"binary", []byte{0x00, 0xFF, 0x10, 0x00, 0x42}},
		{"large", bytes.Repeat([]byte("db-page"), 4096)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sealed, err := SealBlob(dek, tc.plaintext)
			if err != nil {
				t.Fatalf("SealBlob: %v", err)
			}
			// Ciphertext must differ from plaintext (for non-empty content).
			if len(tc.plaintext) > 0 && bytes.Equal(sealed.Ciphertext, tc.plaintext) {
				t.Error("ciphertext equals plaintext")
			}
			got, err := OpenBlob(dek, sealed)
			if err != nil {
				t.Fatalf("OpenBlob: %v", err)
			}
			if !bytes.Equal(got, tc.plaintext) {
				t.Errorf("OpenBlob = %x, want %x", got, tc.plaintext)
			}
		})
	}
}

// TestSealBlobUniqueNonce verifies sealing the same plaintext twice produces
// different ciphertexts (fresh nonce each time).
func TestSealBlobUniqueNonce(t *testing.T) {
	_, dek, err := NewEnvelope(testPassword)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	pt := []byte("identical plaintext")
	a, err := SealBlob(dek, pt)
	if err != nil {
		t.Fatalf("SealBlob a: %v", err)
	}
	b, err := SealBlob(dek, pt)
	if err != nil {
		t.Fatalf("SealBlob b: %v", err)
	}
	if bytes.Equal(a.Nonce, b.Nonce) {
		t.Error("two SealBlob calls reused the same nonce")
	}
	if bytes.Equal(a.Ciphertext, b.Ciphertext) {
		t.Error("two SealBlob calls produced identical ciphertext")
	}
}

// TestOpenBlobWrongDEK verifies a blob sealed under one DEK cannot be opened with
// a different DEK (the AEAD tag fails).
func TestOpenBlobWrongDEK(t *testing.T) {
	_, dek1, err := NewEnvelope(testPassword)
	if err != nil {
		t.Fatalf("NewEnvelope 1: %v", err)
	}
	_, dek2, err := NewEnvelope(testPassword)
	if err != nil {
		t.Fatalf("NewEnvelope 2: %v", err)
	}
	if bytes.Equal(dek1, dek2) {
		t.Fatal("two random DEKs collided; randomness broken")
	}

	sealed, err := SealBlob(dek1, []byte("secret database bytes"))
	if err != nil {
		t.Fatalf("SealBlob: %v", err)
	}
	if _, err := OpenBlob(dek2, sealed); err == nil {
		t.Error("OpenBlob with wrong DEK succeeded, want failure")
	}
}

// TestOpenBlobBadNonceLength verifies the explicit nonce-length guard in open().
func TestOpenBlobBadNonceLength(t *testing.T) {
	_, dek, err := NewEnvelope(testPassword)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	sealed, err := SealBlob(dek, []byte("payload"))
	if err != nil {
		t.Fatalf("SealBlob: %v", err)
	}
	// Truncate the nonce.
	sealed.Nonce = sealed.Nonce[:len(sealed.Nonce)-1]
	if _, err := OpenBlob(dek, sealed); err == nil {
		t.Error("OpenBlob with short nonce succeeded, want error")
	}
}

// TestKeyStoreUseAndZero verifies Use exposes the DEK and Zero locks the store.
func TestKeyStoreUseAndZero(t *testing.T) {
	_, dek, err := NewEnvelope(testPassword)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	want := append([]byte(nil), dek...)

	ks := NewKeyStore(dek)

	var seen []byte
	if err := ks.Use(func(d []byte) error {
		seen = append([]byte(nil), d...)
		return nil
	}); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if !bytes.Equal(seen, want) {
		t.Errorf("Use saw DEK %x, want %x", seen, want)
	}

	// Use propagates the fn error.
	sentinel := errors.New("sentinel")
	if err := ks.Use(func(d []byte) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Errorf("Use error = %v, want sentinel", err)
	}

	// After Zero, the underlying DEK bytes are wiped and Use is locked.
	ks.Zero()
	for i, b := range dek {
		if b != 0 {
			t.Errorf("after Zero, dek[%d] = %d, want 0", i, b)
			break
		}
	}
	err = ks.Use(func(d []byte) error {
		t.Error("fn should not run on a locked keystore")
		return nil
	})
	if err == nil {
		t.Error("Use on locked keystore returned nil error, want locked error")
	}

	// Zero is idempotent / safe to call again on a locked store.
	ks.Zero()
}

// TestKDFDeterminism verifies that the SAME password and the SAME envelope
// (with its pinned KDF params) re-derive the SAME DEK across repeated Unwraps.
// This guards the portability requirement: threads/time/memory are read from the
// stored envelope, not from runtime.NumCPU.
func TestKDFDeterminism(t *testing.T) {
	env, dek, err := NewEnvelope(testPassword)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	for i := 0; i < 3; i++ {
		got, err := env.Unwrap(testPassword)
		if err != nil {
			t.Fatalf("Unwrap #%d: %v", i, err)
		}
		if !bytes.Equal(got, dek) {
			t.Fatalf("Unwrap #%d DEK = %x, want %x", i, got, dek)
		}
	}

	// deriveKEK must be a pure function of (password, params): same inputs => same KEK.
	k1 := deriveKEK(testPassword, env.KDF)
	k2 := deriveKEK(testPassword, env.KDF)
	if !bytes.Equal(k1, k2) {
		t.Errorf("deriveKEK not deterministic: %x vs %x", k1, k2)
	}

	// Changing only Threads (pinned param) changes the derived KEK, proving the
	// stored value drives derivation rather than the host CPU count.
	params2 := env.KDF
	params2.Threads = env.KDF.Threads + 1
	k3 := deriveKEK(testPassword, params2)
	if bytes.Equal(k1, k3) {
		t.Error("deriveKEK ignored Threads param; pinning would not affect derivation")
	}
}

// TestDefaultKDF sanity-checks the calibrated defaults match the documented design.
func TestDefaultKDF(t *testing.T) {
	p := DefaultKDF()
	if p.Algo != "argon2id" {
		t.Errorf("Algo = %q, want argon2id", p.Algo)
	}
	if p.Memory != 256*1024 {
		t.Errorf("Memory = %d KiB, want %d", p.Memory, 256*1024)
	}
	if p.KeyLen != 32 {
		t.Errorf("KeyLen = %d, want 32", p.KeyLen)
	}
	if p.Threads == 0 {
		t.Error("Threads = 0, want pinned non-zero value")
	}
	if p.Time == 0 {
		t.Error("Time = 0, want non-zero iterations")
	}
	if len(p.Salt) != 0 {
		t.Errorf("DefaultKDF Salt should be empty until NewEnvelope fills it, got %d bytes", len(p.Salt))
	}
}

// TestConstantTimeEqual verifies the bearer-token comparison helper.
func TestConstantTimeEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b []byte
		want bool
	}{
		{"equal", []byte("token-abc"), []byte("token-abc"), true},
		{"different", []byte("token-abc"), []byte("token-xyz"), false},
		{"different length", []byte("short"), []byte("longer-token"), false},
		{"both empty", []byte{}, []byte{}, true},
		{"empty vs nonempty", []byte{}, []byte("x"), false},
		{"nil vs nil", nil, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConstantTimeEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("ConstantTimeEqual(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
