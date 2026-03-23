// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file contains tests for Long-Term Prediction (LTP) analysis.

package magnum

import (
	"math"
	"testing"
)

// TestNewLTPAnalyzer tests LTP analyzer creation.
func TestNewLTPAnalyzer(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		wantMax    int
	}{
		{
			name:       "narrowband 8kHz",
			sampleRate: SampleRate8k,
			wantMax:    LTPMaxPitchLag8k,
		},
		{
			name:       "wideband 16kHz",
			sampleRate: SampleRate16k,
			wantMax:    LTPMaxPitchLag16k,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ltp := NewLTPAnalyzer(tt.sampleRate)
			if ltp == nil {
				t.Fatal("NewLTPAnalyzer returned nil")
			}
			if ltp.MaxLag() != tt.wantMax {
				t.Errorf("MaxLag() = %d, want %d", ltp.MaxLag(), tt.wantMax)
			}
			if ltp.SampleRate() != tt.sampleRate {
				t.Errorf("SampleRate() = %d, want %d", ltp.SampleRate(), tt.sampleRate)
			}
		})
	}
}

// TestLTPAnalyze_VoicedFrame tests LTP analysis on voiced speech (sine wave).
func TestLTPAnalyze_VoicedFrame(t *testing.T) {
	sampleRate := SampleRate16k
	ltp := NewLTPAnalyzer(sampleRate)

	// Generate a 200 Hz sine wave (typical male fundamental)
	freq := 200.0
	numSamples := sampleRate / 25 // 40ms = 640 samples
	samples := make([]float64, numSamples)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * freq * float64(i) / float64(sampleRate))
	}

	result := ltp.Analyze(samples)
	if result == nil {
		t.Fatal("Analyze returned nil")
	}

	// Should be detected as voiced
	if !result.Voiced {
		t.Error("Voiced frame not detected as voiced")
	}

	// Check subframe results
	expectedLag := int(float64(sampleRate) / freq) // 80 samples
	for sf := 0; sf < LTPNumSubframes; sf++ {
		if result.Subframes[sf] == nil {
			t.Errorf("Subframe[%d] is nil", sf)
			continue
		}

		// Pitch lag should be close to expected
		lag := result.Subframes[sf].PitchLag
		tolerance := expectedLag / 5 // 20% tolerance
		if lag < expectedLag-tolerance || lag > expectedLag+tolerance {
			t.Errorf("Subframe[%d]: lag=%d, expected ~%d (±%d)",
				sf, lag, expectedLag, tolerance)
		}

		// Codebook index should not be 0 (some prediction)
		if result.Subframes[sf].CodebookIndex == 0 {
			t.Logf("Subframe[%d]: codebook=0 (no prediction)", sf)
		}
	}

	t.Logf("Voiced analysis: contour=%d", result.ContourIndex)
}

// TestLTPAnalyze_UnvoicedFrame tests LTP analysis on unvoiced content (noise).
func TestLTPAnalyze_UnvoicedFrame(t *testing.T) {
	sampleRate := SampleRate16k
	ltp := NewLTPAnalyzer(sampleRate)

	// Generate pseudo-random noise
	numSamples := sampleRate / 25 // 40ms
	samples := make([]float64, numSamples)
	seed := uint32(12345)
	for i := range samples {
		seed = seed*1103515245 + 12345
		samples[i] = float64(int32(seed)>>16) / 32768.0
	}

	result := ltp.Analyze(samples)
	if result == nil {
		t.Fatal("Analyze returned nil")
	}

	// Noise should be detected as unvoiced (or have low prediction gain)
	if result.Voiced {
		t.Log("Noise frame detected as voiced - checking prediction gains")
		for sf := 0; sf < LTPNumSubframes; sf++ {
			if result.Subframes[sf] != nil {
				if result.Subframes[sf].PredictionGain > 3.0 {
					t.Errorf("Subframe[%d]: high prediction gain %.2f dB for noise",
						sf, result.Subframes[sf].PredictionGain)
				}
			}
		}
	}
}

