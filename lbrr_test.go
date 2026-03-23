// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file contains tests for Low Bit-Rate Redundancy (LBRR) FEC encoding.

package magnum

import (
	"testing"
)

// TestNewLBRREncoder tests LBRR encoder creation.
func TestNewLBRREncoder(t *testing.T) {
	config := DefaultLBRRConfig()
	enc := NewLBRREncoder(16000, 1, config)

	if enc == nil {
		t.Fatal("NewLBRREncoder returned nil")
	}
	if enc.IsEnabled() {
		t.Error("LBRR should be disabled by default")
	}
	if enc.Mode() != LBRRModeOff {
		t.Errorf("Mode = %d, want LBRRModeOff", enc.Mode())
	}
}

// TestDefaultLBRRConfig tests default configuration values.
func TestDefaultLBRRConfig(t *testing.T) {
	config := DefaultLBRRConfig()

	if config.Mode != LBRRModeOff {
		t.Errorf("Default Mode = %d, want LBRRModeOff", config.Mode)
	}
	if config.PacketLossPercentage != 0 {
		t.Errorf("Default PacketLossPercentage = %d, want 0", config.PacketLossPercentage)
	}
	if config.ThresholdPLC <= 0 {
		t.Error("ThresholdPLC should be positive")
	}
}

// TestLBRR_SetMode tests mode setting.
func TestLBRR_SetMode(t *testing.T) {
	enc := NewLBRREncoder(16000, 1, DefaultLBRRConfig())

	modes := []LBRRMode{LBRRModeLow, LBRRModeMedium, LBRRModeHigh, LBRRModeOff}
	expectedBitrates := []int{6000, 8000, 12000, 0}

	for i, mode := range modes {
		enc.SetMode(mode)
		if enc.Mode() != mode {
			t.Errorf("SetMode(%d): Mode = %d, want %d", mode, enc.Mode(), mode)
		}
		if enc.TargetBitrate() != expectedBitrates[i] {
			t.Errorf("SetMode(%d): TargetBitrate = %d, want %d",
				mode, enc.TargetBitrate(), expectedBitrates[i])
		}
	}
}

// TestLBRR_IsEnabled tests enabled state.
func TestLBRR_IsEnabled(t *testing.T) {
	enc := NewLBRREncoder(16000, 1, DefaultLBRRConfig())

	if enc.IsEnabled() {
		t.Error("Should be disabled initially")
	}

	enc.SetMode(LBRRModeLow)
	if !enc.IsEnabled() {
		t.Error("Should be enabled after SetMode(LBRRModeLow)")
	}

	enc.SetMode(LBRRModeOff)
	if enc.IsEnabled() {
		t.Error("Should be disabled after SetMode(LBRRModeOff)")
	}
}

// TestLBRR_EncodeLBRR_Disabled tests that encoding returns nil when disabled.
func TestLBRR_EncodeLBRR_Disabled(t *testing.T) {
	enc := NewLBRREncoder(16000, 1, DefaultLBRRConfig())

	// Store some frame data
	frameData := &LBRRFrameData{
		LPCCoeffs: []float64{0.5, 0.3, 0.2, 0.1},
		Gains:     []float64{0.8, 0.7, 0.6, 0.5},
		VADFlag:   true,
	}
	enc.StorePrimaryFrame(frameData)

	// Should return nil when disabled
	lbrr := enc.EncodeLBRR()
	if lbrr != nil {
		t.Error("EncodeLBRR should return nil when disabled")
	}
}

// TestLBRR_EncodeLBRR_NoData tests encoding with no stored data.
func TestLBRR_EncodeLBRR_NoData(t *testing.T) {
	config := DefaultLBRRConfig()
	config.Mode = LBRRModeLow
	enc := NewLBRREncoder(16000, 1, config)

	// Should return nil with no stored frames
	lbrr := enc.EncodeLBRR()
	if lbrr != nil {
		t.Error("EncodeLBRR should return nil with no stored frames")
	}
}

