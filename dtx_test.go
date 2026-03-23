// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file contains tests for Discontinuous Transmission (DTX).

package magnum

import (
	"math"
	"testing"
)

// TestNewDTX tests DTX creation.
func TestNewDTX(t *testing.T) {
	config := DefaultDTXConfig()
	config.Enabled = true
	dtx := NewDTX(16000, config)

	if dtx == nil {
		t.Fatal("NewDTX returned nil")
	}
	if dtx.State() != DTXStateActive {
		t.Errorf("initial state = %d, want DTXStateActive", dtx.State())
	}
	if !dtx.IsEnabled() {
		t.Error("DTX should be enabled")
	}
}

// TestDTX_Disabled tests that DTX always transmits when disabled.
func TestDTX_Disabled(t *testing.T) {
	config := DefaultDTXConfig()
	config.Enabled = false
	dtx := NewDTX(16000, config)

	// Generate silence
	silence := make([]float64, 320)

	// Should always transmit when disabled
	for i := 0; i < 20; i++ {
		decision := dtx.Process(silence)
		if !decision.Transmit {
			t.Errorf("frame %d: should transmit when DTX disabled", i)
		}
	}

	transmitted, suppressed := dtx.Stats()
	if suppressed != 0 {
		t.Errorf("suppressed = %d, want 0 when disabled", suppressed)
	}
	if transmitted != 20 {
		t.Errorf("transmitted = %d, want 20", transmitted)
	}
}

