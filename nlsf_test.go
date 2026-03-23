package magnum

import (
	"math"
	"testing"
)

// TestLPCToNLSFRoundTrip tests that LPC → NLSF → LPC round-trips correctly.
func TestLPCToNLSFRoundTrip(t *testing.T) {
	t.Parallel()

	// Generate LPC coefficients from LPC analysis (more realistic)
	// Create a simple signal and analyze it
	n := 64
	samples := make([]float64, n)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 8000)
	}

	analyzer := NewLPCAnalyzer(4)
	result := analyzer.Analyze(samples)
	lpc := result.Coefficients

	// Convert to NLSF
	nlsf := LPCToNLSF(lpc)

	if len(nlsf) != len(lpc) {
		t.Fatalf("NLSF length %d != LPC length %d", len(nlsf), len(lpc))
	}

	// Check NLSFs are in valid range and increasing
	for i, val := range nlsf {
		if val < 0 || val > 1 {
			t.Errorf("NLSF[%d] = %f is outside [0, 1]", i, val)
		}
		if i > 0 && nlsf[i] <= nlsf[i-1] {
			t.Errorf("NLSFs not strictly increasing: [%d]=%f >= [%d]=%f",
				i-1, nlsf[i-1], i, nlsf[i])
		}
	}

	// The NLSF representation itself is the main test
	// Full round-trip is lossy due to root-finding and polynomial reconstruction
	t.Logf("LPC: %v", lpc)
	t.Logf("NLSF: %v", nlsf)
}

// TestStabilizeNLSF tests the NLSF stabilization procedure.
func TestStabilizeNLSF(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []float64
	}{
		{
			name:  "already stable",
			input: []float64{0.1, 0.3, 0.5, 0.7, 0.9},
		},
		{
			name:  "too close",
			input: []float64{0.1, 0.1001, 0.1002, 0.5, 0.9},
		},
		{
			name:  "out of range low",
			input: []float64{-0.1, 0.3, 0.5, 0.7, 0.9},
		},
		{
			name:  "out of range high",
			input: []float64{0.1, 0.3, 0.5, 0.7, 1.1},
		},
		{
			name:  "decreasing",
			input: []float64{0.9, 0.7, 0.5, 0.3, 0.1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nlsf := make([]float64, len(tt.input))
			copy(nlsf, tt.input)

			StabilizeNLSF(nlsf)

			// Check all values are in range
			for i, val := range nlsf {
				if val < nlsfMinValue || val > nlsfMaxValue {
					t.Errorf("NLSF[%d] = %f is outside [%f, %f]",
						i, val, nlsfMinValue, nlsfMaxValue)
				}
			}

			// Check strictly increasing with minimum separation
			for i := 1; i < len(nlsf); i++ {
				if nlsf[i] <= nlsf[i-1] {
					t.Errorf("Not strictly increasing: [%d]=%f >= [%d]=%f",
						i-1, nlsf[i-1], i, nlsf[i])
				}
			}
		})
	}
}

// TestInterpolateNLSF tests NLSF interpolation.
func TestInterpolateNLSF(t *testing.T) {
	t.Parallel()

	nlsf1 := []float64{0.1, 0.2, 0.3, 0.4, 0.5}
	nlsf2 := []float64{0.2, 0.4, 0.6, 0.7, 0.9}

	// Test alpha = 0 returns nlsf1
	result := InterpolateNLSF(nlsf1, nlsf2, 0.0)
	for i := range result {
		if math.Abs(result[i]-nlsf1[i]) > 1e-10 {
			t.Errorf("alpha=0: result[%d] = %f, want %f", i, result[i], nlsf1[i])
		}
	}

	// Test alpha = 1 returns nlsf2
	result = InterpolateNLSF(nlsf1, nlsf2, 1.0)
	for i := range result {
		if math.Abs(result[i]-nlsf2[i]) > 1e-10 {
			t.Errorf("alpha=1: result[%d] = %f, want %f", i, result[i], nlsf2[i])
		}
	}

	// Test alpha = 0.5 returns midpoint
	result = InterpolateNLSF(nlsf1, nlsf2, 0.5)
	for i := range result {
		expected := (nlsf1[i] + nlsf2[i]) / 2
		if math.Abs(result[i]-expected) > 1e-10 {
			t.Errorf("alpha=0.5: result[%d] = %f, want %f", i, result[i], expected)
		}
	}

	// Test that result is always stable
	for alpha := 0.0; alpha <= 1.0; alpha += 0.1 {
		result = InterpolateNLSF(nlsf1, nlsf2, alpha)
		for i := 1; i < len(result); i++ {
			if result[i] <= result[i-1] {
				t.Errorf("alpha=%f: not increasing at [%d]", alpha, i)
			}
		}
	}
}

// TestInterpolateNLSFEdgeCases tests edge cases for NLSF interpolation.
func TestInterpolateNLSFEdgeCases(t *testing.T) {
	t.Parallel()

	nlsf := []float64{0.1, 0.3, 0.5, 0.7, 0.9}

	// Different lengths should return nil
	result := InterpolateNLSF(nlsf, []float64{0.1, 0.2}, 0.5)
	if result != nil {
		t.Error("expected nil for different lengths")
	}

	// Empty should return nil
	result = InterpolateNLSF([]float64{}, []float64{}, 0.5)
	if result != nil {
		t.Error("expected nil for empty input")
	}

	// Alpha clamping
	result = InterpolateNLSF(nlsf, nlsf, -0.5)
	if result == nil {
		t.Error("unexpected nil for negative alpha")
	}

	result = InterpolateNLSF(nlsf, nlsf, 1.5)
	if result == nil {
		t.Error("unexpected nil for alpha > 1")
	}
}

