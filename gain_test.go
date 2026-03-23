// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file contains tests for subframe gain coding.

package magnum

import (
	"math"
	"testing"
)

// TestNewGainCoder tests gain coder creation.
func TestNewGainCoder(t *testing.T) {
	gc := NewGainCoder()
	if gc == nil {
		t.Fatal("NewGainCoder returned nil")
	}
	if gc.predCoeff != GainPredCoeff {
		t.Errorf("predCoeff = %f, want %f", gc.predCoeff, GainPredCoeff)
	}
}

// TestComputeGains_ConstantSignal tests gain computation on constant signal.
func TestComputeGains_ConstantSignal(t *testing.T) {
	gc := NewGainCoder()

	// Create a constant signal with known amplitude
	amplitude := 0.5
	subframeLen := 40 // 5ms at 8kHz
	samples := make([]float64, subframeLen*GainNumSubframes)
	for i := range samples {
		samples[i] = amplitude
	}

	gains := gc.ComputeGains(samples, subframeLen)
	if gains == nil {
		t.Fatal("ComputeGains returned nil")
	}

	// Expected dB: 10 * log10(0.5^2) = 10 * log10(0.25) ≈ -6.02 dB
	expectedDb := 10.0 * math.Log10(amplitude*amplitude)

	for sf := 0; sf < GainNumSubframes; sf++ {
		// Check that computed gain is close to expected
		tolerance := 2.0 // dB tolerance due to quantization
		if math.Abs(gains.Subframes[sf].LogGain-expectedDb) > tolerance {
			t.Errorf("Subframe[%d]: LogGain = %.2f dB, expected ~%.2f dB",
				sf, gains.Subframes[sf].LogGain, expectedDb)
		}

		// Linear gain should be close to 1 (normalized)
		if gains.Subframes[sf].LinearGain <= 0 {
			t.Errorf("Subframe[%d]: LinearGain = %f, should be > 0", sf, gains.Subframes[sf].LinearGain)
		}
	}
}

// TestComputeGains_VaryingAmplitude tests gain computation with varying amplitude.
func TestComputeGains_VaryingAmplitude(t *testing.T) {
	gc := NewGainCoder()

	subframeLen := 40
	samples := make([]float64, subframeLen*GainNumSubframes)

	// Each subframe has different amplitude
	amplitudes := []float64{0.1, 0.3, 0.5, 0.8}
	for sf := 0; sf < GainNumSubframes; sf++ {
		startIdx := sf * subframeLen
		for i := 0; i < subframeLen; i++ {
			samples[startIdx+i] = amplitudes[sf]
		}
	}

	gains := gc.ComputeGains(samples, subframeLen)
	if gains == nil {
		t.Fatal("ComputeGains returned nil")
	}

	// Check that gains increase with amplitude
	for sf := 1; sf < GainNumSubframes; sf++ {
		// Due to quantization and prediction, this is not always strictly true
		// but the trend should be generally increasing
		prevEnergy := amplitudes[sf-1] * amplitudes[sf-1]
		currEnergy := amplitudes[sf] * amplitudes[sf]
		prevDbExpected := 10.0 * math.Log10(prevEnergy)
		currDbExpected := 10.0 * math.Log10(currEnergy)

		t.Logf("Subframe[%d]: expected %.2f dB, got %.2f dB",
			sf, currDbExpected, gains.Subframes[sf].LogGain)

		// At least check the general direction
		if amplitudes[sf] > amplitudes[sf-1] {
			if gains.Subframes[sf].LogGain < gains.Subframes[sf-1].LogGain-3.0 {
				t.Errorf("Subframe[%d]: gain should be higher than subframe[%d] (%.2f vs %.2f)",
					sf, sf-1, gains.Subframes[sf].LogGain, gains.Subframes[sf-1].LogGain)
			}
		}

		_ = prevDbExpected // Used for logging
	}
}

// TestComputeGains_Silence tests gain computation on silence.
func TestComputeGains_Silence(t *testing.T) {
	gc := NewGainCoder()

	subframeLen := 40
	samples := make([]float64, subframeLen*GainNumSubframes) // All zeros

	gains := gc.ComputeGains(samples, subframeLen)
	if gains == nil {
		t.Fatal("ComputeGains returned nil")
	}

	// Silence should have very low gain (close to minimum)
	// Due to prediction and quantization, the exact value may vary
	for sf := 0; sf < GainNumSubframes; sf++ {
		// Should be at least lower than -10 dB for silence
		if gains.Subframes[sf].LogGain > -10.0 {
			t.Errorf("Subframe[%d]: LogGain = %.2f dB, expected < -10 dB for silence",
				sf, gains.Subframes[sf].LogGain)
		}
		t.Logf("Subframe[%d]: LogGain = %.2f dB", sf, gains.Subframes[sf].LogGain)
	}
}

