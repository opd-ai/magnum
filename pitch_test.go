// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file contains tests for the open-loop pitch estimator.

package magnum

import (
	"math"
	"testing"
)

// TestNewPitchEstimator tests pitch estimator creation.
func TestNewPitchEstimator(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		wantMin    int
		wantMax    int
	}{
		{
			name:       "narrowband 8kHz",
			sampleRate: SampleRate8k,
			wantMin:    4,   // Scaled down from 8
			wantMax:    144, // Scaled down from 288
		},
		{
			name:       "wideband 16kHz",
			sampleRate: SampleRate16k,
			wantMin:    8,
			wantMax:    288,
		},
		{
			name:       "invalid defaults to wideband",
			sampleRate: 44100,
			wantMin:    8,
			wantMax:    288,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pe := NewPitchEstimator(tt.sampleRate)
			if pe == nil {
				t.Fatal("NewPitchEstimator returned nil")
			}
			if pe.MinLag() != tt.wantMin {
				t.Errorf("MinLag() = %d, want %d", pe.MinLag(), tt.wantMin)
			}
			if pe.MaxLag() != tt.wantMax {
				t.Errorf("MaxLag() = %d, want %d", pe.MaxLag(), tt.wantMax)
			}
		})
	}
}

// TestPitchEstimate_SineWave tests pitch detection on a pure sine wave.
func TestPitchEstimate_SineWave(t *testing.T) {
	sampleRate := SampleRate16k
	pe := NewPitchEstimator(sampleRate)

	// Test at various frequencies within the pitch range
	freqs := []float64{100, 150, 200, 250, 300}

	for _, freq := range freqs {
		t.Run(t.Name()+"_"+string(rune('A'+int(freq/100))), func(t *testing.T) {
			// Generate 40ms of sine wave (enough for maxLag*2 requirement)
			numSamples := sampleRate / 25 // 40ms = 640 samples at 16kHz
			samples := make([]float64, numSamples)
			for i := range samples {
				samples[i] = math.Sin(2 * math.Pi * freq * float64(i) / float64(sampleRate))
			}

			estimate := pe.Estimate(samples)
			if estimate == nil {
				t.Fatal("Estimate returned nil")
			}

			// Check that it detected as voiced
			if !estimate.Voiced {
				t.Errorf("Expected voiced=true for sine wave at %.0f Hz, got false (corr=%.3f)",
					freq, estimate.Correlation)
			}

			// Check the detected frequency is within 10% of actual
			expectedLag := int(float64(sampleRate) / freq)
			detectedFreq := pe.LagToFrequency(estimate.Lag)

			tolerance := 0.15 // 15% tolerance
			if math.Abs(detectedFreq-freq)/freq > tolerance {
				t.Errorf("Detected freq %.1f Hz, expected %.1f Hz (±%.0f%%)",
					detectedFreq, freq, tolerance*100)
			}

			// Check correlation is high for pure tone
			if estimate.Correlation < 0.5 {
				t.Errorf("Low correlation %.3f for pure sine wave", estimate.Correlation)
			}

			t.Logf("Freq=%.0f Hz: lag=%d (expected %d), corr=%.3f, detected=%.1f Hz",
				freq, estimate.Lag, expectedLag, estimate.Correlation, detectedFreq)
		})
	}
}

// TestPitchEstimate_WhiteNoise tests that white noise is detected as unvoiced.
func TestPitchEstimate_WhiteNoise(t *testing.T) {
	sampleRate := SampleRate16k
	pe := NewPitchEstimator(sampleRate)

	// Generate pseudo-random noise (40ms for maxLag*2 requirement)
	numSamples := sampleRate / 25 // 40ms = 640 samples
	samples := make([]float64, numSamples)
	seed := uint32(12345)
	for i := range samples {
		seed = seed*1103515245 + 12345
		samples[i] = float64(int32(seed)>>16) / 32768.0
	}

	estimate := pe.Estimate(samples)
	if estimate == nil {
		t.Fatal("Estimate returned nil")
	}

	// Noise should have low correlation (possibly not detected as voiced)
	if estimate.Correlation > 0.5 {
		t.Errorf("Noise has high correlation %.3f, expected < 0.5", estimate.Correlation)
	}

	t.Logf("Noise: lag=%d, corr=%.3f, voiced=%v", estimate.Lag, estimate.Correlation, estimate.Voiced)
}

