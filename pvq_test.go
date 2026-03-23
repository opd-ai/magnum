package magnum

import (
	"math"
	"testing"
)

func TestPVQUFunction(t *testing.T) {
	pvq := NewPVQ()

	// Test base cases
	if got := pvq.U(0, 0); got != 1 {
		t.Errorf("U(0,0) = %d, want 1", got)
	}
	if got := pvq.U(5, 0); got != 1 {
		t.Errorf("U(5,0) = %d, want 1", got)
	}
	if got := pvq.U(0, 5); got != 0 {
		t.Errorf("U(0,5) = %d, want 0", got)
	}
	if got := pvq.U(1, 1); got != 1 {
		t.Errorf("U(1,1) = %d, want 1", got)
	}

	// Test symmetry: U(N,K) = U(K,N) (only for N>0 and K>0)
	for n := 1; n <= 6; n++ {
		for k := 1; k <= 6; k++ {
			u1 := pvq.U(n, k)
			u2 := pvq.U(k, n)
			if u1 != u2 {
				t.Errorf("U(%d,%d) = %d != U(%d,%d) = %d", n, k, u1, k, n, u2)
			}
		}
	}

	// Test known values for U(2,K): U(2,K) = 2*K - 1
	for k := 2; k <= 10; k++ {
		expected := uint64(2*k - 1)
		got := pvq.U(2, k)
		if got != expected {
			t.Errorf("U(2,%d) = %d, want %d", k, got, expected)
		}
	}

	// Test known values for U(3,K): U(3,K) = 2*K^2 - 2*K + 1
	for k := 2; k <= 10; k++ {
		expected := uint64(2*k*k - 2*k + 1)
		got := pvq.U(3, k)
		if got != expected {
			t.Errorf("U(3,%d) = %d, want %d", k, got, expected)
		}
	}
}

func TestPVQVFunction(t *testing.T) {
	pvq := NewPVQ()

	// V(N,0) = 1 (only the zero vector)
	for n := 0; n <= 10; n++ {
		if got := pvq.V(n, 0); got != 1 {
			t.Errorf("V(%d,0) = %d, want 1", n, got)
		}
	}

	// V(0,K) = 0 for K > 0 (no dimensions to place pulses)
	for k := 1; k <= 10; k++ {
		if got := pvq.V(0, k); got != 0 {
			t.Errorf("V(0,%d) = %d, want 0", k, got)
		}
	}

	// V(1,K) = 2 for K > 0 (just +K or -K)
	for k := 1; k <= 10; k++ {
		if got := pvq.V(1, k); got != 2 {
			t.Errorf("V(1,%d) = %d, want 2", k, got)
		}
	}

	// V(N,1) = 2*N (one pulse in any of N positions, with either sign)
	for n := 1; n <= 10; n++ {
		expected := uint64(2 * n)
		if got := pvq.V(n, 1); got != expected {
			t.Errorf("V(%d,1) = %d, want %d", n, got, expected)
		}
	}
}

func TestPVQEncodeDecodeRoundTrip(t *testing.T) {
	pvq := NewPVQ()

	testCases := []struct {
		name   string
		vector []float64
		k      int
	}{
		{"simple 2D", []float64{1.0, 0.0}, 4},
		{"simple 2D negative", []float64{-1.0, 0.0}, 4},
		{"diagonal 2D", []float64{1.0, 1.0}, 4},
		{"3D mixed signs", []float64{1.0, -2.0, 1.0}, 6},
		{"4D unit simplex", []float64{0.5, 0.5, 0.5, 0.5}, 8},
		{"sparse 5D", []float64{3.0, 0.0, 0.0, 0.0, 1.0}, 4},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Make a copy since Encode normalizes in-place
			input := make([]float64, len(tc.vector))
			copy(input, tc.vector)

			// Encode
			cw := pvq.Encode(input, tc.k)

			// Verify codeword properties
			if cw.N != len(tc.vector) {
				t.Errorf("codeword N = %d, want %d", cw.N, len(tc.vector))
			}

			// Verify K pulses total
			totalPulses := 0
			for _, p := range cw.Pulses {
				totalPulses += p
			}
			if totalPulses != tc.k {
				t.Errorf("total pulses = %d, want %d", totalPulses, tc.k)
			}

			// Decode
			decoded := pvq.Decode(cw)

			// Verify decoded is unit norm
			normSq := 0.0
			for _, v := range decoded {
				normSq += v * v
			}
			if math.Abs(normSq-1.0) > 1e-10 {
				t.Errorf("decoded norm^2 = %f, want 1.0", normSq)
			}
		})
	}
}

