package magnum

import (
	"math"
	"testing"
)

// TestNewLPCAnalyzer verifies analyzer creation with various orders.
func TestNewLPCAnalyzer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		order         int
		expectedOrder int
	}{
		{"narrowband order", LPCOrderNarrowband, LPCOrderNarrowband},
		{"wideband order", LPCOrderWideband, LPCOrderWideband},
		{"zero order defaults", 0, LPCOrderNarrowband},
		{"negative order defaults", -1, LPCOrderNarrowband},
		{"custom order", 12, 12},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewLPCAnalyzer(tt.order)
			if a.order != tt.expectedOrder {
				t.Errorf("order = %d, want %d", a.order, tt.expectedOrder)
			}
		})
	}
}

// TestLPCAnalyzerAnalyze tests the LPC analysis on synthetic signals.
func TestLPCAnalyzerAnalyze(t *testing.T) {
	t.Parallel()

	// Generate a simple sine wave
	n := 256
	samples := make([]float64, n)
	freq := 440.0
	sampleRate := 8000.0
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * freq * float64(i) / sampleRate)
	}

	analyzer := NewLPCAnalyzer(10)
	result := analyzer.Analyze(samples)

	// Basic sanity checks
	if len(result.Coefficients) != 10 {
		t.Errorf("expected 10 coefficients, got %d", len(result.Coefficients))
	}
	if len(result.ReflectionCoeffs) != 10 {
		t.Errorf("expected 10 reflection coefficients, got %d", len(result.ReflectionCoeffs))
	}

	// Prediction error should be positive and less than input energy
	if result.PredictionError <= 0 {
		t.Errorf("prediction error should be positive, got %f", result.PredictionError)
	}

	// Gain should be >= 1 for valid LPC
	if result.Gain < 1.0 {
		t.Errorf("gain should be >= 1.0, got %f", result.Gain)
	}

	// Reflection coefficients should be in [-1, 1] for stability
	for i, k := range result.ReflectionCoeffs {
		if math.Abs(k) > 1.0 {
			t.Errorf("reflection coeff[%d] = %f is outside [-1, 1]", i, k)
		}
	}
}

// TestLPCAnalyzerEmptyInput tests handling of empty input.
func TestLPCAnalyzerEmptyInput(t *testing.T) {
	t.Parallel()

	analyzer := NewLPCAnalyzer(10)
	result := analyzer.Analyze(nil)

	if result == nil {
		t.Fatal("expected non-nil result for empty input")
	}
	if len(result.Coefficients) != 10 {
		t.Errorf("expected 10 coefficients, got %d", len(result.Coefficients))
	}
	if result.Gain != 1.0 {
		t.Errorf("expected gain = 1.0 for empty input, got %f", result.Gain)
	}
}

// TestLPCRoundTrip verifies that LPC analysis and synthesis round-trip.
func TestLPCRoundTrip(t *testing.T) {
	t.Parallel()

	// Generate a simple test signal (no windowing to avoid numerical instability)
	n := 64
	samples := make([]float64, n)
	for i := range samples {
		// Simple sine wave
		samples[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 8000)
	}

	// Analyze with moderate order
	analyzer := NewLPCAnalyzer(4)
	result := analyzer.Analyze(samples)

	// Compute residual
	residual := ApplyLPCFilter(samples, result.Coefficients)

	// Synthesize back
	reconstructed := SynthesizeLPC(residual, result.Coefficients)

	// Check reconstruction matches original input
	maxError := 0.0
	for i := range samples {
		err := math.Abs(samples[i] - reconstructed[i])
		if err > maxError {
			maxError = err
		}
	}

	// Allow small numerical error
	if maxError > 1e-6 {
		t.Errorf("round-trip max error = %e, want < 1e-6", maxError)
	}
}

// TestAutocorrelation tests the autocorrelation function.
func TestAutocorrelation(t *testing.T) {
	t.Parallel()

	// Known test case: constant signal
	samples := []float64{1, 1, 1, 1, 1}
	ac := Autocorrelation(samples, 4)

	// For constant signal of value 1 and length 5:
	// r[0] = 5, r[1] = 4, r[2] = 3, r[3] = 2, r[4] = 1
	expected := []float64{5, 4, 3, 2, 1}
	for i, want := range expected {
		if math.Abs(ac[i]-want) > 1e-10 {
			t.Errorf("autocorr[%d] = %f, want %f", i, ac[i], want)
		}
	}
}

// TestAutocorrelationEmptyInput tests autocorrelation with empty input.
func TestAutocorrelationEmptyInput(t *testing.T) {
	t.Parallel()

	ac := Autocorrelation(nil, 10)
	if ac != nil {
		t.Errorf("expected nil for empty input, got %v", ac)
	}

	ac = Autocorrelation([]float64{}, 10)
	if ac != nil {
		t.Errorf("expected nil for empty slice, got %v", ac)
	}
}