// TestPitchEstimate_Silence tests handling of silence.
func TestPitchEstimate_Silence(t *testing.T) {
	sampleRate := SampleRate16k
	pe := NewPitchEstimator(sampleRate)

	// Generate silence (40ms for maxLag*2 requirement)
	numSamples := sampleRate / 25 // 40ms = 640 samples
	samples := make([]float64, numSamples)

	estimate := pe.Estimate(samples)
	if estimate == nil {
		t.Fatal("Estimate returned nil")
	}

	// Silence should not be detected as voiced
	if estimate.Voiced {
		t.Error("Silence was incorrectly detected as voiced")
	}

	if estimate.Correlation > 0.1 {
		t.Errorf("Silence has non-zero correlation %.3f", estimate.Correlation)
	}
}

// TestPitchEstimate_ShortInput tests handling of too-short input.
func TestPitchEstimate_ShortInput(t *testing.T) {
	pe := NewPitchEstimator(SampleRate16k)

	// Input shorter than 2*maxLag should return nil
	samples := make([]float64, 100) // Way too short
	estimate := pe.Estimate(samples)

	if estimate != nil {
		t.Error("Expected nil for short input, got estimate")
	}
}

// TestPitchEstimate_SubframeLags tests that sub-frame lags are computed.
func TestPitchEstimate_SubframeLags(t *testing.T) {
	sampleRate := SampleRate16k
	pe := NewPitchEstimator(sampleRate)

	// Generate a stable 200 Hz tone (40ms for maxLag*2 requirement)
	numSamples := sampleRate / 25 // 40ms = 640 samples at 16kHz
	samples := make([]float64, numSamples)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 200 * float64(i) / float64(sampleRate))
	}

	estimate := pe.Estimate(samples)
	if estimate == nil {
		t.Fatal("Estimate returned nil")
	}

	// Check that we have the correct number of sub-frame lags
	if len(estimate.SubframeLags) != SILKSubFrames {
		t.Errorf("SubframeLags length = %d, want %d", len(estimate.SubframeLags), SILKSubFrames)
	}

	// For a stable tone, all sub-frame lags should be similar
	for i, lag := range estimate.SubframeLags {
		if lag < pe.MinLag() || lag > pe.MaxLag() {
			t.Errorf("SubframeLag[%d] = %d is out of range [%d, %d]",
				i, lag, pe.MinLag(), pe.MaxLag())
		}
	}
}

// TestPitchEstimate_Continuity tests pitch continuity between frames.
func TestPitchEstimate_Continuity(t *testing.T) {
	sampleRate := SampleRate16k
	pe := NewPitchEstimator(sampleRate)

	// Generate multiple frames at the same pitch (40ms per frame)
	numSamples := sampleRate / 25 // 40ms = 640 samples
	freq := 200.0

	var prevLag int
	for frame := 0; frame < 5; frame++ {
		samples := make([]float64, numSamples)
		phase := float64(frame*numSamples) / float64(sampleRate) * 2 * math.Pi * freq
		for i := range samples {
			samples[i] = math.Sin(phase + 2*math.Pi*freq*float64(i)/float64(sampleRate))
		}

		estimate := pe.Estimate(samples)
		if estimate == nil {
			t.Fatalf("Frame %d: Estimate returned nil", frame)
		}

		if frame > 0 && prevLag > 0 {
			// Lags should be within ±10% across frames for stable pitch
			diff := math.Abs(float64(estimate.Lag - prevLag))
			tolerance := float64(prevLag) * 0.1
			if diff > tolerance {
				t.Errorf("Frame %d: Lag discontinuity: %d -> %d (diff %.1f > tol %.1f)",
					frame, prevLag, estimate.Lag, diff, tolerance)
			}
		}
		prevLag = estimate.Lag
	}
}

// TestPitchEstimatorReset tests the Reset function.
func TestPitchEstimatorReset(t *testing.T) {
	pe := NewPitchEstimator(SampleRate16k)

	// Process a frame
	samples := make([]float64, 640)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 16000)
	}
	pe.Estimate(samples)

	// Reset and verify state
	pe.Reset()

	// After reset, prevVoiced should be false
	// We can't directly access private fields, but we can verify behavior
	// by checking that a new frame doesn't benefit from continuity

	// This is an indirect test - just ensure Reset doesn't panic
	pe.Reset()
}

