package vector

import (
	"bytes"
	"math"
	"testing"
)

// floatEq reports whether a and b are within tol of each other.
func floatEq(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

// l2 returns the Euclidean norm of v computed in float64.
func l2(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

func TestEncodeByteLayout(t *testing.T) {
	// 1.0f32 is 0x3f800000; little-endian on the wire => 00 00 80 3f.
	got := Encode([]float32{1.0})
	want := []byte{0x00, 0x00, 0x80, 0x3f}
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode([]float32{1.0}) = % x, want % x", got, want)
	}
}

func TestEncodeByteLayoutMulti(t *testing.T) {
	// Verify dim*4 sizing and ordering across multiple elements.
	// -2.0f32 = 0xc0000000 -> 00 00 00 c0 ; 0.5f32 = 0x3f000000 -> 00 00 00 3f.
	got := Encode([]float32{1.0, -2.0, 0.5})
	want := []byte{
		0x00, 0x00, 0x80, 0x3f, // 1.0
		0x00, 0x00, 0x00, 0xc0, // -2.0
		0x00, 0x00, 0x00, 0x3f, // 0.5
	}
	if len(got) != len([]float32{1.0, -2.0, 0.5})*4 {
		t.Fatalf("Encode len = %d, want %d", len(got), 3*4)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode = % x, want % x", got, want)
	}
}

func TestEncodeEmpty(t *testing.T) {
	got := Encode(nil)
	if len(got) != 0 {
		t.Fatalf("Encode(nil) len = %d, want 0", len(got))
	}
	if rt := Decode(got); len(rt) != 0 {
		t.Fatalf("Decode(Encode(nil)) len = %d, want 0", len(rt))
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   []float32
	}{
		{"single", []float32{1.0}},
		{"basis", []float32{3, 4, 0, 0}},
		{"signs and fractions", []float32{1.0, -2.0, 0.5, -0.25, 0.125}},
		{"specials", []float32{
			0,
			float32(math.Copysign(0, -1)), // negative zero
			float32(math.Inf(1)),
			float32(math.Inf(-1)),
			3.4028235e38,  // ~max float32
			1.1754944e-38, // ~smallest normal float32
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decode(Encode(tt.in))
			if len(got) != len(tt.in) {
				t.Fatalf("round-trip len = %d, want %d", len(got), len(tt.in))
			}
			for i := range tt.in {
				// Exact bit-for-bit equality, so Inf compares fine. NaN is
				// deliberately excluded since NaN != NaN.
				if math.Float32bits(got[i]) != math.Float32bits(tt.in[i]) {
					t.Errorf("index %d: got %v (bits %#x), want %v (bits %#x)",
						i, got[i], math.Float32bits(got[i]),
						tt.in[i], math.Float32bits(tt.in[i]))
				}
			}
		})
	}
}

func TestNormalizeUnitLength(t *testing.T) {
	const tol = 1e-6
	tests := []struct {
		name string
		in   []float32
	}{
		{"3-4-5 triangle", []float32{3, 4}},
		{"axis aligned", []float32{0, 0, 5, 0}},
		{"negatives", []float32{-1, -1, -1, -1}},
		{"tiny", []float32{1e-3, 2e-3, -3e-3}},
		{"large", []float32{1e6, -2e6, 3e6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := Normalize(tt.in)
			if len(out) != len(tt.in) {
				t.Fatalf("len = %d, want %d", len(out), len(tt.in))
			}
			if n := l2(out); !floatEq(n, 1.0, tol) {
				t.Errorf("L2(normalized) = %v, want ~1", n)
			}
			// Direction must be preserved: normalized is a positive scalar
			// multiple of the input.
			orig := l2(tt.in)
			for i := range tt.in {
				want := float64(tt.in[i]) / orig
				if !floatEq(float64(out[i]), want, tol) {
					t.Errorf("index %d: got %v, want %v (direction changed)", i, out[i], want)
				}
			}
		})
	}
}

func TestNormalizeZeroVector(t *testing.T) {
	in := []float32{0, 0, 0, 0}
	out := Normalize(in)
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d", len(out), len(in))
	}
	for i, x := range out {
		if x != 0 {
			t.Errorf("index %d: got %v, want 0 (zero vector must stay zero)", i, x)
		}
	}
	// Must return a copy, not alias the input, so callers can mutate freely.
	out[0] = 1
	if in[0] != 0 {
		t.Errorf("Normalize aliased its input: in[0] = %v", in[0])
	}
}

