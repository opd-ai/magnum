package magnum

import (
	"math"
	"testing"
)

func TestSpreadingModes(t *testing.T) {
	// Verify spreading mode constants
	if SpreadNone != 0 {
		t.Errorf("SpreadNone = %d, want 0", SpreadNone)
	}
	if SpreadLight != 1 {
		t.Errorf("SpreadLight = %d, want 1", SpreadLight)
	}
	if SpreadNormal != 2 {
		t.Errorf("SpreadNormal = %d, want 2", SpreadNormal)
	}
	if SpreadAggressive != 3 {
		t.Errorf("SpreadAggressive = %d, want 3", SpreadAggressive)
	}
}

func TestSpreadingAnalyzerBasic(t *testing.T) {
	sa := NewSpreadingAnalyzer()
	if sa == nil {
		t.Fatal("NewSpreadingAnalyzer returned nil")
	}

	// Empty spectrum should return SpreadNormal
	result := sa.Analyze(nil, 0)
	if result != SpreadNormal {
		t.Errorf("Analyze(nil) = %d, want SpreadNormal (%d)", result, SpreadNormal)
	}

	result = sa.Analyze([]float64{}, 0)
	if result != SpreadNormal {
		t.Errorf("Analyze([]) = %d, want SpreadNormal (%d)", result, SpreadNormal)
	}
}

func TestSpreadingAnalyzerTonal(t *testing.T) {
	sa := NewSpreadingAnalyzer()

	// Create a tonal signal (single sinusoid in spectrum)
	// This should result in less spreading needed
	spectrum := make([]float64, 64)
	spectrum[10] = 1.0 // Single peak

	// Run multiple iterations to let the average stabilize
	var result SpreadingMode
	for i := 0; i < 10; i++ {
		result = sa.Analyze(spectrum, 21)
	}

	// Tonal signals should result in less spreading
	if result > SpreadNormal {
		t.Logf("Tonal spectrum spreading mode: %d (expected <= SpreadNormal)", result)
	}
}

func TestSpreadingAnalyzerNoise(t *testing.T) {
	sa := NewSpreadingAnalyzer()

	// Create a noise-like signal (flat spectrum)
	spectrum := make([]float64, 64)
	for i := range spectrum {
		spectrum[i] = 0.5 // Flat
	}

	// Run multiple iterations to let the average stabilize
	var result SpreadingMode
	for i := 0; i < 10; i++ {
		result = sa.Analyze(spectrum, 21)
	}

	// Noise-like signals may need more spreading
	t.Logf("Noise-like spectrum spreading mode: %d", result)
}

func TestTFAnalyzerBasic(t *testing.T) {
	ta := NewTFAnalyzer(21)
	if ta == nil {
		t.Fatal("NewTFAnalyzer returned nil")
	}

	// Create a basic band energy structure
	be := &BandEnergy{}
	for i := range be.Valid {
		be.Valid[i] = true
		be.LogEnergy[i] = -10.0
	}

	tf := ta.Analyze(be)
	if tf == nil {
		t.Fatal("Analyze returned nil")
	}
	if tf.NumBands != 21 {
		t.Errorf("NumBands = %d, want 21", tf.NumBands)
	}
	if len(tf.TF) != 21 {
		t.Errorf("len(TF) = %d, want 21", len(tf.TF))
	}
}

func TestTFAnalyzerTransient(t *testing.T) {
	ta := NewTFAnalyzer(21)

	// First frame: stable energy
	be1 := &BandEnergy{}
	for i := range be1.Valid {
		be1.Valid[i] = true
		be1.LogEnergy[i] = -10.0
	}
	ta.Analyze(be1)

	// Second frame: transient in band 5
	be2 := &BandEnergy{}
	for i := range be2.Valid {
		be2.Valid[i] = true
		be2.LogEnergy[i] = -10.0
	}
	be2.LogEnergy[5] = 0.0 // +10 dB jump

	tf := ta.Analyze(be2)

	// Band 5 should potentially use time resolution after energy jump
	// Note: may need multiple frames for accumulator to trigger
	t.Logf("TF after transient: band 5 TF=%d", tf.TF[5])
}