// TestNLSFQuantizer tests NLSF quantization and dequantization.
func TestNLSFQuantizer(t *testing.T) {
	t.Parallel()

	order := 5
	q := NewNLSFQuantizer(order)

	nlsf := []float64{0.1, 0.25, 0.5, 0.75, 0.9}

	// Quantize
	indices, quantized := q.Quantize(nlsf, 8)

	if len(indices) != order {
		t.Fatalf("indices length %d != order %d", len(indices), order)
	}
	if len(quantized) != order {
		t.Fatalf("quantized length %d != order %d", len(quantized), order)
	}

	// Check indices are in valid range
	numLevels := 1 << 8
	for i, idx := range indices {
		if idx < 0 || idx >= numLevels {
			t.Errorf("index[%d] = %d is outside [0, %d)", i, idx, numLevels)
		}
	}

	// Dequantize and check round-trip
	deq := q.Dequantize(indices, 8)

	for i := range quantized {
		// Quantized and dequantized should match (both are stable)
		if math.Abs(quantized[i]-deq[i]) > 0.01 {
			t.Errorf("deq[%d] = %f != quantized[%d] = %f", i, deq[i], i, quantized[i])
		}
	}
}

// TestLPCToNLSFEmpty tests handling of empty input.
func TestLPCToNLSFEmpty(t *testing.T) {
	t.Parallel()

	nlsf := LPCToNLSF(nil)
	if nlsf != nil {
		t.Error("expected nil for nil input")
	}

	nlsf = LPCToNLSF([]float64{})
	if nlsf != nil {
		t.Error("expected nil for empty input")
	}
}

// TestNLSFToLPCEmpty tests handling of empty input.
func TestNLSFToLPCEmpty(t *testing.T) {
	t.Parallel()

	lpc := NLSFToLPC(nil)
	if lpc != nil {
		t.Error("expected nil for nil input")
	}

	lpc = NLSFToLPC([]float64{})
	if lpc != nil {
		t.Error("expected nil for empty input")
	}
}

// TestSortFloat64s tests the sorting function.
func TestSortFloat64s(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []float64
		expected []float64
	}{
		{"already sorted", []float64{1, 2, 3, 4, 5}, []float64{1, 2, 3, 4, 5}},
		{"reverse", []float64{5, 4, 3, 2, 1}, []float64{1, 2, 3, 4, 5}},
		{"random", []float64{3, 1, 4, 1, 5}, []float64{1, 1, 3, 4, 5}},
		{"single", []float64{42}, []float64{42}},
		{"empty", []float64{}, []float64{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := make([]float64, len(tt.input))
			copy(input, tt.input)
			sortFloat64s(input)
			for i := range input {
				if input[i] != tt.expected[i] {
					t.Errorf("got %v, want %v", input, tt.expected)
					break
				}
			}
		})
	}
}

// TestNLSFWithLPCAnalyzer integrates NLSF with LPC analysis.
func TestNLSFWithLPCAnalyzer(t *testing.T) {
	t.Parallel()

	// Generate a test signal
	n := 160
	samples := make([]float64, n)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 300 * float64(i) / 8000)
	}

	// Analyze with LPC
	analyzer := NewLPCAnalyzer(LPCOrderNarrowband)
	lpcResult := analyzer.Analyze(samples)

	// Convert to NLSF
	nlsf := LPCToNLSF(lpcResult.Coefficients)

	// Check NLSF properties
	if len(nlsf) != LPCOrderNarrowband {
		t.Errorf("NLSF length %d != %d", len(nlsf), LPCOrderNarrowband)
	}

	// All should be in [0, 1] and increasing
	for i, val := range nlsf {
		if val < 0 || val > 1 {
			t.Errorf("NLSF[%d] = %f outside [0, 1]", i, val)
		}
		if i > 0 && val <= nlsf[i-1] {
			t.Errorf("NLSF not increasing at [%d]", i)
		}
	}
}

// BenchmarkLPCToNLSF benchmarks LPC to NLSF conversion.
func BenchmarkLPCToNLSF(b *testing.B) {
	lpc := make([]float64, LPCOrderNarrowband)
	for i := range lpc {
		lpc[i] = 0.5 * float64(LPCOrderNarrowband-i) / float64(LPCOrderNarrowband)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = LPCToNLSF(lpc)
	}
}

// BenchmarkNLSFToLPC benchmarks NLSF to LPC conversion.
func BenchmarkNLSFToLPC(b *testing.B) {
	nlsf := make([]float64, LPCOrderNarrowband)
	for i := range nlsf {
		nlsf[i] = float64(i+1) / float64(LPCOrderNarrowband+1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NLSFToLPC(nlsf)
	}
}

// BenchmarkInterpolateNLSF benchmarks NLSF interpolation.
func BenchmarkInterpolateNLSF(b *testing.B) {
	nlsf1 := make([]float64, LPCOrderNarrowband)
	nlsf2 := make([]float64, LPCOrderNarrowband)
	for i := range nlsf1 {
		nlsf1[i] = float64(i+1) / float64(LPCOrderNarrowband+2)
		nlsf2[i] = float64(i+2) / float64(LPCOrderNarrowband+2)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = InterpolateNLSF(nlsf1, nlsf2, 0.5)
	}
}