func TestNormalizeEmpty(t *testing.T) {
	out := Normalize(nil)
	if len(out) != 0 {
		t.Fatalf("Normalize(nil) len = %d, want 0", len(out))
	}
}

func TestDotEqualUnitVectors(t *testing.T) {
	const tol = 1e-6
	tests := []struct {
		name string
		in   []float32
	}{
		{"2d", []float32{3, 4}},
		{"4d", []float32{1, 2, 3, 4}},
		{"with negatives", []float32{-5, 12, 0, -7}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := Normalize(tt.in)
			// Dot of a unit vector with itself is the squared norm == 1.
			if got := Dot(u, u); !floatEq(got, 1.0, tol) {
				t.Errorf("Dot(u, u) = %v, want ~1", got)
			}
		})
	}
}

func TestDot(t *testing.T) {
	const tol = 1e-9
	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
		{"simple", []float32{1, 2, 3}, []float32{4, 5, 6}, 32},
		{"shorter b truncates", []float32{1, 2, 3}, []float32{4, 5}, 14},
		{"shorter a truncates", []float32{1, 2}, []float32{4, 5, 6}, 14},
		{"empty", []float32{}, []float32{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Dot(tt.a, tt.b); !floatEq(got, tt.want, tol) {
				t.Errorf("Dot(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCosineIgnoresMagnitude(t *testing.T) {
	const tol = 1e-9
	// Collinear vectors of different magnitude have cosine 1.
	if got := Cosine([]float32{3, 4, 0, 0}, []float32{6, 8, 0, 0}); !floatEq(got, 1.0, tol) {
		t.Errorf("Cosine of collinear vectors = %v, want 1", got)
	}

	// A vector and its scaled copies all share the cosine of the unit form.
	base := []float32{1, -2, 3, -4, 5}
	unit := Normalize(base)
	ref := []float32{2, 1, 0, 0, -1}
	wantCos := Cosine(unit, ref)
	for _, scale := range []float32{0.001, 0.5, 1, 2, 1000} {
		scaled := make([]float32, len(base))
		for i, x := range base {
			scaled[i] = x * scale
		}
		got := Cosine(scaled, ref)
		if !floatEq(got, wantCos, 1e-6) {
			t.Errorf("Cosine with scale %v = %v, want %v (magnitude must not matter)", scale, got, wantCos)
		}
	}
}

func TestCosine(t *testing.T) {
	const tol = 1e-9
	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 2, 3}, []float32{1, 2, 3}, 1},
		{"collinear scaled", []float32{3, 4, 0, 0}, []float32{6, 8, 0, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 1}, []float32{-1, -1}, -1},
		{"45 degrees", []float32{1, 0}, []float32{1, 1}, 1 / math.Sqrt2},
		{"zero a", []float32{0, 0}, []float32{1, 1}, 0},
		{"zero b", []float32{1, 1}, []float32{0, 0}, 0},
		{"both zero", []float32{0, 0}, []float32{0, 0}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Cosine(tt.a, tt.b); !floatEq(got, tt.want, tol) {
				t.Errorf("Cosine(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCosineBounded(t *testing.T) {
	// Cosine is mathematically bounded to [-1, 1]; guard against drift.
	const eps = 1e-12
	vecs := [][]float32{
		{1, 0, 0},
		{0.5, 0.5, 0.5},
		{-3, 7, -2},
		{100, 0.001, -50},
	}
	for _, a := range vecs {
		for _, b := range vecs {
			c := Cosine(a, b)
			if c < -1-eps || c > 1+eps {
				t.Errorf("Cosine(%v, %v) = %v, out of [-1,1]", a, b, c)
			}
		}
	}
}
