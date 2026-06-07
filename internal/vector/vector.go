// Package vector handles embedding storage encoding and exact cosine math.
// Vectors are unit-normalized and packed little-endian into BLOBs (PLAN §5.3);
// the same BLOB feeds both the store's vec0 KNN index and the brute-force fallback.
package vector

import (
	"encoding/binary"
	"math"
)

// Normalize returns a unit-length (L2) copy of v. A zero vector is returned as-is.
func Normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	n := math.Sqrt(sum)
	out := make([]float32, len(v))
	if n == 0 {
		copy(out, v)
		return out
	}
	for i, x := range v {
		out[i] = float32(float64(x) / n)
	}
	return out
}

// Encode packs a float32 vector little-endian into dim*4 bytes (endian-safe via
// encoding/binary, never unsafe — PLAN §5.3).
func Encode(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(x))
	}
	return b
}

// Decode unpacks a little-endian BLOB back into a float32 vector.
func Decode(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// Dot returns the dot product; for unit-normalized inputs this is cosine similarity.
func Dot(a, b []float32) float64 {
	var s float64
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// Cosine returns the cosine similarity of two arbitrary (not necessarily unit) vectors.
func Cosine(a, b []float32) float64 {
	var dot, na, nb float64
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// Scored is a candidate id with its similarity, used by the brute-force scan.
type Scored struct {
	ID  int64
	Sim float64
}