func TestApplyTFChange(t *testing.T) {
	// Create test spectrum
	spectrum := make([]float64, 10)
	for i := range spectrum {
		spectrum[i] = float64(i + 1)
	}
	original := make([]float64, len(spectrum))
	copy(original, spectrum)

	// Apply TF change
	tf := &TFResolution{TF: []int{1}, NumBands: 1}
	ApplyTFChange(spectrum, tf, 0, 10, true) // shortBlocks=true

	// Verify Haar transform was applied
	// After Haar, values should be different
	changed := false
	for i := range spectrum {
		if math.Abs(spectrum[i]-original[i]) > 1e-10 {
			changed = true
			break
		}
	}
	if !changed {
		t.Error("ApplyTFChange did not modify spectrum")
	}

	// Apply inverse and verify recovery
	InvertTFChange(spectrum, tf, 0, 10, true)

	for i := range spectrum {
		if math.Abs(spectrum[i]-original[i]) > 1e-6 {
			t.Errorf("InvertTFChange failed at %d: got %f, want %f", i, spectrum[i], original[i])
		}
	}
}

func TestApplyTFChangeNoOp(t *testing.T) {
	// TF=0 should be no-op
	spectrum := make([]float64, 10)
	for i := range spectrum {
		spectrum[i] = float64(i + 1)
	}
	original := make([]float64, len(spectrum))
	copy(original, spectrum)

	tf := &TFResolution{TF: []int{0}, NumBands: 1}
	ApplyTFChange(spectrum, tf, 0, 10, true)

	for i := range spectrum {
		if spectrum[i] != original[i] {
			t.Errorf("TF=0 modified spectrum at %d", i)
		}
	}
}

func TestApplySpreading(t *testing.T) {
	// Create test spectrum
	spectrum := make([]float64, 10)
	for i := range spectrum {
		spectrum[i] = float64(i+1) / 10.0
	}
	original := make([]float64, len(spectrum))
	copy(original, spectrum)

	// SpreadNone should not change spectrum
	ApplySpreading(spectrum, SpreadNone, 0, 10, 12345)
	for i := range spectrum {
		if spectrum[i] != original[i] {
			t.Error("SpreadNone modified spectrum")
		}
	}

	// SpreadNormal should modify spectrum
	copy(spectrum, original)
	ApplySpreading(spectrum, SpreadNormal, 0, 10, 12345)

	changed := false
	for i := range spectrum {
		if math.Abs(spectrum[i]-original[i]) > 1e-10 {
			changed = true
			break
		}
	}
	if !changed {
		t.Error("SpreadNormal did not modify spectrum")
	}
}

func TestRemoveSpreading(t *testing.T) {
	// Create spectrum with non-unit norm
	spectrum := []float64{3.0, 4.0, 0.0, 0.0}

	RemoveSpreading(spectrum, SpreadNormal, 0, 4)

	// Should be normalized to unit norm
	sumSq := 0.0
	for _, v := range spectrum {
		sumSq += v * v
	}
	if math.Abs(sumSq-1.0) > 1e-6 {
		t.Errorf("RemoveSpreading: norm^2 = %f, want 1.0", sumSq)
	}
}

func TestSpectralFlatness(t *testing.T) {
	// Pure tone: single nonzero value
	tone := make([]float64, 64)
	tone[10] = 1.0
	flatnessTone := computeSpectralFlatness(tone)

	// White noise: flat spectrum
	noise := make([]float64, 64)
	for i := range noise {
		noise[i] = 0.5
	}
	flatnessNoise := computeSpectralFlatness(noise)

	// Noise should be flatter than tone
	if flatnessNoise <= flatnessTone {
		t.Errorf("Flatness: noise=%f should be > tone=%f", flatnessNoise, flatnessTone)
	}

	// Flatness should be in [0,1]
	if flatnessTone < 0 || flatnessTone > 1 {
		t.Errorf("Tone flatness %f out of range [0,1]", flatnessTone)
	}
	if flatnessNoise < 0 || flatnessNoise > 1 {
		t.Errorf("Noise flatness %f out of range [0,1]", flatnessNoise)
	}
}

func TestHFRatio(t *testing.T) {
	// All energy in low frequencies
	lowFreq := make([]float64, 100)
	for i := 0; i < 25; i++ {
		lowFreq[i] = 1.0
	}
	ratioLow := computeHFRatio(lowFreq)

	// All energy in high frequencies
	highFreq := make([]float64, 100)
	for i := 75; i < 100; i++ {
		highFreq[i] = 1.0
	}
	ratioHigh := computeHFRatio(highFreq)

	if ratioHigh <= ratioLow {
		t.Errorf("HF ratio: high=%f should be > low=%f", ratioHigh, ratioLow)
	}

	// Verify ranges
	if ratioLow < 0 || ratioLow > 1 {
		t.Errorf("Low HF ratio %f out of range [0,1]", ratioLow)
	}
	if ratioHigh < 0 || ratioHigh > 1 {
		t.Errorf("High HF ratio %f out of range [0,1]", ratioHigh)
	}
}