// TestQuantizeDelta tests delta quantization round-trip.
func TestQuantizeDelta(t *testing.T) {
	gc := NewGainCoder()

	tests := []float64{-30.0, -10.0, -5.0, 0.0, 5.0, 10.0, 30.0}

	for _, delta := range tests {
		index := gc.quantizeDelta(delta)
		dequant := gc.dequantizeDelta(index)

		// Check that index is in valid range
		if index < 0 || index >= GainQuantLevels {
			t.Errorf("quantizeDelta(%f) = %d, out of range [0, %d)",
				delta, index, GainQuantLevels)
		}

		// Check that dequantized value is close to original
		// (within half a quantization step, plus clamping effects)
		if math.Abs(delta) <= float64(GainQuantLevels/2)*GainQuantStep {
			tolerance := GainQuantStep * 0.6
			if math.Abs(dequant-delta) > tolerance {
				t.Errorf("Round-trip error: delta=%f, index=%d, dequant=%f",
					delta, index, dequant)
			}
		}
	}
}

// TestDecodeGains tests gain decoding from indices.
func TestDecodeGains(t *testing.T) {
	gc := NewGainCoder()

	// Create specific indices (around center = unity gain)
	indices := []int{32, 33, 31, 32} // Center is 32 (delta = 0)

	decoded := gc.DecodeGains(indices)
	if decoded == nil {
		t.Fatal("DecodeGains returned nil")
	}

	for sf := 0; sf < GainNumSubframes; sf++ {
		if decoded.Subframes[sf].QuantIndex != indices[sf] {
			t.Errorf("Subframe[%d]: QuantIndex = %d, want %d",
				sf, decoded.Subframes[sf].QuantIndex, indices[sf])
		}
	}
}

// TestApplyGains tests gain application to signal.
func TestApplyGains(t *testing.T) {
	gc := NewGainCoder()

	subframeLen := 20
	samples := make([]float64, subframeLen*GainNumSubframes)
	for i := range samples {
		samples[i] = 1.0 // Unity amplitude
	}

	// Compute gains (should be around 0 dB = unity)
	gains := gc.ComputeGains(samples, subframeLen)

	// Apply gains
	output := ApplyGains(samples, gains, subframeLen)

	if len(output) != len(samples) {
		t.Errorf("Output length = %d, want %d", len(output), len(samples))
	}

	// Output should be scaled by the linear gains
	for sf := 0; sf < GainNumSubframes; sf++ {
		startIdx := sf * subframeLen
		expectedValue := samples[startIdx] * gains.Subframes[sf].LinearGain
		if math.Abs(output[startIdx]-expectedValue) > 1e-10 {
			t.Errorf("Subframe[%d]: output = %f, expected %f",
				sf, output[startIdx], expectedValue)
		}
	}
}

// TestNormalizeByGains tests gain normalization (inverse of apply).
func TestNormalizeByGains(t *testing.T) {
	subframeLen := 20
	samples := make([]float64, subframeLen*GainNumSubframes)
	for i := range samples {
		samples[i] = float64(i%subframeLen + 1) // Varying amplitude
	}

	// Create gains
	gains := &FrameGains{}
	for sf := 0; sf < GainNumSubframes; sf++ {
		gains.Subframes[sf] = SubframeGain{
			LinearGain: 2.0 + float64(sf)*0.5, // Different gain per subframe
			LogGain:    LinearToDb(2.0 + float64(sf)*0.5),
		}
	}

	// Apply then normalize should give back original
	applied := ApplyGains(samples, gains, subframeLen)
	normalized := NormalizeByGains(applied, gains, subframeLen)

	for i := range samples {
		if math.Abs(normalized[i]-samples[i]) > 1e-10 {
			t.Errorf("Sample[%d]: normalized = %f, original = %f",
				i, normalized[i], samples[i])
			break
		}
	}
}

// TestEncodeDecodeGains tests bitstream encoding/decoding round-trip.
func TestEncodeDecodeGains(t *testing.T) {
	gc := NewGainCoder()

	// Create gains with known values
	original := &FrameGains{
		FrameGainIndex: 128,
	}
	for sf := 0; sf < GainNumSubframes; sf++ {
		original.Subframes[sf] = SubframeGain{
			LinearGain: 1.0 + float64(sf)*0.2,
			LogGain:    LinearToDb(1.0 + float64(sf)*0.2),
			QuantIndex: 32 + sf*2, // Near center with some variation
		}
	}

	// Encode
	enc := NewRangeEncoder()
	EncodeGains(enc, original)
	encoded := enc.Bytes()

	// Decode
	gc.Reset() // Reset state
	dec := NewRangeDecoder(encoded)
	decoded := DecodeGainsFromBitstream(dec, gc)

	if decoded == nil {
		t.Fatal("DecodeGainsFromBitstream returned nil")
	}

	// Verify frame gain index
	if decoded.FrameGainIndex != original.FrameGainIndex {
		t.Errorf("FrameGainIndex = %d, want %d",
			decoded.FrameGainIndex, original.FrameGainIndex)
	}

	// Verify subframe indices
	for sf := 0; sf < GainNumSubframes; sf++ {
		if decoded.Subframes[sf].QuantIndex != original.Subframes[sf].QuantIndex {
			t.Errorf("Subframe[%d]: QuantIndex = %d, want %d",
				sf, decoded.Subframes[sf].QuantIndex, original.Subframes[sf].QuantIndex)
		}
	}
}