// TestApplyLPCFilter tests the LPC filter application.
func TestApplyLPCFilter(t *testing.T) {
	t.Parallel()

	// Simple test: first-order filter with coeff = 0.5
	// residual[n] = input[n] - 0.5 * input[n-1]
	input := []float64{1, 2, 3, 4, 5}
	coeffs := []float64{0.5}

	residual := ApplyLPCFilter(input, coeffs)

	// Expected: [1, 2-0.5*1, 3-0.5*2, 4-0.5*3, 5-0.5*4]
	//         = [1, 1.5, 2, 2.5, 3]
	expected := []float64{1, 1.5, 2, 2.5, 3}
	for i, want := range expected {
		if math.Abs(residual[i]-want) > 1e-10 {
			t.Errorf("residual[%d] = %f, want %f", i, residual[i], want)
		}
	}
}

// TestSynthesizeLPC tests the LPC synthesis function.
func TestSynthesizeLPC(t *testing.T) {
	t.Parallel()

	// Simple test: first-order filter with coeff = 0.5
	// output[n] = residual[n] + 0.5 * output[n-1]
	residual := []float64{1, 1.5, 2, 2.5, 3}
	coeffs := []float64{0.5}

	output := SynthesizeLPC(residual, coeffs)

	// Expected: [1, 1.5+0.5*1, 2+0.5*2, 2.5+0.5*3, 3+0.5*4]
	//         = [1, 2, 3, 4, 5]
	expected := []float64{1, 2, 3, 4, 5}
	for i, want := range expected {
		if math.Abs(output[i]-want) > 1e-10 {
			t.Errorf("output[%d] = %f, want %f", i, output[i], want)
		}
	}
}

// TestApplyHammingWindow tests the Hamming window function.
func TestApplyHammingWindow(t *testing.T) {
	t.Parallel()

	// Test window properties
	n := 256
	ones := make([]float64, n)
	for i := range ones {
		ones[i] = 1.0
	}

	windowed := ApplyHammingWindow(ones)

	// First and last samples should be close to 0.08 (Hamming window edge value)
	if math.Abs(windowed[0]-0.08) > 0.01 {
		t.Errorf("windowed[0] = %f, want ~0.08", windowed[0])
	}

	// Middle sample should be close to 1.0
	mid := n / 2
	if math.Abs(windowed[mid]-1.0) > 0.01 {
		t.Errorf("windowed[%d] = %f, want ~1.0", mid, windowed[mid])
	}
}

// TestApplyHammingWindowEmptyInput tests Hamming window with empty input.
func TestApplyHammingWindowEmptyInput(t *testing.T) {
	t.Parallel()

	windowed := ApplyHammingWindow(nil)
	if windowed != nil {
		t.Errorf("expected nil for nil input")
	}

	windowed = ApplyHammingWindow([]float64{})
	if windowed != nil {
		t.Errorf("expected nil for empty slice")
	}
}

// TestLPCStability tests that LPC analysis produces stable filters.
func TestLPCStability(t *testing.T) {
	t.Parallel()

	// Generate various test signals
	tests := []struct {
		name    string
		samples []float64
	}{
		{
			name:    "impulse",
			samples: append([]float64{1.0}, make([]float64, 255)...),
		},
		{
			name: "white_noise",
			samples: func() []float64 {
				s := make([]float64, 256)
				// Simple pseudo-random using LCG
				x := uint32(12345)
				for i := range s {
					x = x*1103515245 + 12345
					s[i] = float64(int32(x>>16)&0x7FFF-16384) / 16384.0
				}
				return s
			}(),
		},
		{
			name: "dc_offset",
			samples: func() []float64 {
				s := make([]float64, 256)
				for i := range s {
					s[i] = 0.5
				}
				return s
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			analyzer := NewLPCAnalyzer(10)
			result := analyzer.Analyze(tt.samples)

			// All reflection coefficients must be in (-1, 1) for stability
			for i, k := range result.ReflectionCoeffs {
				if math.Abs(k) >= 1.0 {
					t.Errorf("reflection coeff[%d] = %f is outside (-1, 1)", i, k)
				}
			}

			// Prediction error must be positive
			if result.PredictionError < 0 {
				t.Errorf("prediction error = %f, must be non-negative", result.PredictionError)
			}
		})
	}
}

// TestComputeLPCGain tests the gain computation.
func TestComputeLPCGain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		error    float64
		expected float64
	}{
		{"positive error", 4.0, 2.0},
		{"unit error", 1.0, 1.0},
		{"zero error", 0.0, 0.0},
		{"negative error", -1.0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gain := ComputeLPCGain(tt.error)
			if math.Abs(gain-tt.expected) > 1e-10 {
				t.Errorf("ComputeLPCGain(%f) = %f, want %f", tt.error, gain, tt.expected)
			}
		})
	}
}

// BenchmarkLPCAnalyze benchmarks LPC analysis performance.
func BenchmarkLPCAnalyze(b *testing.B) {
	// Typical SILK frame: 160 samples at 8kHz (20ms)
	samples := make([]float64, 160)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 300 * float64(i) / 8000)
	}

	analyzer := NewLPCAnalyzer(LPCOrderNarrowband)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = analyzer.Analyze(samples)
	}
}

// BenchmarkLPCFilterApply benchmarks LPC filter application.
func BenchmarkLPCFilterApply(b *testing.B) {
	samples := make([]float64, 160)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 300 * float64(i) / 8000)
	}
	coeffs := make([]float64, 10)
	for i := range coeffs {
		coeffs[i] = 0.1 * float64(10-i) / 10.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ApplyLPCFilter(samples, coeffs)
	}
}