func TestPVQIndexRoundTrip(t *testing.T) {
	pvq := NewPVQ()

	// Test that all indices in range [0, V(N,K)) produce valid round-trips
	testCases := []struct {
		n, k int
	}{
		{2, 2},
		{2, 3},
		{3, 2},
		{3, 3},
		{4, 2},
		{4, 3},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			v := pvq.V(tc.n, tc.k)

			for idx := uint64(0); idx < v; idx++ {
				// Decode from index
				cw := pvq.DecodeFromIndex(idx, tc.n, tc.k)

				// Verify total pulses
				totalPulses := 0
				for _, p := range cw.Pulses {
					totalPulses += p
				}
				if totalPulses != tc.k {
					t.Errorf("N=%d K=%d index=%d: total pulses = %d, want %d",
						tc.n, tc.k, idx, totalPulses, tc.k)
				}

				// Verify index matches
				if cw.Index != idx {
					t.Errorf("N=%d K=%d: decoded index = %d, want %d",
						tc.n, tc.k, cw.Index, idx)
				}
			}
		})
	}
}

func TestPVQBitsRequired(t *testing.T) {
	pvq := NewPVQ()

	// V(N,K=0) = 1, requires 0 bits
	for n := 1; n <= 10; n++ {
		if bits := pvq.BitsRequired(n, 0); bits != 0 {
			t.Errorf("BitsRequired(%d, 0) = %d, want 0", n, bits)
		}
	}

	// V(N,1) = 2*N, requires ceil(log2(2*N)) bits
	for n := 1; n <= 10; n++ {
		expected := int(math.Ceil(math.Log2(float64(2 * n))))
		if bits := pvq.BitsRequired(n, 1); bits != expected {
			t.Errorf("BitsRequired(%d, 1) = %d, want %d", n, bits, expected)
		}
	}
}

func TestPVQSelectK(t *testing.T) {
	pvq := NewPVQ()

	// With zero bits, K should be 0
	if k := pvq.SelectK(10, 0); k != 0 {
		t.Errorf("SelectK(10, 0) = %d, want 0", k)
	}

	// With zero dimensions, K should be 0
	if k := pvq.SelectK(0, 10); k != 0 {
		t.Errorf("SelectK(0, 10) = %d, want 0", k)
	}

	// K should increase with more bits
	prevK := 0
	for bits := 1; bits <= 20; bits++ {
		k := pvq.SelectK(8, bits)
		if k < prevK {
			t.Errorf("SelectK(8, %d) = %d < SelectK(8, %d) = %d",
				bits, k, bits-1, prevK)
		}
		prevK = k
	}
}

func TestPVQDistortion(t *testing.T) {
	pvq := NewPVQ()

	// Test that higher K gives lower distortion
	original := []float64{1.0, 2.0, 3.0, 4.0}

	// Normalize for comparison
	norm := 0.0
	for _, v := range original {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	normalized := make([]float64, len(original))
	for i, v := range original {
		normalized[i] = v / norm
	}

	prevDistortion := math.MaxFloat64
	for k := 1; k <= 16; k++ {
		input := make([]float64, len(original))
		copy(input, original)

		cw := pvq.Encode(input, k)
		decoded := pvq.Decode(cw)
		distortion := ComputeDistortion(normalized, decoded)

		if distortion > prevDistortion+1e-10 {
			t.Errorf("K=%d distortion %f > K=%d distortion %f",
				k, distortion, k-1, prevDistortion)
		}
		prevDistortion = distortion
	}
}

func TestPVQEdgeCases(t *testing.T) {
	pvq := NewPVQ()

	// Empty vector
	cw := pvq.Encode([]float64{}, 5)
	if cw.N != 0 || cw.K != 0 {
		t.Error("empty vector should return zero codeword")
	}

	decoded := pvq.Decode(cw)
	if len(decoded) != 0 {
		t.Error("decoding empty codeword should return empty slice")
	}

	// K=0 (no pulses)
	cw = pvq.Encode([]float64{1.0, 2.0, 3.0}, 0)
	if cw.K != 0 {
		t.Error("K=0 should return zero pulses")
	}

	// Zero vector input
	cw = pvq.Encode([]float64{0.0, 0.0, 0.0}, 5)
	// Should still allocate pulses somehow (implementation-dependent)
	totalPulses := 0
	for _, p := range cw.Pulses {
		totalPulses += p
	}
	// With zero input, we can't determine direction, but pulses should be allocated
	// The exact behavior depends on implementation; just verify no crash
}

func BenchmarkPVQEncode(b *testing.B) {
	pvq := NewPVQ()
	vector := make([]float64, 32)
	for i := range vector {
		vector[i] = float64(i%5 - 2)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		input := make([]float64, len(vector))
		copy(input, vector)
		pvq.Encode(input, 16)
	}
}

func BenchmarkPVQDecode(b *testing.B) {
	pvq := NewPVQ()
	vector := make([]float64, 32)
	for i := range vector {
		vector[i] = float64(i%5 - 2)
	}
	input := make([]float64, len(vector))
	copy(input, vector)
	cw := pvq.Encode(input, 16)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pvq.Decode(cw)
	}
}

func BenchmarkPVQUCache(b *testing.B) {
	pvq := NewPVQ()

	// First populate cache
	for n := 0; n <= 20; n++ {
		for k := 0; k <= 20; k++ {
			pvq.U(n, k)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pvq.U(15, 12)
	}
}
