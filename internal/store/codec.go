package store

import (
	"errors"

	"mykeep.ai/internal/secret"
)

// nonceLen is the AES-GCM standard nonce size.
const nonceLen = 12

// encodeSealed lays the on-disk blob out as [nonce(12)][ciphertext].
func encodeSealed(s secret.Sealed) []byte {
	out := make([]byte, 0, len(s.Nonce)+len(s.Ciphertext))
	out = append(out, s.Nonce...)
	return append(out, s.Ciphertext...)
}

func decodeSealed(blob []byte, s *secret.Sealed) error {
	if len(blob) < nonceLen {
		return errors.New("mykeep: encrypted blob too short")
	}
	s.Nonce = blob[:nonceLen]
	s.Ciphertext = blob[nonceLen:]
	return nil
}
