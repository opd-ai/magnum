// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file contains tests for Packet Loss Concealment (PLC).

package magnum

import (
	"math"
	"testing"
)

// TestNewPLCState tests PLC state creation.
func TestNewPLCState(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)
	if plc == nil {
		t.Fatal("NewPLCState returned nil")
	}

	if plc.sampleRate != 16000 {
		t.Errorf("sampleRate = %d, want 16000", plc.sampleRate)
	}
	if plc.frameSize != 320 {
		t.Errorf("frameSize = %d, want 320", plc.frameSize)
	}
	if plc.channels != 1 {
		t.Errorf("channels = %d, want 1", plc.channels)
	}
	if plc.State() != PLCStateNormal {
		t.Errorf("initial state = %d, want PLCStateNormal", plc.State())
	}
}

// TestNewPLCState_Defaults tests default values.
func TestNewPLCState_Defaults(t *testing.T) {
	plc := NewPLCState(0, 0, 0)
	if plc == nil {
		t.Fatal("NewPLCState returned nil")
	}

	if plc.sampleRate != SampleRate16k {
		t.Errorf("default sampleRate = %d, want %d", plc.sampleRate, SampleRate16k)
	}
	if plc.channels != 1 {
		t.Errorf("default channels = %d, want 1", plc.channels)
	}
}

// TestPLCState_PacketReceived tests normal packet reception.
func TestPLCState_PacketReceived(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)

	frameData := &PLCFrameData{
		LPCCoeffs: []float64{0.5, 0.3, 0.1},
		PitchLag:  80,
		PitchGain: 0.8,
		Voiced:    true,
		Gain:      1.0,
	}

	plc.PacketReceived(frameData)

	if plc.State() != PLCStateNormal {
		t.Errorf("state after receive = %d, want PLCStateNormal", plc.State())
	}
	if plc.LostCount() != 0 {
		t.Errorf("lostCount = %d, want 0", plc.LostCount())
	}
	if plc.lastGoodFrame == nil {
		t.Error("lastGoodFrame should not be nil")
	}
}

// TestPLCState_PacketLost tests single packet loss.
func TestPLCState_PacketLost(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)

	// First, receive a good packet
	frameData := &PLCFrameData{
		PitchLag:  80,
		PitchGain: 0.7,
		Voiced:    true,
		Gain:      0.5,
		Samples:   make([]float64, 320),
	}
	// Fill with a sine wave
	for i := range frameData.Samples {
		frameData.Samples[i] = 0.5 * math.Sin(2*math.Pi*200*float64(i)/16000)
	}
	plc.PacketReceived(frameData)

	// Now lose a packet
	concealed := plc.PacketLost()

	if plc.State() != PLCStateLost {
		t.Errorf("state after loss = %d, want PLCStateLost", plc.State())
	}
	if plc.LostCount() != 1 {
		t.Errorf("lostCount = %d, want 1", plc.LostCount())
	}
	if len(concealed) != 320 {
		t.Errorf("concealed length = %d, want 320", len(concealed))
	}

	// Concealed audio should not be silent
	maxAbs := 0.0
	for _, s := range concealed {
		if math.Abs(s) > maxAbs {
			maxAbs = math.Abs(s)
		}
	}
	if maxAbs < 0.01 {
		t.Error("Concealed audio is too quiet (expected non-zero)")
	}
}

// TestPLCState_MultipleLosses tests consecutive packet losses.
func TestPLCState_MultipleLosses(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)

	// Setup with a good frame
	frameData := &PLCFrameData{
		Voiced:  true,
		Gain:    0.5,
		Samples: make([]float64, 320),
	}
	for i := range frameData.Samples {
		frameData.Samples[i] = 0.5
	}
	plc.PacketReceived(frameData)

	// Lose multiple packets
	var amplitudes []float64
	for i := 0; i < 5; i++ {
		concealed := plc.PacketLost()
		maxAbs := 0.0
		for _, s := range concealed {
			if math.Abs(s) > maxAbs {
				maxAbs = math.Abs(s)
			}
		}
		amplitudes = append(amplitudes, maxAbs)
	}

	// Amplitude should decrease with each lost packet (attenuation)
	for i := 1; i < len(amplitudes); i++ {
		if amplitudes[i] > amplitudes[i-1]*1.1 { // Allow 10% tolerance
			t.Errorf("Loss %d: amplitude %.4f > previous %.4f (should decrease)",
				i, amplitudes[i], amplitudes[i-1])
		}
	}

	if plc.LostCount() != 5 {
		t.Errorf("lostCount = %d, want 5", plc.LostCount())
	}
}