func TestHysteresisDecision(t *testing.T) {
	thresholds := []int{100, 200, 300}
	hysteresis := []int{20, 20, 20}

	// Below first threshold
	if d := hysteresisDecision(50, thresholds, hysteresis, 0); d != 0 {
		t.Errorf("Decision for 50: got %d, want 0", d)
	}

	// Between first and second
	if d := hysteresisDecision(150, thresholds, hysteresis, 0); d != 1 {
		t.Errorf("Decision for 150: got %d, want 1", d)
	}

	// Test hysteresis: value just above threshold but prev was lower
	// Should stay at lower decision
	if d := hysteresisDecision(105, thresholds, hysteresis, 0); d != 0 {
		t.Errorf("Hysteresis up: got %d, want 0", d)
	}

	// Test hysteresis: value just below threshold but prev was higher
	if d := hysteresisDecision(195, thresholds, hysteresis, 2); d != 2 {
		t.Errorf("Hysteresis down: got %d, want 2", d)
	}
}

func TestCeltLCGRand(t *testing.T) {
	// Verify deterministic behavior
	seed1 := celtLCGRand(12345)
	seed2 := celtLCGRand(12345)
	if seed1 != seed2 {
		t.Error("celtLCGRand not deterministic")
	}

	// Verify different seeds produce different outputs
	seed3 := celtLCGRand(12346)
	if seed1 == seed3 {
		t.Error("celtLCGRand produced same output for different seeds")
	}

	// Verify known value from reference implementation
	// celt_lcg_rand(0) = 1013904223
	if celtLCGRand(0) != 1013904223 {
		t.Errorf("celtLCGRand(0) = %d, want 1013904223", celtLCGRand(0))
	}
}

func TestEncodeDecodeTFSelect(t *testing.T) {
	// Create TF resolution
	tf := &TFResolution{
		TF:       []int{1, 0, 1, 0, 1},
		NumBands: 5,
	}

	// Test transient mode (per-band encoding)
	enc := NewRangeEncoder()
	EncodeTFSelect(enc, tf, true)
	data := enc.Bytes()

	dec := NewRangeDecoder(data)
	decoded := DecodeTFSelect(dec, 5, true)

	for i := 0; i < 5; i++ {
		if decoded.TF[i] != tf.TF[i] {
			t.Errorf("Transient TF[%d]: got %d, want %d", i, decoded.TF[i], tf.TF[i])
		}
	}

	// Test non-transient mode (uniform encoding)
	enc2 := NewRangeEncoder()
	EncodeTFSelect(enc2, tf, false)
	data2 := enc2.Bytes()

	dec2 := NewRangeDecoder(data2)
	decoded2 := DecodeTFSelect(dec2, 5, false)

	// Non-transient should have uniform TF (all same as first)
	expected := tf.TF[0]
	for i := 0; i < 5; i++ {
		if decoded2.TF[i] != expected {
			t.Errorf("Non-transient TF[%d]: got %d, want %d", i, decoded2.TF[i], expected)
		}
	}
}

func TestEncodeDecodeSpread(t *testing.T) {
	modes := []SpreadingMode{SpreadNone, SpreadLight, SpreadNormal, SpreadAggressive}

	for _, mode := range modes {
		enc := NewRangeEncoder()
		EncodeSpread(enc, mode)
		data := enc.Bytes()

		dec := NewRangeDecoder(data)
		decoded := DecodeSpread(dec)

		if decoded != mode {
			t.Errorf("Spread mode: got %d, want %d", decoded, mode)
		}
	}
}

func BenchmarkSpreadingAnalyze(b *testing.B) {
	sa := NewSpreadingAnalyzer()
	spectrum := make([]float64, 480)
	for i := range spectrum {
		spectrum[i] = float64(i%10) / 10.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sa.Analyze(spectrum, 21)
	}
}

func BenchmarkApplyTFChange(b *testing.B) {
	spectrum := make([]float64, 480)
	for i := range spectrum {
		spectrum[i] = float64(i)
	}
	tf := &TFResolution{TF: []int{1}, NumBands: 1}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ApplyTFChange(spectrum, tf, 0, 480, true)
	}
}

func BenchmarkApplySpreading(b *testing.B) {
	spectrum := make([]float64, 480)
	for i := range spectrum {
		spectrum[i] = float64(i) / 480.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ApplySpreading(spectrum, SpreadNormal, 0, 480, uint32(i))
	}
}