// TestLBRR_EncodeDecode tests LBRR encode/decode round-trip.
func TestLBRR_EncodeDecode(t *testing.T) {
	config := DefaultLBRRConfig()
	config.Mode = LBRRModeMedium
	enc := NewLBRREncoder(16000, 1, config)

	// Create frame data
	frameData := &LBRRFrameData{
		LPCCoeffs: []float64{0.5, 0.3, -0.2, 0.1},
		Gains:     []float64{0.8, 0.7, 0.6, 0.5},
		PitchLags: []int{150, 150, 152, 152},
		VADFlag:   true,
		Energy:    0.5,
	}

	// Store frame and encode LBRR
	enc.StorePrimaryFrame(frameData)
	lbrr := enc.EncodeLBRR()

	if lbrr == nil {
		t.Fatal("EncodeLBRR returned nil")
	}
	if !lbrr.Valid {
		t.Error("LBRR frame should be valid")
	}
	if lbrr.Index != -1 {
		t.Errorf("LBRR Index = %d, want -1", lbrr.Index)
	}
	if !lbrr.Flags.VADFlag {
		t.Error("LBRR VADFlag should be true")
	}
	if len(lbrr.Data) == 0 {
		t.Error("LBRR Data should not be empty")
	}

	t.Logf("LBRR data size: %d bytes", len(lbrr.Data))

	// Decode and verify
	decoded, err := DecodeLBRR(lbrr.Data)
	if err != nil {
		t.Fatalf("DecodeLBRR error: %v", err)
	}
	if decoded == nil {
		t.Fatal("DecodeLBRR returned nil")
	}

	if decoded.VADFlag != frameData.VADFlag {
		t.Errorf("Decoded VADFlag = %v, want %v", decoded.VADFlag, frameData.VADFlag)
	}
	if len(decoded.Gains) != len(frameData.Gains) {
		t.Errorf("Decoded Gains length = %d, want %d",
			len(decoded.Gains), len(frameData.Gains))
	}

	// Gains should be approximately equal after quantization
	for i, gain := range decoded.Gains {
		diff := gain - frameData.Gains[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > 0.02 { // Allow small quantization error
			t.Errorf("Decoded Gains[%d] = %.3f, want ~%.3f", i, gain, frameData.Gains[i])
		}
	}

	if len(decoded.PitchLags) == 0 {
		t.Error("Decoded PitchLags should not be empty for voiced frame")
	}
}

// TestLBRR_UnvoicedFrame tests LBRR encoding for unvoiced frame.
func TestLBRR_UnvoicedFrame(t *testing.T) {
	config := DefaultLBRRConfig()
	config.Mode = LBRRModeLow
	enc := NewLBRREncoder(16000, 1, config)

	// Create unvoiced frame data
	frameData := &LBRRFrameData{
		Gains:   []float64{0.3, 0.3, 0.3, 0.3},
		VADFlag: false,
	}

	enc.StorePrimaryFrame(frameData)
	lbrr := enc.EncodeLBRR()

	if lbrr == nil {
		t.Fatal("EncodeLBRR returned nil")
	}
	if lbrr.Flags.VADFlag {
		t.Error("LBRR VADFlag should be false for unvoiced")
	}

	// Decode
	decoded, err := DecodeLBRR(lbrr.Data)
	if err != nil {
		t.Fatalf("DecodeLBRR error: %v", err)
	}
	if decoded.VADFlag {
		t.Error("Decoded VADFlag should be false")
	}
}

// TestLBRR_MultipleFrames tests storing multiple frames.
func TestLBRR_MultipleFrames(t *testing.T) {
	config := DefaultLBRRConfig()
	config.Mode = LBRRModeLow
	enc := NewLBRREncoder(16000, 1, config)

	// Store two frames
	frame1 := &LBRRFrameData{
		Gains:   []float64{0.5, 0.5, 0.5, 0.5},
		VADFlag: true,
	}
	frame2 := &LBRRFrameData{
		Gains:   []float64{0.8, 0.8, 0.8, 0.8},
		VADFlag: false,
	}

	enc.StorePrimaryFrame(frame1)
	lbrr1 := enc.EncodeLBRR()
	if lbrr1 == nil {
		t.Fatal("First LBRR should not be nil")
	}
	// First LBRR encodes frame1
	if lbrr1.Flags.VADFlag != true {
		t.Error("First LBRR should have VADFlag from frame1")
	}

	enc.StorePrimaryFrame(frame2)
	lbrr2 := enc.EncodeLBRR()
	if lbrr2 == nil {
		t.Fatal("Second LBRR should not be nil")
	}

	// Second LBRR encodes frame2 (the most recently stored frame before current)
	// Circular buffer: after storing frame2, prevIndex points to frame2
	if lbrr2.Flags.VADFlag != false {
		t.Error("Second LBRR should have VADFlag from frame2")
	}
}

// TestLBRR_PacketLossAutoEnable tests automatic LBRR enable on packet loss.
func TestLBRR_PacketLossAutoEnable(t *testing.T) {
	enc := NewLBRREncoder(16000, 1, DefaultLBRRConfig())

	if enc.IsEnabled() {
		t.Error("Should start disabled")
	}

	// Set low packet loss - should not enable
	enc.SetPacketLossPercentage(2)
	if enc.IsEnabled() {
		t.Error("Should not enable with 2% loss")
	}

	// Set moderate packet loss - should auto-enable LBRRModeLow
	enc.SetMode(LBRRModeOff) // Reset
	enc.SetPacketLossPercentage(5)
	if !enc.IsEnabled() {
		t.Error("Should auto-enable with 5% loss")
	}
	if enc.Mode() != LBRRModeLow {
		t.Errorf("Mode = %d, want LBRRModeLow with moderate loss", enc.Mode())
	}

	// Set high packet loss - should auto-enable LBRRModeMedium
	enc.SetMode(LBRRModeOff) // Reset
	enc.SetPacketLossPercentage(10)
	if enc.Mode() != LBRRModeMedium {
		t.Errorf("Mode = %d, want LBRRModeMedium with 10%% loss", enc.Mode())
	}

	// Set very high packet loss - should auto-enable LBRRModeHigh
	enc.SetMode(LBRRModeOff) // Reset
	enc.SetPacketLossPercentage(20)
	if enc.Mode() != LBRRModeHigh {
		t.Errorf("Mode = %d, want LBRRModeHigh with 20%% loss", enc.Mode())
	}
}

// TestLBRR_Reset tests reset functionality.
func TestLBRR_Reset(t *testing.T) {
	config := DefaultLBRRConfig()
	config.Mode = LBRRModeLow
	enc := NewLBRREncoder(16000, 1, config)

	// Store some data
	enc.StorePrimaryFrame(&LBRRFrameData{
		Gains:   []float64{0.5, 0.5, 0.5, 0.5},
		VADFlag: true,
	})

	// Reset
	enc.Reset()

	// Should have no data
	lbrr := enc.EncodeLBRR()
	if lbrr != nil {
		t.Error("Should have no LBRR data after reset")
	}
}

// TestLBRR_DecodeLBRR_Empty tests decoding empty data.
func TestLBRR_DecodeLBRR_Empty(t *testing.T) {
	decoded, err := DecodeLBRR(nil)
	if err != nil {
		t.Errorf("DecodeLBRR(nil) error: %v", err)
	}
	if decoded != nil {
		t.Error("DecodeLBRR(nil) should return nil")
	}

	decoded, err = DecodeLBRR([]byte{})
	if err != nil {
		t.Errorf("DecodeLBRR([]) error: %v", err)
	}
	if decoded != nil {
		t.Error("DecodeLBRR([]) should return nil")
	}
}

// TestLBRR_SetConfig tests configuration update.
func TestLBRR_SetConfig(t *testing.T) {
	enc := NewLBRREncoder(16000, 1, DefaultLBRRConfig())

	newConfig := LBRRConfig{
		Mode:                 LBRRModeHigh,
		PacketLossPercentage: 10,
		ThresholdPLC:         5,
	}
	enc.SetConfig(newConfig)

	cfg := enc.Config()
	if cfg.Mode != LBRRModeHigh {
		t.Errorf("Config Mode = %d, want LBRRModeHigh", cfg.Mode)
	}
	if cfg.PacketLossPercentage != 10 {
		t.Errorf("Config PacketLossPercentage = %d, want 10", cfg.PacketLossPercentage)
	}
	if cfg.ThresholdPLC != 5 {
		t.Errorf("Config ThresholdPLC = %d, want 5", cfg.ThresholdPLC)
	}
}

// BenchmarkLBRREncode benchmarks LBRR encoding.
func BenchmarkLBRREncode(b *testing.B) {
	config := DefaultLBRRConfig()
	config.Mode = LBRRModeMedium
	enc := NewLBRREncoder(16000, 1, config)

	frameData := &LBRRFrameData{
		LPCCoeffs: []float64{0.5, 0.3, -0.2, 0.1},
		Gains:     []float64{0.8, 0.7, 0.6, 0.5},
		PitchLags: []int{150, 150, 152, 152},
		VADFlag:   true,
	}

	enc.StorePrimaryFrame(frameData)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc.EncodeLBRR()
	}
}

// BenchmarkLBRRDecode benchmarks LBRR decoding.
func BenchmarkLBRRDecode(b *testing.B) {
	config := DefaultLBRRConfig()
	config.Mode = LBRRModeMedium
	enc := NewLBRREncoder(16000, 1, config)

	frameData := &LBRRFrameData{
		LPCCoeffs: []float64{0.5, 0.3, -0.2, 0.1},
		Gains:     []float64{0.8, 0.7, 0.6, 0.5},
		PitchLags: []int{150, 150, 152, 152},
		VADFlag:   true,
	}

	enc.StorePrimaryFrame(frameData)
	lbrr := enc.EncodeLBRR()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DecodeLBRR(lbrr.Data)
	}
}