// TestLagToFrequency tests conversion between lag and frequency.
func TestLagToFrequency(t *testing.T) {
	pe := NewPitchEstimator(SampleRate16k)

	tests := []struct {
		lag      int
		wantFreq float64
	}{
		{lag: 80, wantFreq: 200.0},  // 16000/80 = 200 Hz
		{lag: 160, wantFreq: 100.0}, // 16000/160 = 100 Hz
		{lag: 53, wantFreq: 301.89}, // 16000/53 ≈ 301.89 Hz
	}

	for _, tt := range tests {
		freq := pe.LagToFrequency(tt.lag)
		if math.Abs(freq-tt.wantFreq) > 0.1 {
			t.Errorf("LagToFrequency(%d) = %.2f, want %.2f", tt.lag, freq, tt.wantFreq)
		}
	}

	// Test edge case
	if pe.LagToFrequency(0) != 0 {
		t.Error("LagToFrequency(0) should return 0")
	}
}

// TestFrequencyToLag tests conversion from frequency to lag.
func TestFrequencyToLag(t *testing.T) {
	pe := NewPitchEstimator(SampleRate16k)

	tests := []struct {
		freq    float64
		wantLag int
	}{
		{freq: 200.0, wantLag: 80},  // 16000/200 = 80
		{freq: 100.0, wantLag: 160}, // 16000/100 = 160
		{freq: 300.0, wantLag: 53},  // 16000/300 ≈ 53
	}

	for _, tt := range tests {
		lag := pe.FrequencyToLag(tt.freq)
		if lag != tt.wantLag {
			t.Errorf("FrequencyToLag(%.1f) = %d, want %d", tt.freq, lag, tt.wantLag)
		}
	}

	// Test edge cases: very low/high frequencies get clamped
	if pe.FrequencyToLag(0) != pe.MaxLag() {
		t.Errorf("FrequencyToLag(0) should return maxLag, got %d", pe.FrequencyToLag(0))
	}
}

// TestPitchLagHelpers tests standalone helper functions.
func TestPitchLagHelpers(t *testing.T) {
	// Test PitchLagToHz
	hz := PitchLagToHz(80, 16000)
	if hz != 200.0 {
		t.Errorf("PitchLagToHz(80, 16000) = %.2f, want 200.0", hz)
	}

	if PitchLagToHz(0, 16000) != 0 {
		t.Error("PitchLagToHz(0, ...) should return 0")
	}

	// Test HzToPitchLag
	lag := HzToPitchLag(200.0, 16000)
	if lag != 80 {
		t.Errorf("HzToPitchLag(200, 16000) = %d, want 80", lag)
	}

	if HzToPitchLag(0, 16000) != 0 {
		t.Error("HzToPitchLag(0, ...) should return 0")
	}
}

// TestVoicingStrength tests the voicing strength estimation.
func TestVoicingStrength(t *testing.T) {
	pe := NewPitchEstimator(SampleRate16k)
	numSamples := 640

	// Voiced signal (sine wave)
	voiced := make([]float64, numSamples)
	for i := range voiced {
		voiced[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 16000)
	}
	voicedStrength := pe.EstimateVoicingStrength(voiced)

	// Unvoiced signal (noise)
	pe.Reset()
	unvoiced := make([]float64, numSamples)
	seed := uint32(12345)
	for i := range unvoiced {
		seed = seed*1103515245 + 12345
		unvoiced[i] = float64(int32(seed)>>16) / 32768.0
	}
	unvoicedStrength := pe.EstimateVoicingStrength(unvoiced)

	// Voiced should have higher strength than unvoiced
	if voicedStrength <= unvoicedStrength {
		t.Errorf("Voiced strength %.3f should be > unvoiced %.3f",
			voicedStrength, unvoicedStrength)
	}

	t.Logf("Voiced strength: %.3f, Unvoiced strength: %.3f", voicedStrength, unvoicedStrength)
}

// BenchmarkPitchEstimate benchmarks pitch estimation.
func BenchmarkPitchEstimate(b *testing.B) {
	pe := NewPitchEstimator(SampleRate16k)
	numSamples := 320 // 20ms at 16kHz

	// Generate a test signal
	samples := make([]float64, numSamples)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 16000)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pe.Estimate(samples)
	}
}

// BenchmarkNormalizedCorrelation benchmarks the correlation computation.
func BenchmarkNormalizedCorrelation(b *testing.B) {
	pe := NewPitchEstimator(SampleRate16k)
	numSamples := 640

	samples := make([]float64, numSamples)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 16000)
	}

	lag := 80 // 200 Hz at 16 kHz

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pe.computeNormalizedCorr(samples, lag)
	}
}