// TestDTX_SilenceSuppression tests that DTX suppresses silence frames.
func TestDTX_SilenceSuppression(t *testing.T) {
	config := DefaultDTXConfig()
	config.Enabled = true
	config.HangoverFrames = 3
	dtx := NewDTX(16000, config)

	// Generate speech signal first
	speech := make([]float64, 320)
	for i := range speech {
		speech[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}

	// Generate silence
	silence := make([]float64, 320)

	// Start with speech to establish active state
	for i := 0; i < 5; i++ {
		decision := dtx.Process(speech)
		if !decision.Transmit {
			t.Errorf("speech frame %d: should transmit", i)
		}
	}

	if dtx.State() != DTXStateActive {
		t.Errorf("state after speech = %d, want DTXStateActive", dtx.State())
	}

	// Now send silence - should go through hangover then suppression
	transmitCount := 0
	suppressCount := 0
	for i := 0; i < 20; i++ {
		decision := dtx.Process(silence)
		if decision.Transmit {
			transmitCount++
		} else {
			suppressCount++
		}
	}

	// Should have transmitted during hangover, then suppressed
	if transmitCount == 0 {
		t.Error("should have transmitted some hangover frames")
	}
	if suppressCount == 0 {
		t.Error("should have suppressed some silence frames")
	}
	if dtx.State() != DTXStateInactive {
		t.Errorf("state after silence = %d, want DTXStateInactive", dtx.State())
	}

	t.Logf("Silence test: transmitted=%d, suppressed=%d", transmitCount, suppressCount)
}

// TestDTX_SpeechResumption tests that DTX resumes transmission for speech.
func TestDTX_SpeechResumption(t *testing.T) {
	config := DefaultDTXConfig()
	config.Enabled = true
	config.HangoverFrames = 2
	config.MinActiveFrames = 2
	dtx := NewDTX(16000, config)

	speech := make([]float64, 320)
	for i := range speech {
		speech[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}
	silence := make([]float64, 320)

	// Establish active state with speech
	for i := 0; i < 5; i++ {
		dtx.Process(speech)
	}

	// Go inactive with silence
	for i := 0; i < 10; i++ {
		dtx.Process(silence)
	}

	if dtx.State() != DTXStateInactive {
		t.Errorf("state = %d, want DTXStateInactive", dtx.State())
	}

	// Resume with speech - should transmit
	for i := 0; i < 5; i++ {
		decision := dtx.Process(speech)
		if !decision.Transmit {
			t.Errorf("resumed speech frame %d: should transmit", i)
		}
	}

	if dtx.State() != DTXStateActive {
		t.Errorf("state after speech resumption = %d, want DTXStateActive", dtx.State())
	}
}

// TestDTX_Hangover tests the hangover behavior.
func TestDTX_Hangover(t *testing.T) {
	config := DefaultDTXConfig()
	config.Enabled = true
	config.HangoverFrames = 5
	dtx := NewDTX(16000, config)

	speech := make([]float64, 320)
	for i := range speech {
		speech[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}
	silence := make([]float64, 320)

	// Establish active state
	for i := 0; i < 5; i++ {
		dtx.Process(speech)
	}

	// Send silence - count frames before suppression starts
	hangoverCount := 0
	for i := 0; i < 20; i++ {
		decision := dtx.Process(silence)
		if decision.State == DTXStateHangover || (decision.State == DTXStateActive && !decision.VADResult.Active) {
			hangoverCount++
		}
		if decision.State == DTXStateInactive {
			break
		}
	}

	// Should have gone through hangover frames
	t.Logf("Hangover frames: %d (config: %d)", hangoverCount, config.HangoverFrames)
	if hangoverCount < config.HangoverFrames {
		t.Errorf("hangover count %d < configured %d", hangoverCount, config.HangoverFrames)
	}
}

// TestDTX_Stats tests statistics tracking.
func TestDTX_Stats(t *testing.T) {
	config := DefaultDTXConfig()
	config.Enabled = true
	config.HangoverFrames = 2
	dtx := NewDTX(16000, config)

	speech := make([]float64, 320)
	for i := range speech {
		speech[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}
	silence := make([]float64, 320)

	// Process some speech frames
	for i := 0; i < 10; i++ {
		dtx.Process(speech)
	}

	// Process some silence frames
	for i := 0; i < 20; i++ {
		dtx.Process(silence)
	}

	transmitted, suppressed := dtx.Stats()
	total := transmitted + suppressed

	if total != 30 {
		t.Errorf("total frames = %d, want 30", total)
	}

	ratio := dtx.SuppressionRatio()
	t.Logf("Stats: transmitted=%d, suppressed=%d, ratio=%.2f", transmitted, suppressed, ratio)

	if suppressed == 0 {
		t.Error("should have suppressed some frames")
	}
	if ratio < 0 || ratio > 1 {
		t.Errorf("suppression ratio %.2f out of range [0, 1]", ratio)
	}
}

// TestDTX_Reset tests reset functionality.
func TestDTX_Reset(t *testing.T) {
	config := DefaultDTXConfig()
	config.Enabled = true
	dtx := NewDTX(16000, config)

	speech := make([]float64, 320)
	for i := range speech {
		speech[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}

	// Process some frames
	for i := 0; i < 10; i++ {
		dtx.Process(speech)
	}

	dtx.Reset()

	if dtx.State() != DTXStateActive {
		t.Errorf("state after reset = %d, want DTXStateActive", dtx.State())
	}

	transmitted, suppressed := dtx.Stats()
	if transmitted != 0 || suppressed != 0 {
		t.Errorf("stats after reset: transmitted=%d, suppressed=%d, want 0, 0",
			transmitted, suppressed)
	}
}

// TestDTX_ProcessInt16 tests the int16 convenience method.
func TestDTX_ProcessInt16(t *testing.T) {
	config := DefaultDTXConfig()
	config.Enabled = true
	dtx := NewDTX(16000, config)

	// Generate speech signal as int16
	samples := make([]int16, 320)
	for i := range samples {
		samples[i] = int16(9830 * math.Sin(2*math.Pi*200*float64(i)/16000)) // ~0.3 amplitude
	}

	decision := dtx.ProcessInt16(samples)
	if decision == nil {
		t.Fatal("ProcessInt16 returned nil")
	}
	if decision.VADResult == nil {
		t.Error("VADResult should not be nil")
	}
}

// TestDTX_Config tests configuration methods.
func TestDTX_Config(t *testing.T) {
	dtx := NewDTX(16000, DefaultDTXConfig())

	// Test SetEnabled
	dtx.SetEnabled(true)
	if !dtx.IsEnabled() {
		t.Error("DTX should be enabled")
	}

	dtx.SetEnabled(false)
	if dtx.IsEnabled() {
		t.Error("DTX should be disabled")
	}

	// Test SetHangoverFrames
	dtx.SetHangoverFrames(10)
	if dtx.Config().HangoverFrames != 10 {
		t.Errorf("HangoverFrames = %d, want 10", dtx.Config().HangoverFrames)
	}

	// Test SetConfig
	newConfig := DTXConfig{
		Enabled:         true,
		HangoverFrames:  7,
		MinActiveFrames: 3,
	}
	dtx.SetConfig(newConfig)

	cfg := dtx.Config()
	if !cfg.Enabled || cfg.HangoverFrames != 7 || cfg.MinActiveFrames != 3 {
		t.Errorf("Config mismatch: got %+v", cfg)
	}
}

// TestDTX_VADAccess tests access to underlying VAD.
func TestDTX_VADAccess(t *testing.T) {
	dtx := NewDTX(16000, DefaultDTXConfig())

	vad := dtx.VAD()
	if vad == nil {
		t.Fatal("VAD() returned nil")
	}

	// Should be able to configure VAD
	vad.SetEnergyThreshold(-40.0)
	vad.SetHangoverFrames(10)
}

// TestDTX_SuppressionRatio_Empty tests suppression ratio with no frames.
func TestDTX_SuppressionRatio_Empty(t *testing.T) {
	dtx := NewDTX(16000, DefaultDTXConfig())

	ratio := dtx.SuppressionRatio()
	if ratio != 0 {
		t.Errorf("suppression ratio with no frames = %.2f, want 0", ratio)
	}
}

// TestDefaultDTXConfig tests default configuration values.
func TestDefaultDTXConfig(t *testing.T) {
	config := DefaultDTXConfig()

	if config.Enabled {
		t.Error("DTX should be disabled by default")
	}
	if config.HangoverFrames <= 0 {
		t.Error("HangoverFrames should be positive")
	}
	if config.MinActiveFrames <= 0 {
		t.Error("MinActiveFrames should be positive")
	}
}

// BenchmarkDTXProcess benchmarks DTX processing.
func BenchmarkDTXProcess(b *testing.B) {
	config := DefaultDTXConfig()
	config.Enabled = true
	dtx := NewDTX(16000, config)

	samples := make([]float64, 320)
	for i := range samples {
		samples[i] = 0.3 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dtx.Process(samples)
	}
}

// BenchmarkDTXProcessInt16 benchmarks DTX processing with int16 input.
func BenchmarkDTXProcessInt16(b *testing.B) {
	config := DefaultDTXConfig()
	config.Enabled = true
	dtx := NewDTX(16000, config)

	samples := make([]int16, 320)
	for i := range samples {
		samples[i] = int16(9830 * math.Sin(2*math.Pi*200*float64(i)/16000))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dtx.ProcessInt16(samples)
	}
}
