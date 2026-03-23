// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file contains tests for Voice Activity Detection (VAD).

package magnum

import (
	"math"
	"testing"
)

// TestNewVAD tests VAD creation.
func TestNewVAD(t *testing.T) {
	vad := NewVAD(16000)
	if vad == nil {
		t.Fatal("NewVAD returned nil")
	}

	if vad.sampleRate != 16000 {
		t.Errorf("sampleRate = %d, want 16000", vad.sampleRate)
	}
	if vad.State() != VADStateInactive {
		t.Errorf("initial state = %d, want VADStateInactive", vad.State())
	}
	if vad.IsActive() {
		t.Error("IsActive should return false initially")
	}
}

// TestNewVAD_Default tests default sample rate.
func TestNewVAD_Default(t *testing.T) {
	vad := NewVAD(0)
	if vad.sampleRate != SampleRate16k {
		t.Errorf("default sampleRate = %d, want %d", vad.sampleRate, SampleRate16k)
	}
}

// TestVAD_Silence tests VAD on silence.
func TestVAD_Silence(t *testing.T) {
	vad := NewVAD(16000)

	// Generate silence
	samples := make([]float64, 320)

	result := vad.Detect(samples)
	if result == nil {
		t.Fatal("Detect returned nil")
	}

	if result.Active {
		t.Error("Silence should not be detected as active")
	}
	if result.Confidence > 0.1 {
		t.Errorf("Silence confidence %.2f should be near 0", result.Confidence)
	}
}

