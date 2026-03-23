// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file contains tests for LPC excitation coding.

package magnum

import (
	"math"
	"testing"
)

// TestNewExcitationEncoder tests excitation encoder creation.
func TestNewExcitationEncoder(t *testing.T) {
	ee := NewExcitationEncoder(40)
	if ee == nil {
		t.Fatal("NewExcitationEncoder returned nil")
	}
	if ee.subframeLen != 40 {
		t.Errorf("subframeLen = %d, want 40", ee.subframeLen)
	}

	// Test default
	ee2 := NewExcitationEncoder(0)
	if ee2.subframeLen != ExcSubframeLength {
		t.Errorf("default subframeLen = %d, want %d", ee2.subframeLen, ExcSubframeLength)
	}
}

// TestExcitationEncode_SineWave tests encoding a sine wave residual.
func TestExcitationEncode_SineWave(t *testing.T) {
	subframeLen := 20
	ee := NewExcitationEncoder(subframeLen)

	// Generate sine wave residual
	totalLen := subframeLen * GainNumSubframes
	residual := make([]float64, totalLen)
	for i := range residual {
		residual[i] = math.Sin(2 * math.Pi * 4 * float64(i) / float64(totalLen))
	}

	exc := ee.Encode(residual, 5) // Hint: 5 pulses per subframe
	if exc == nil {
		t.Fatal("Encode returned nil")
	}

	for sf := 0; sf < GainNumSubframes; sf++ {
		if exc.Subframes[sf] == nil {
			t.Errorf("Subframe[%d] is nil", sf)
			continue
		}
		if exc.Subframes[sf].NumPulses > ExcMaxPulses {
			t.Errorf("Subframe[%d]: NumPulses = %d, max is %d",
				sf, exc.Subframes[sf].NumPulses, ExcMaxPulses)
		}
	}
}

// TestExcitationEncode_Silence tests encoding silence.
func TestExcitationEncode_Silence(t *testing.T) {
	subframeLen := 20
	ee := NewExcitationEncoder(subframeLen)

	// Zero residual
	totalLen := subframeLen * GainNumSubframes
	residual := make([]float64, totalLen)

	exc := ee.Encode(residual, 0) // Auto-determine pulses
	if exc == nil {
		t.Fatal("Encode returned nil")
	}

	// Silence should result in few or no pulses
	for sf := 0; sf < GainNumSubframes; sf++ {
		if exc.Subframes[sf] == nil {
			continue
		}
		if exc.Subframes[sf].NumPulses > 0 {
			t.Logf("Subframe[%d]: %d pulses for silence", sf, exc.Subframes[sf].NumPulses)
		}
	}
}

// TestExcitationEncode_Impulse tests encoding an impulse.
func TestExcitationEncode_Impulse(t *testing.T) {
	subframeLen := 20
	ee := NewExcitationEncoder(subframeLen)

	// Single impulse in each subframe
	totalLen := subframeLen * GainNumSubframes
	residual := make([]float64, totalLen)
	for sf := 0; sf < GainNumSubframes; sf++ {
		residual[sf*subframeLen+5] = 1.0 // Impulse at position 5
	}

	exc := ee.Encode(residual, 1) // 1 pulse per subframe
	if exc == nil {
		t.Fatal("Encode returned nil")
	}

	for sf := 0; sf < GainNumSubframes; sf++ {
		if exc.Subframes[sf] == nil {
			t.Errorf("Subframe[%d] is nil", sf)
			continue
		}
		if len(exc.Subframes[sf].Pulses) == 0 {
			t.Errorf("Subframe[%d]: no pulses found for impulse", sf)
			continue
		}
		// First pulse should be at position 5
		if exc.Subframes[sf].Pulses[0].Position != 5 {
			t.Errorf("Subframe[%d]: pulse at position %d, expected 5",
				sf, exc.Subframes[sf].Pulses[0].Position)
		}
		// Sign should be positive
		if exc.Subframes[sf].Pulses[0].Sign != 1 {
			t.Errorf("Subframe[%d]: pulse sign = %d, expected 1",
				sf, exc.Subframes[sf].Pulses[0].Sign)
		}
	}
}

