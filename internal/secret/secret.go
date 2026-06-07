// Package secret implements joyvend's at-rest encryption: an argon2id
// password-derived key-encryption-key (KEK) that wraps a random data-encryption
// key (DEK); the DEK seals the whole database blob (PLAN §11.3, §11.4, §11.6).
//
// With no API key in the design, the password's sole purpose is decrypting the DB.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"runtime"
	"sync"

	"golang.org/x/crypto/argon2"
)

// ErrWrongPassphrase is returned when KEK derivation + AEAD open fails — i.e. the
// supplied password is wrong or the envelope was tampered with.
var ErrWrongPassphrase = errors.New("joyvend: wrong passphrase")

// KDFParams are stored in plaintext in the config so the KEK can be re-derived.
// Threads is PINNED to the stored value (never runtime.NumCPU at derive time) so a
// stick moved between hosts with different core counts derives the same key.
type KDFParams struct {
	Algo    string `json:"algo"`    // "argon2id"
	Time    uint32 `json:"time"`    // iterations
	Memory  uint32 `json:"memory"`  // KiB
	Threads uint8  `json:"threads"` // PINNED
	KeyLen  uint32 `json:"key_len"`
	Salt    []byte `json:"salt"` // marshals as base64
}

// Sealed is one AES-256-GCM ciphertext with its nonce.
type Sealed struct {
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// Envelope is the secret block persisted in the config file. Only WrappedDEK is
// secret; KDF params + salt + nonces are plaintext (needed to derive/open).
type Envelope struct {
	KDF        KDFParams `json:"kdf"`
	WrappedDEK Sealed    `json:"wrapped_dek"`
	Version    int       `json:"version"`
}

// DefaultKDF returns calibrated argon2id params (PLAN §11.3): 256 MiB floor so any
// supported host can allocate it; threads pinned.
func DefaultKDF() KDFParams {
	return KDFParams{
		Algo:    "argon2id",
		Time:    4,
		Memory:  256 * 1024, // 256 MiB
		Threads: 4,
		KeyLen:  32,
	}
}

func deriveKEK(password []byte, p KDFParams) []byte {
	return argon2.IDKey(password, p.Salt, p.Time, p.Memory, p.Threads, p.KeyLen)
}

// aad binds the full-width KDF parameters + salt so any tampering surfaces as an
// auth failure rather than a silently different key (PLAN §11.2, D7). Numeric fields
// are serialized at full width — a single-byte cast would leave e.g. Memory=262144
// (and any multiple of 256) indistinguishable from 0.
func (e Envelope) aad() []byte {
	a := []byte(e.KDF.Algo)
	a = binary.BigEndian.AppendUint32(a, e.KDF.Time)
	a = binary.BigEndian.AppendUint32(a, e.KDF.Memory)
	a = append(a, e.KDF.Threads)
	a = binary.BigEndian.AppendUint32(a, e.KDF.KeyLen)
	a = binary.BigEndian.AppendUint32(a, uint32(e.Version))
	a = binary.BigEndian.AppendUint32(a, uint32(len(e.KDF.Salt)))
	return append(a, e.KDF.Salt...)
}

// NewEnvelope creates a fresh envelope for a new password: random salt + random
// DEK wrapped under the password-derived KEK. Returns the cleartext DEK for the
// caller to hold in a KeyStore.
func NewEnvelope(password []byte) (Envelope, []byte, error) {
	p := DefaultKDF()
	p.Salt = make([]byte, 16)
	if _, err := rand.Read(p.Salt); err != nil {
		return Envelope{}, nil, err
	}
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return Envelope{}, nil, err
	}
	env := Envelope{KDF: p, Version: 1}
	kek := deriveKEK(password, p)
	defer zero(kek)
	wrapped, err := seal(kek, dek, env.aad())
	if err != nil {
		return Envelope{}, nil, err
	}
	env.WrappedDEK = wrapped
	return env, dek, nil
}

// Unwrap re-derives the KEK from the password and recovers the DEK. A wrong
// password fails the GCM tag and returns ErrWrongPassphrase.
func (e Envelope) Unwrap(password []byte) ([]byte, error) {
	kek := deriveKEK(password, e.KDF)
	defer zero(kek)
	dek, err := open(kek, e.WrappedDEK, e.aad())
	if err != nil {
		return nil, ErrWrongPassphrase
	}
	return dek, nil
}

// SealBlob encrypts the whole-DB bytes under the DEK (PLAN §11.6).
func SealBlob(dek, plaintext []byte) (Sealed, error) { return seal(dek, plaintext, nil) }

// OpenBlob decrypts a sealed whole-DB blob under the DEK.
func OpenBlob(dek []byte, s Sealed) ([]byte, error) { return open(dek, s, nil) }

func seal(key, plaintext, aad []byte) (Sealed, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return Sealed{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Sealed{}, err
	}
	return Sealed{Nonce: nonce, Ciphertext: gcm.Seal(nil, nonce, plaintext, aad)}, nil
}

func open(key []byte, s Sealed, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(s.Nonce) != gcm.NonceSize() {
		return nil, errors.New("joyvend: bad nonce length")
	}
	return gcm.Open(nil, s.Nonce, s.Ciphertext, aad)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

// KeyStore holds the decrypted DEK in memory for the process lifetime.
type KeyStore struct {
	mu  sync.RWMutex
	dek []byte
}

func NewKeyStore(dek []byte) *KeyStore { return &KeyStore{dek: dek} }

// Use runs fn with the DEK held under the read lock; the slice must not escape.
func (k *KeyStore) Use(fn func(dek []byte) error) error {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.dek == nil {
		return errors.New("joyvend: keystore locked")
	}
	return fn(k.dek)
}

// Zero wipes the DEK (called on shutdown/lock).
func (k *KeyStore) Zero() {
	k.mu.Lock()
	defer k.mu.Unlock()
	zero(k.dek)
	k.dek = nil
}

// ConstantTimeEqual is used for optional bearer-token comparison (D20).
func ConstantTimeEqual(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