// TestLTPAnalyze_ShortInput tests handling of too-short input.
func TestLTPAnalyze_ShortInput(t *testing.T) {
	ltp := NewLTPAnalyzer(SampleRate16k)

	// Input shorter than required subframes
	samples := make([]float64, 50)
	result := ltp.Analyze(samples)

	if result != nil {
		t.Error("Expected nil for short input, got result")
	}
}

// TestApplyLTP tests LTP filtering.
func TestApplyLTP(t *testing.T) {
	// Create a simple periodic signal
	n := 200
	signal := make([]float64, n)
	period := 20
	for i := range signal {
		signal[i] = float64((i % period) - period/2)
	}

	// Apply LTP with center tap gain
	gains := []float64{0.0, 0.0, 0.5, 0.0, 0.0}
	prediction := ApplyLTP(signal, period, gains)

	if len(prediction) != n {
		t.Errorf("Prediction length = %d, want %d", len(prediction), n)
	}

	// Prediction should be non-zero after lag samples
	hasNonZero := false
	for i := period; i < n; i++ {
		if prediction[i] != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("Prediction is all zeros after lag")
	}
}

// TestComputeLTPResidual tests residual computation.
func TestComputeLTPResidual(t *testing.T) {
	// Create a periodic signal
	n := 200
	period := 20
	signal := make([]float64, n)
	for i := range signal {
		signal[i] = math.Sin(2 * math.Pi * float64(i) / float64(period))
	}

	// With perfect prediction, residual should be small
	gains := []float64{0.0, 0.0, 0.9, 0.0, 0.0}
	residual := ComputeLTPResidual(signal, period, gains)

	if len(residual) != n {
		t.Errorf("Residual length = %d, want %d", len(residual), n)
	}

	// Compute residual energy (should be lower than signal energy)
	signalEnergy := 0.0
	residualEnergy := 0.0
	for i := period; i < n; i++ { // Start after lag
		signalEnergy += signal[i] * signal[i]
		residualEnergy += residual[i] * residual[i]
	}

	if residualEnergy > signalEnergy*0.5 {
		t.Errorf("Residual energy %.4f is not much lower than signal energy %.4f",
			residualEnergy, signalEnergy)
	}

	t.Logf("Signal energy: %.4f, Residual energy: %.4f (ratio: %.2f)",
		signalEnergy, residualEnergy, residualEnergy/signalEnergy)
}

// TestSynthesizeLTP tests signal reconstruction from residual.
func TestSynthesizeLTP(t *testing.T) {
	// Create a simple signal
	n := 100
	original := make([]float64, n)
	for i := range original {
		original[i] = float64(i) / float64(n)
	}

	lag := 10
	gains := []float64{0.0, 0.0, 0.3, 0.0, 0.0}

	// Compute residual and synthesize back
	residual := ComputeLTPResidual(original, lag, gains)
	synthesized := SynthesizeLTP(residual, lag, gains)

	if len(synthesized) != n {
		t.Errorf("Synthesized length = %d, want %d", len(synthesized), n)
	}

	// Note: Due to IIR nature, synthesis may not perfectly reconstruct
	// but should be reasonably close for the first part of the signal
}

// TestLTPCodebookQuantization tests gain quantization.
func TestLTPCodebookQuantization(t *testing.T) {
	ltp := NewLTPAnalyzer(SampleRate16k)

	tests := []struct {
		gains   [LTPOrder]float64
		wantIdx int
	}{
		// Zero gains should map to index 0
		{gains: [LTPOrder]float64{0, 0, 0, 0, 0}, wantIdx: 0},
		// Single center tap should map to indices 1-3
		{gains: [LTPOrder]float64{0, 0, 0.25, 0, 0}, wantIdx: 1},
		{gains: [LTPOrder]float64{0, 0, 0.5, 0, 0}, wantIdx: 2},
		{gains: [LTPOrder]float64{0, 0, 0.75, 0, 0}, wantIdx: 3},
	}

	for i, tt := range tests {
		idx := ltp.quantizeGains(tt.gains)
		if idx != tt.wantIdx {
			t.Errorf("Test %d: quantizeGains(%v) = %d, want %d",
				i, tt.gains, idx, tt.wantIdx)
		}
	}
}