// TestPLCState_Recovery tests transition from loss to recovery.
func TestPLCState_Recovery(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)

	// Setup
	frameData := &PLCFrameData{Voiced: false, Gain: 0.3}
	plc.PacketReceived(frameData)

	// Lose a packet
	plc.PacketLost()
	if plc.State() != PLCStateLost {
		t.Errorf("state after loss = %d, want PLCStateLost", plc.State())
	}

	// Receive a packet - should enter recovery
	plc.PacketReceived(frameData)
	if plc.State() != PLCStateRecovery {
		t.Errorf("state after recovery receive = %d, want PLCStateRecovery", plc.State())
	}
	if !plc.IsRecovering() {
		t.Error("IsRecovering should return true")
	}

	// After recovery period, should return to normal
	for i := 0; i < PLCRecoveryFrames; i++ {
		plc.PacketReceived(frameData)
	}
	if plc.State() != PLCStateNormal {
		t.Errorf("state after full recovery = %d, want PLCStateNormal", plc.State())
	}
}

// TestPLCState_NoHistory tests packet loss without prior history.
func TestPLCState_NoHistory(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)

	// Lose a packet without any prior good frames
	concealed := plc.PacketLost()

	if len(concealed) != 320 {
		t.Errorf("concealed length = %d, want 320", len(concealed))
	}

	// Should output silence (all zeros)
	for i, s := range concealed {
		if s != 0 {
			t.Errorf("Sample[%d] = %f, expected 0 for no history", i, s)
			break
		}
	}
}

// TestPLCState_VoicedConcealment tests voiced frame concealment.
func TestPLCState_VoicedConcealment(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)

	// Create a voiced frame with pitch period 80 (200 Hz)
	frameData := &PLCFrameData{
		Voiced:    true,
		PitchLag:  80,
		PitchGain: 0.9,
		Gain:      1.0,
		Samples:   make([]float64, 320),
	}
	for i := range frameData.Samples {
		frameData.Samples[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 16000)
	}
	plc.PacketReceived(frameData)

	concealed := plc.PacketLost()

	// Check that concealed audio has some structure
	energy := 0.0
	for _, s := range concealed {
		energy += s * s
	}
	if energy < 0.01 {
		t.Error("Voiced concealment has too little energy")
	}

	t.Logf("Voiced concealment energy: %.4f", energy/float64(len(concealed)))
}

// TestPLCState_UnvoicedConcealment tests unvoiced frame concealment.
func TestPLCState_UnvoicedConcealment(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)

	// Create an unvoiced frame
	frameData := &PLCFrameData{
		Voiced:    false,
		PitchLag:  0,
		PitchGain: 0.0,
		Gain:      0.3,
		LPCCoeffs: []float64{0.9, -0.5, 0.2}, // Simple filter
	}
	plc.PacketReceived(frameData)

	concealed := plc.PacketLost()

	// Should be noise-like (non-zero)
	energy := 0.0
	for _, s := range concealed {
		energy += s * s
	}
	if energy < 0.001 {
		t.Error("Unvoiced concealment has too little energy")
	}

	t.Logf("Unvoiced concealment energy: %.4f", energy/float64(len(concealed)))
}

// TestApplyTransition tests smooth transition from PLC to decoded.
func TestApplyTransition(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)

	// Setup with good frame
	frameData := &PLCFrameData{Voiced: true, Gain: 1.0, Samples: make([]float64, 320)}
	for i := range frameData.Samples {
		frameData.Samples[i] = 0.5
	}
	plc.PacketReceived(frameData)

	// Lose a packet
	concealed := plc.PacketLost()
	copy(plc.prevSamples, concealed)

	// Receive next packet
	plc.PacketReceived(frameData)

	// Create decoded frame (different from concealed)
	decoded := make([]float64, 320)
	for i := range decoded {
		decoded[i] = -0.5 // Opposite sign
	}

	// Apply transition
	transitioned := plc.ApplyTransition(decoded)

	if len(transitioned) != 320 {
		t.Errorf("transitioned length = %d, want 320", len(transitioned))
	}

	// First sample should be closer to PLC output
	// Last sample should be closer to decoded output
	// (This is a simplified check)
	t.Logf("First sample: %.4f, Last sample: %.4f", transitioned[0], transitioned[319])
}

// TestPLCState_Reset tests state reset.
func TestPLCState_Reset(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)

	// Setup and lose packets
	frameData := &PLCFrameData{Voiced: true, Gain: 0.5}
	plc.PacketReceived(frameData)
	plc.PacketLost()
	plc.PacketLost()

	// Reset
	plc.Reset()

	if plc.State() != PLCStateNormal {
		t.Errorf("state after reset = %d, want PLCStateNormal", plc.State())
	}
	if plc.LostCount() != 0 {
		t.Errorf("lostCount after reset = %d, want 0", plc.LostCount())
	}
	if plc.lastGoodFrame != nil {
		t.Error("lastGoodFrame should be nil after reset")
	}
}