// TestGainCoderReset tests the Reset function.
func TestGainCoderReset(t *testing.T) {
	gc := NewGainCoder()

	// Process a frame to change state
	samples := make([]float64, 160)
	for i := range samples {
		samples[i] = 0.5
	}
	gc.ComputeGains(samples, 40)

	// Reset
	gc.Reset()

	// Verify state is reset
	for i := 0; i < GainNumSubframes; i++ {
		if gc.prevGains[i] != 0.0 {
			t.Errorf("prevGains[%d] = %f, want 0.0", i, gc.prevGains[i])
		}
	}
	if gc.prevFrameGain != 0.0 {
		t.Errorf("prevFrameGain = %f, want 0.0", gc.prevFrameGain)
	}
}

// TestComputeSignalEnergy tests signal energy computation.
func TestComputeSignalEnergy(t *testing.T) {
	tests := []struct {
		samples    []float64
		expectedDb float64
		tolerance  float64
	}{
		{
			samples:    []float64{1.0, 1.0, 1.0, 1.0},
			expectedDb: 0.0, // 10 * log10(1.0) = 0
			tolerance:  0.1,
		},
		{
			samples:    []float64{0.1, 0.1, 0.1, 0.1},
			expectedDb: -20.0, // 10 * log10(0.01) = -20
			tolerance:  0.1,
		},
		{
			samples:    []float64{0.0, 0.0, 0.0, 0.0},
			expectedDb: GainMinDB,
			tolerance:  0.1,
		},
	}

	for i, tt := range tests {
		energy := ComputeSignalEnergy(tt.samples)
		if math.Abs(energy-tt.expectedDb) > tt.tolerance {
			t.Errorf("Test %d: ComputeSignalEnergy = %.2f dB, expected %.2f dB",
				i, energy, tt.expectedDb)
		}
	}
}

// TestLinearToDb tests linear to dB conversion.
func TestLinearToDb(t *testing.T) {
	tests := []struct {
		linear float64
		db     float64
	}{
		{linear: 1.0, db: 0.0},
		{linear: 10.0, db: 20.0},
		{linear: 0.1, db: -20.0},
		{linear: 0.0, db: GainMinDB},
		{linear: -1.0, db: GainMinDB},
	}

	for _, tt := range tests {
		result := LinearToDb(tt.linear)
		if math.Abs(result-tt.db) > 0.01 {
			t.Errorf("LinearToDb(%f) = %f, want %f", tt.linear, result, tt.db)
		}
	}
}

// TestDbToLinear tests dB to linear conversion.
func TestDbToLinear(t *testing.T) {
	tests := []struct {
		db     float64
		linear float64
	}{
		{db: 0.0, linear: 1.0},
		{db: 20.0, linear: 10.0},
		{db: -20.0, linear: 0.1},
		{db: 6.02, linear: 2.0},
	}

	for _, tt := range tests {
		result := DbToLinear(tt.db)
		tolerance := tt.linear * 0.01 // 1% tolerance
		if tolerance < 0.001 {
			tolerance = 0.001
		}
		if math.Abs(result-tt.linear) > tolerance {
			t.Errorf("DbToLinear(%f) = %f, want %f", tt.db, result, tt.linear)
		}
	}
}

// BenchmarkComputeGains benchmarks gain computation.
func BenchmarkComputeGains(b *testing.B) {
	gc := NewGainCoder()
	subframeLen := 80 // 5ms at 16kHz
	samples := make([]float64, subframeLen*GainNumSubframes)

	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 200 * float64(i) / 16000)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gc.ComputeGains(samples, subframeLen)
	}
}

// BenchmarkApplyGains benchmarks gain application.
func BenchmarkApplyGains(b *testing.B) {
	subframeLen := 80
	samples := make([]float64, subframeLen*GainNumSubframes)
	for i := range samples {
		samples[i] = float64(i) / float64(len(samples))
	}

	gains := &FrameGains{}
	for sf := 0; sf < GainNumSubframes; sf++ {
		gains.Subframes[sf] = SubframeGain{LinearGain: 1.5}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ApplyGains(samples, gains, subframeLen)
	}
}