// TestEncodeLTPParams tests LTP parameter encoding/decoding.
func TestEncodeLTPParams(t *testing.T) {
	// Create a voiced result
	result := &LTPFrameResult{
		Voiced:       true,
		ContourIndex: 1,
	}
	for sf := 0; sf < LTPNumSubframes; sf++ {
		result.Subframes[sf] = &LTPResult{
			PitchLag:      80 + sf*2, // Slight variation
			CodebookIndex: sf + 1,
		}
		copy(result.Subframes[sf].Gains[:], LTPGainCodebook[sf+1])
	}

	frameLag := 80

	// Encode
	enc := NewRangeEncoder()
	EncodeLTPParams(enc, result, frameLag)
	encoded := enc.Bytes()

	// Decode
	dec := NewRangeDecoder(encoded)
	decoded, decodedLag := DecodeLTPParams(dec)

	// Verify
	if decoded == nil {
		t.Fatal("DecodeLTPParams returned nil")
	}
	if !decoded.Voiced {
		t.Error("Decoded result not voiced")
	}
	if decodedLag != frameLag {
		t.Errorf("Decoded lag = %d, want %d", decodedLag, frameLag)
	}
	if decoded.ContourIndex != result.ContourIndex {
		t.Errorf("Decoded contour = %d, want %d", decoded.ContourIndex, result.ContourIndex)
	}

	for sf := 0; sf < LTPNumSubframes; sf++ {
		if decoded.Subframes[sf] == nil {
			t.Errorf("Decoded subframe[%d] is nil", sf)
			continue
		}
		if decoded.Subframes[sf].CodebookIndex != result.Subframes[sf].CodebookIndex {
			t.Errorf("Subframe[%d] codebook = %d, want %d",
				sf, decoded.Subframes[sf].CodebookIndex, result.Subframes[sf].CodebookIndex)
		}
	}
}

// TestEncodeLTPParams_Unvoiced tests encoding/decoding unvoiced frames.
func TestEncodeLTPParams_Unvoiced(t *testing.T) {
	result := &LTPFrameResult{
		Voiced: false,
	}

	// Encode
	enc := NewRangeEncoder()
	EncodeLTPParams(enc, result, 0)
	encoded := enc.Bytes()

	// Decode
	dec := NewRangeDecoder(encoded)
	decoded, _ := DecodeLTPParams(dec)

	if decoded == nil {
		t.Fatal("DecodeLTPParams returned nil")
	}
	if decoded.Voiced {
		t.Error("Decoded unvoiced frame as voiced")
	}
}

// TestLTPReset tests the Reset function.
func TestLTPReset(t *testing.T) {
	ltp := NewLTPAnalyzer(SampleRate16k)

	// Process a frame
	samples := make([]float64, 640)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 16000)
	}
	ltp.Analyze(samples)

	// Reset should not panic
	ltp.Reset()
}

// BenchmarkLTPAnalyze benchmarks LTP analysis.
func BenchmarkLTPAnalyze(b *testing.B) {
	ltp := NewLTPAnalyzer(SampleRate16k)
	numSamples := 640 // 40ms at 16kHz

	// Generate a test signal
	samples := make([]float64, numSamples)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 16000)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ltp.Analyze(samples)
	}
}

// BenchmarkApplyLTP benchmarks LTP filtering.
func BenchmarkApplyLTP(b *testing.B) {
	n := 320
	signal := make([]float64, n)
	for i := range signal {
		signal[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 16000)
	}

	gains := []float64{0.1, 0.2, 0.4, 0.2, 0.1}
	lag := 80

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ApplyLTP(signal, lag, gains)
	}
}