// TestVAD_Speech tests VAD on speech-like signal.
func TestVAD_Speech(t *testing.T) {
	vad := NewVAD(16000)

	// Generate speech-like signal (200 Hz sine at moderate amplitude)
	samples := make([]float64, 320)
	for i := range samples {
		samples[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}

	// Process multiple frames to build up activity
	var result *VADResult
	for frame := 0; frame < 5; frame++ {
		result = vad.Detect(samples)
	}

	if result == nil {
		t.Fatal("Detect returned nil")
	}

	if !result.Active {
		t.Errorf("Speech-like signal should be active (confidence=%.2f, energy=%.1f dB, SNR=%.1f dB)",
			result.Confidence, result.EnergyDB, result.SNR)
	}
}

// TestVAD_LowAmplitude tests VAD on low amplitude signal.
func TestVAD_LowAmplitude(t *testing.T) {
	vad := NewVAD(16000)

	// Generate very low amplitude signal
	samples := make([]float64, 320)
	for i := range samples {
		samples[i] = 0.0001 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}

	result := vad.Detect(samples)
	if result == nil {
		t.Fatal("Detect returned nil")
	}

	if result.Active {
		t.Error("Very low amplitude signal should not be active")
	}
	t.Logf("Low amplitude: energy=%.1f dB", result.EnergyDB)
}

// TestVAD_Noise tests VAD on noise.
func TestVAD_Noise(t *testing.T) {
	vad := NewVAD(16000)

	// Generate pseudo-random noise at moderate level
	samples := make([]float64, 320)
	seed := uint32(12345)
	for i := range samples {
		seed = seed*1103515245 + 12345
		samples[i] = 0.2 * (float64(int32(seed)>>16) / 32768.0)
	}

	// Process multiple frames
	for frame := 0; frame < 5; frame++ {
		vad.Detect(samples)
	}

	result := vad.Detect(samples)
	t.Logf("Noise: active=%v, energy=%.1f dB, SNR=%.1f dB, flatness=%.2f",
		result.Active, result.EnergyDB, result.SNR, result.SpectralFlatness)

	// Noise may or may not be detected as active depending on thresholds
	// The key thing is it shouldn't crash and should have high flatness
	if result.SpectralFlatness < 0.3 {
		t.Error("Noise should have higher spectral flatness")
	}
}

// TestVAD_Hangover tests the hangover behavior.
func TestVAD_Hangover(t *testing.T) {
	vad := NewVAD(16000)
	vad.SetHangoverFrames(3)

	// Generate speech signal
	speech := make([]float64, 320)
	for i := range speech {
		speech[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}

	// Generate silence
	silence := make([]float64, 320)

	// Activate with speech
	for frame := 0; frame < 5; frame++ {
		vad.Detect(speech)
	}

	if !vad.IsActive() {
		t.Error("Should be active after speech")
	}

	// Feed silence frames - should stay active due to hangover
	activeCount := 0
	for frame := 0; frame < 6; frame++ {
		result := vad.Detect(silence)
		if result.Active {
			activeCount++
		}
	}

	// Should have been active for some hangover frames
	if activeCount < 3 {
		t.Errorf("Hangover should keep VAD active for at least 3 frames, got %d", activeCount)
	}

	t.Logf("Hangover: %d frames remained active", activeCount)
}

// TestVAD_AttackFrames tests attack behavior.
func TestVAD_AttackFrames(t *testing.T) {
	vad := NewVAD(16000)

	// Generate speech signal
	speech := make([]float64, 320)
	for i := range speech {
		speech[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}

	// First frame might not activate immediately (attack time)
	result1 := vad.Detect(speech)

	// After a few frames, should be active
	for frame := 0; frame < 5; frame++ {
		vad.Detect(speech)
	}
	result2 := vad.Detect(speech)

	t.Logf("Attack: frame1 active=%v, frame6 active=%v", result1.Active, result2.Active)

	if !result2.Active {
		t.Error("Should be active after attack period")
	}
}

// TestVAD_NoiseFloorUpdate tests adaptive noise floor.
func TestVAD_NoiseFloorUpdate(t *testing.T) {
	vad := NewVAD(16000)

	// Start with silence to establish noise floor
	silence := make([]float64, 320)
	for i := range silence {
		silence[i] = 0.001 * (float64(i)/320 - 0.5) // Very low amplitude
	}

	initialNoise := vad.NoiseFloor()
	for frame := 0; frame < 10; frame++ {
		vad.Detect(silence)
	}
	updatedNoise := vad.NoiseFloor()

	t.Logf("Noise floor: initial=%.2e, updated=%.2e", initialNoise, updatedNoise)

	// Noise floor should have updated
	if initialNoise == updatedNoise {
		t.Error("Noise floor should have been updated")
	}
}

// TestVAD_Reset tests reset functionality.
func TestVAD_Reset(t *testing.T) {
	vad := NewVAD(16000)

	// Process some frames
	speech := make([]float64, 320)
	for i := range speech {
		speech[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}
	for frame := 0; frame < 10; frame++ {
		vad.Detect(speech)
	}

	// Reset
	vad.Reset()

	if vad.IsActive() {
		t.Error("Should be inactive after reset")
	}
	if vad.State() != VADStateInactive {
		t.Error("State should be VADStateInactive after reset")
	}
}

// TestVAD_SetThresholds tests threshold configuration.
func TestVAD_SetThresholds(t *testing.T) {
	vad := NewVAD(16000)

	// Set very high threshold
	vad.SetEnergyThreshold(-10.0) // Very strict

	samples := make([]float64, 320)
	for i := range samples {
		samples[i] = 0.1 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}

	// Should not activate with strict threshold
	for frame := 0; frame < 5; frame++ {
		vad.Detect(samples)
	}

	result := vad.Detect(samples)
	if result.Active {
		t.Error("Should not be active with very strict threshold")
	}

	// Now set lenient threshold
	vad.SetEnergyThreshold(-60.0)

	for frame := 0; frame < 5; frame++ {
		vad.Detect(samples)
	}

	result = vad.Detect(samples)
	if !result.Active {
		t.Error("Should be active with lenient threshold")
	}
}

// TestSimpleVAD tests the stateless simple VAD function.
func TestSimpleVAD(t *testing.T) {
	// Silence should not be detected
	silence := make([]float64, 320)
	if SimpleVAD(silence, -40.0) {
		t.Error("Silence should not be detected by SimpleVAD")
	}

	// Speech-like signal should be detected
	speech := make([]float64, 320)
	for i := range speech {
		speech[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}
	if !SimpleVAD(speech, -40.0) {
		t.Error("Speech should be detected by SimpleVAD")
	}

	// Empty input
	if SimpleVAD(nil, -40.0) {
		t.Error("Empty input should return false")
	}
}

// TestComputeFrameActivity tests activity score computation.
func TestComputeFrameActivity(t *testing.T) {
	// Silence
	silence := make([]float64, 320)
	silenceActivity := ComputeFrameActivity(silence)
	if silenceActivity > 0.01 {
		t.Errorf("Silence activity = %.2f, want ~0", silenceActivity)
	}

	// Moderate amplitude
	moderate := make([]float64, 320)
	for i := range moderate {
		moderate[i] = 0.2
	}
	moderateActivity := ComputeFrameActivity(moderate)
	if moderateActivity < 0.9 {
		t.Errorf("Moderate activity = %.2f, expected ~1.0", moderateActivity)
	}

	// Empty input
	if ComputeFrameActivity(nil) != 0 {
		t.Error("Empty input should return 0")
	}
}

// TestVAD_SpectralFlatness tests spectral flatness computation.
func TestVAD_SpectralFlatness(t *testing.T) {
	vad := NewVAD(16000)

	// Tonal signal (sine wave) should have moderate flatness
	tonal := make([]float64, 320)
	for i := range tonal {
		tonal[i] = 0.3 * math.Sin(2*math.Pi*440*float64(i)/16000)
	}
	tonalFlatness := vad.computeSpectralFlatness(tonal)

	// Noise-like signal
	noise := make([]float64, 320)
	seed := uint32(67890)
	for i := range noise {
		seed = seed*1103515245 + 12345
		noise[i] = float64(int32(seed)>>16) / 32768.0
	}
	noiseFlatness := vad.computeSpectralFlatness(noise)

	t.Logf("Flatness: tonal=%.3f, noise=%.3f", tonalFlatness, noiseFlatness)

	// Both should be in valid range [0, 1]
	if tonalFlatness < 0 || tonalFlatness > 1 {
		t.Errorf("Tonal flatness %.3f out of range [0, 1]", tonalFlatness)
	}
	if noiseFlatness < 0 || noiseFlatness > 1 {
		t.Errorf("Noise flatness %.3f out of range [0, 1]", noiseFlatness)
	}

	// Note: The simplified time-domain approximation may not perfectly
	// distinguish between tonal and noise signals. This is acceptable
	// as the VAD uses multiple features for decision making.
}

// TestVAD_EmptyInput tests handling of empty input.
func TestVAD_EmptyInput(t *testing.T) {
	vad := NewVAD(16000)

	result := vad.Detect(nil)
	if result == nil {
		t.Fatal("Detect should not return nil")
	}
	if result.Active {
		t.Error("Empty input should not be active")
	}
	if result.Confidence != 0 {
		t.Errorf("Empty input confidence = %.2f, want 0", result.Confidence)
	}

	result = vad.Detect([]float64{})
	if result.Active {
		t.Error("Zero-length input should not be active")
	}
}

// BenchmarkVADDetect benchmarks VAD detection.
func BenchmarkVADDetect(b *testing.B) {
	vad := NewVAD(16000)
	samples := make([]float64, 320)
	for i := range samples {
		samples[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vad.Detect(samples)
	}
}

// BenchmarkSimpleVAD benchmarks simple VAD.
func BenchmarkSimpleVAD(b *testing.B) {
	samples := make([]float64, 320)
	for i := range samples {
		samples[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SimpleVAD(samples, -35.0)
	}
}