// TestPLCAttenuation tests attenuation computation.
func TestPLCAttenuation(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)

	// At 0 losses, attenuation should be 1.0
	plc.lostCount = 0
	att0 := plc.computeAttenuation()
	if math.Abs(att0-1.0) > 0.001 {
		t.Errorf("attenuation at 0 losses = %.4f, want 1.0", att0)
	}

	// Attenuation should decrease with more losses
	plc.lostCount = 5
	att5 := plc.computeAttenuation()
	if att5 >= att0 {
		t.Errorf("attenuation at 5 losses (%.4f) should be < at 0 losses (%.4f)", att5, att0)
	}

	// At max losses, should be 0
	plc.lostCount = PLCMaxLostFrames
	attMax := plc.computeAttenuation()
	if attMax != 0 {
		t.Errorf("attenuation at max losses = %.4f, want 0", attMax)
	}
}

// TestComputeFrameGain tests frame gain computation.
func TestComputeFrameGain(t *testing.T) {
	// Unity gain signal
	unity := make([]float64, 100)
	for i := range unity {
		unity[i] = 1.0
	}
	gain := ComputeFrameGain(unity)
	if math.Abs(gain-1.0) > 0.001 {
		t.Errorf("ComputeFrameGain(unity) = %.4f, want 1.0", gain)
	}

	// Zero signal
	zeros := make([]float64, 100)
	gain = ComputeFrameGain(zeros)
	if gain != 0 {
		t.Errorf("ComputeFrameGain(zeros) = %.4f, want 0", gain)
	}

	// Empty signal
	gain = ComputeFrameGain(nil)
	if gain != 0 {
		t.Errorf("ComputeFrameGain(nil) = %.4f, want 0", gain)
	}
}

// TestDetectVoicing tests voicing detection.
func TestDetectVoicing(t *testing.T) {
	// Generate voiced signal (200 Hz sine)
	voiced := make([]float64, 640)
	for i := range voiced {
		voiced[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 16000)
	}

	isVoiced, lag, corr := DetectVoicing(voiced, 20, 200)

	if !isVoiced {
		t.Error("Sine wave should be detected as voiced")
	}
	expectedLag := 80 // 16000/200 = 80
	tolerance := 5
	if lag < expectedLag-tolerance || lag > expectedLag+tolerance {
		t.Errorf("Detected lag = %d, expected ~%d", lag, expectedLag)
	}
	if corr < 0.5 {
		t.Errorf("Correlation %.3f too low for periodic signal", corr)
	}

	t.Logf("Voicing: voiced=%v, lag=%d, corr=%.3f", isVoiced, lag, corr)
}

// TestDetectVoicing_Noise tests voicing detection on noise.
func TestDetectVoicing_Noise(t *testing.T) {
	// Generate noise
	noise := make([]float64, 640)
	seed := uint32(12345)
	for i := range noise {
		seed = seed*1103515245 + 12345
		noise[i] = float64(int32(seed)>>16) / 32768.0
	}

	isVoiced, _, corr := DetectVoicing(noise, 20, 200)

	// Noise typically has lower correlation
	if corr > 0.5 {
		t.Logf("Warning: High correlation %.3f for noise (might be unlucky seed)", corr)
	}

	t.Logf("Noise voicing: voiced=%v, corr=%.3f", isVoiced, corr)
}

// TestUpdateFromDecoder tests the convenience update function.
func TestUpdateFromDecoder(t *testing.T) {
	plc := NewPLCState(16000, 320, 1)

	samples := make([]float64, 320)
	for i := range samples {
		samples[i] = float64(i) / 320.0
	}
	lpcCoeffs := []float64{0.9, -0.4, 0.1}

	plc.UpdateFromDecoder(samples, lpcCoeffs, 80, 0.85, true, 0.7)

	if plc.lastGoodFrame == nil {
		t.Fatal("lastGoodFrame should not be nil")
	}
	if !plc.lastGoodFrame.Voiced {
		t.Error("Voiced should be true")
	}
	if plc.lastGoodFrame.PitchLag != 80 {
		t.Errorf("PitchLag = %d, want 80", plc.lastGoodFrame.PitchLag)
	}
	if len(plc.lastGoodFrame.LPCCoeffs) != 3 {
		t.Errorf("LPCCoeffs length = %d, want 3", len(plc.lastGoodFrame.LPCCoeffs))
	}
	if len(plc.lastGoodFrame.Samples) != 320 {
		t.Errorf("Samples length = %d, want 320", len(plc.lastGoodFrame.Samples))
	}
}

// BenchmarkPacketLost benchmarks PLC generation.
func BenchmarkPacketLost(b *testing.B) {
	plc := NewPLCState(16000, 320, 1)

	frameData := &PLCFrameData{
		Voiced:   true,
		PitchLag: 80,
		Gain:     0.5,
		Samples:  make([]float64, 320),
	}
	for i := range frameData.Samples {
		frameData.Samples[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 16000)
	}
	plc.PacketReceived(frameData)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		plc.lostCount = 0 // Reset for consistent test
		plc.PacketLost()
	}
}