// TestExcitationEncode_NegativeImpulse tests encoding a negative impulse.
func TestExcitationEncode_NegativeImpulse(t *testing.T) {
	subframeLen := 20
	ee := NewExcitationEncoder(subframeLen)

	// Single negative impulse
	totalLen := subframeLen * GainNumSubframes
	residual := make([]float64, totalLen)
	residual[10] = -1.0

	exc := ee.Encode(residual, 1)
	if exc == nil {
		t.Fatal("Encode returned nil")
	}

	if exc.Subframes[0] == nil || len(exc.Subframes[0].Pulses) == 0 {
		t.Fatal("Expected pulse in first subframe")
	}

	pulse := exc.Subframes[0].Pulses[0]
	if pulse.Position != 10 {
		t.Errorf("Pulse position = %d, expected 10", pulse.Position)
	}
	if pulse.Sign != -1 {
		t.Errorf("Pulse sign = %d, expected -1", pulse.Sign)
	}
}

// TestSynthesizeExcitation tests excitation synthesis.
func TestSynthesizeExcitation(t *testing.T) {
	subframeLen := 20
	totalLen := subframeLen * GainNumSubframes

	// Create excitation with known pulses
	exc := &ExcitationFrame{}
	for sf := 0; sf < GainNumSubframes; sf++ {
		exc.Subframes[sf] = &ExcitationSubframe{
			NumPulses: 1,
			Pulses: []ExcitationPulse{
				{Position: 5, Sign: 1, Amplitude: 1.0},
			},
		}
		exc.QuantEnergy[sf] = 4 // Middle energy
	}

	output := SynthesizeExcitation(exc, subframeLen, nil)
	if len(output) != totalLen {
		t.Errorf("Output length = %d, want %d", len(output), totalLen)
	}

	// Check that pulses appear at the right positions
	for sf := 0; sf < GainNumSubframes; sf++ {
		pulseIdx := sf*subframeLen + 5
		if math.Abs(output[pulseIdx]) < 0.001 {
			t.Errorf("Expected non-zero at position %d (subframe %d)", pulseIdx, sf)
		}
	}
}

// TestExcitationEncodeDecode tests bitstream encoding/decoding round-trip.
func TestExcitationEncodeDecode(t *testing.T) {
	subframeLen := 20

	// Create original excitation
	original := &ExcitationFrame{}
	for sf := 0; sf < GainNumSubframes; sf++ {
		original.Subframes[sf] = &ExcitationSubframe{
			NumPulses: 2,
			Pulses: []ExcitationPulse{
				{Position: 3, Sign: 1, Amplitude: 1.0},
				{Position: 12, Sign: -1, Amplitude: 1.0},
			},
		}
		original.QuantEnergy[sf] = sf + 1
	}

	// Encode
	enc := NewRangeEncoder()
	EncodeExcitationParams(enc, original, subframeLen)
	encoded := enc.Bytes()

	// Decode
	dec := NewRangeDecoder(encoded)
	decoded := DecodeExcitationParams(dec, subframeLen)

	if decoded == nil {
		t.Fatal("DecodeExcitationParams returned nil")
	}

	// Verify
	for sf := 0; sf < GainNumSubframes; sf++ {
		orig := original.Subframes[sf]
		dec := decoded.Subframes[sf]

		if dec == nil {
			t.Errorf("Subframe[%d] decoded as nil", sf)
			continue
		}

		if dec.NumPulses != orig.NumPulses {
			t.Errorf("Subframe[%d]: NumPulses = %d, want %d",
				sf, dec.NumPulses, orig.NumPulses)
		}

		if decoded.QuantEnergy[sf] != original.QuantEnergy[sf] {
			t.Errorf("Subframe[%d]: QuantEnergy = %d, want %d",
				sf, decoded.QuantEnergy[sf], original.QuantEnergy[sf])
		}

		for p := 0; p < orig.NumPulses && p < len(dec.Pulses); p++ {
			if dec.Pulses[p].Position != orig.Pulses[p].Position {
				t.Errorf("Subframe[%d] Pulse[%d]: Position = %d, want %d",
					sf, p, dec.Pulses[p].Position, orig.Pulses[p].Position)
			}
			if dec.Pulses[p].Sign != orig.Pulses[p].Sign {
				t.Errorf("Subframe[%d] Pulse[%d]: Sign = %d, want %d",
					sf, p, dec.Pulses[p].Sign, orig.Pulses[p].Sign)
			}
		}
	}
}

// TestComputeExcitationError tests error computation.
func TestComputeExcitationError(t *testing.T) {
	// Identical signals should have zero error
	sig := []float64{1.0, 2.0, 3.0, 4.0}
	err := ComputeExcitationError(sig, sig)
	if err > 1e-10 {
		t.Errorf("Error for identical signals = %f, want ~0", err)
	}

	// Zero original should return 0
	zeros := []float64{0, 0, 0, 0}
	err = ComputeExcitationError(zeros, zeros)
	if err != 0 {
		t.Errorf("Error for zero signals = %f, want 0", err)
	}

	// Different signals should have non-zero error
	sig2 := []float64{2.0, 3.0, 4.0, 5.0}
	err = ComputeExcitationError(sig, sig2)
	if err <= 0 {
		t.Errorf("Error for different signals = %f, want > 0", err)
	}
}

// TestBitsForValue tests the bit calculation function.
func TestBitsForValue(t *testing.T) {
	tests := []struct {
		n        int
		wantBits uint32
	}{
		{n: 1, wantBits: 1},
		{n: 2, wantBits: 1},
		{n: 3, wantBits: 2},
		{n: 4, wantBits: 2},
		{n: 5, wantBits: 3},
		{n: 8, wantBits: 3},
		{n: 9, wantBits: 4},
		{n: 16, wantBits: 4},
		{n: 20, wantBits: 5},
	}

	for _, tt := range tests {
		bits := bitsForValue(tt.n)
		if bits != tt.wantBits {
			t.Errorf("bitsForValue(%d) = %d, want %d", tt.n, bits, tt.wantBits)
		}
	}
}

// TestShellCoder tests shell coder operations.
func TestShellCoder(t *testing.T) {
	sc := NewShellCoder(4)
	if sc == nil {
		t.Fatal("NewShellCoder returned nil")
	}

	// Test encode/decode round-trip
	enc := NewRangeEncoder()
	sc.EncodePulseCount(enc, 5, 10)
	encoded := enc.Bytes()

	dec := NewRangeDecoder(encoded)
	count := sc.DecodePulseCount(dec, 10)
	if count != 5 {
		t.Errorf("DecodePulseCount = %d, want 5", count)
	}
}

// TestExcitationEncoderReset tests reset functionality.
func TestExcitationEncoderReset(t *testing.T) {
	ee := NewExcitationEncoder(20)
	ee.prevSeed = 123

	ee.Reset()

	if ee.prevSeed != 0 {
		t.Errorf("prevSeed after Reset = %d, want 0", ee.prevSeed)
	}
}

// BenchmarkExcitationEncode benchmarks excitation encoding.
func BenchmarkExcitationEncode(b *testing.B) {
	subframeLen := 40
	ee := NewExcitationEncoder(subframeLen)
	totalLen := subframeLen * GainNumSubframes
	residual := make([]float64, totalLen)

	for i := range residual {
		residual[i] = math.Sin(2 * math.Pi * 8 * float64(i) / float64(totalLen))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ee.Encode(residual, 5)
	}
}

// BenchmarkSynthesizeExcitation benchmarks excitation synthesis.
func BenchmarkSynthesizeExcitation(b *testing.B) {
	subframeLen := 40

	exc := &ExcitationFrame{}
	for sf := 0; sf < GainNumSubframes; sf++ {
		exc.Subframes[sf] = &ExcitationSubframe{
			NumPulses: 5,
			Pulses: []ExcitationPulse{
				{Position: 5, Sign: 1, Amplitude: 1.0},
				{Position: 10, Sign: -1, Amplitude: 0.8},
				{Position: 15, Sign: 1, Amplitude: 0.6},
				{Position: 20, Sign: -1, Amplitude: 0.5},
				{Position: 25, Sign: 1, Amplitude: 0.4},
			},
		}
		exc.QuantEnergy[sf] = 4
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SynthesizeExcitation(exc, subframeLen, nil)
	}
}
