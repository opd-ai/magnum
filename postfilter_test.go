package magnum

import (
	"math"
	"testing"
)

func TestNewPostFilter(t *testing.T) {
	pf := NewPostFilter(0) // Use default max period
	if pf == nil {
		t.Fatal("NewPostFilter returned nil")
	}
	if pf.maxPeriod != maxPitchPeriod {
		t.Errorf("maxPeriod = %d, want %d", pf.maxPeriod, maxPitchPeriod)
	}
	if pf.IsEnabled() {
		t.Error("filter should be disabled by default")
	}

	pf2 := NewPostFilter(512)
	if pf2.maxPeriod != 512 {
		t.Errorf("maxPeriod = %d, want 512", pf2.maxPeriod)
	}
}

func TestPostFilterEnableDisable(t *testing.T) {
	pf := NewPostFilter(0)

	pf.SetEnabled(true)
	if !pf.IsEnabled() {
		t.Error("filter should be enabled")
	}

	pf.SetEnabled(false)
	if pf.IsEnabled() {
		t.Error("filter should be disabled")
	}
}

func TestPostFilterReset(t *testing.T) {
	pf := NewPostFilter(0)
	pf.SetEnabled(true)
	pf.state.prevGain = 0.5
	pf.state.prevPeriod = 100

	pf.Reset()

	if pf.IsEnabled() {
		t.Error("filter should be disabled after reset")
	}
	if pf.state.prevGain != 0 {
		t.Error("prevGain should be 0 after reset")
	}
	if pf.state.prevPeriod != minPitchPeriod {
		t.Errorf("prevPeriod = %d, want %d", pf.state.prevPeriod, minPitchPeriod)
	}
}

func TestPostFilterApplyDisabled(t *testing.T) {
	pf := NewPostFilter(0)
	samples := []float64{1.0, 2.0, 3.0, 4.0}
	original := make([]float64, len(samples))
	copy(original, samples)

	config := &PostFilterConfig{
		Period: 100,
		Gain:   0.5,
		Tapset: 0,
	}

	// Filter is disabled, samples should not change
	pf.Apply(samples, config)

	for i, v := range samples {
		if v != original[i] {
			t.Errorf("sample[%d] changed when filter disabled", i)
		}
	}
}

func TestPostFilterApplyZeroGain(t *testing.T) {
	pf := NewPostFilter(0)
	pf.SetEnabled(true)

	samples := []float64{1.0, 2.0, 3.0, 4.0}
	original := make([]float64, len(samples))
	copy(original, samples)

	config := &PostFilterConfig{
		Period: 100,
		Gain:   0,
		Tapset: 0,
	}

	// Zero gain, samples should not change
	pf.Apply(samples, config)

	for i, v := range samples {
		if v != original[i] {
			t.Errorf("sample[%d] changed with zero gain", i)
		}
	}
}

func TestPostFilterApply(t *testing.T) {
	pf := NewPostFilter(0)
	pf.SetEnabled(true)

	// Create a simple periodic signal
	n := 200
	period := 50
	samples := make([]float64, n)
	for i := 0; i < n; i++ {
		samples[i] = math.Sin(2 * math.Pi * float64(i) / float64(period))
	}

	config := &PostFilterConfig{
		Period: period,
		Gain:   0.5,
		Tapset: 0,
	}

	// Apply the filter
	pf.Apply(samples, config)

	// Verify that samples were modified
	// (exact values depend on filter implementation)
	modified := false
	for i := 0; i < n; i++ {
		expected := math.Sin(2 * math.Pi * float64(i) / float64(period))
		if math.Abs(samples[i]-expected) > 1e-10 {
			modified = true
			break
		}
	}
	if !modified {
		t.Error("samples were not modified by filter")
	}
}

func TestPostFilterPreFilterPostFilterRoundTrip(t *testing.T) {
	// Test that pre-filter followed by post-filter approximates original
	preFilter := NewPostFilter(0)
	preFilter.SetEnabled(true)

	postFilter := NewPostFilter(0)
	postFilter.SetEnabled(true)

	// Create a simple signal
	n := 500
	period := 100
	original := make([]float64, n)
	for i := 0; i < n; i++ {
		original[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(period))
	}

	// Copy for processing
	samples := make([]float64, n)
	copy(samples, original)

	config := &PostFilterConfig{
		Period: period,
		Gain:   0.3,
		Tapset: 1,
	}

	// Apply pre-filter then post-filter
	preFilter.ApplyPreFilter(samples, config)
	postFilter.Apply(samples, config)

	// Check that result is close to original (within tolerance)
	// Note: Perfect reconstruction is not expected due to filter design
	maxDiff := 0.0
	for i := period + 10; i < n; i++ { // Skip initial transient
		diff := math.Abs(samples[i] - original[i])
		if diff > maxDiff {
			maxDiff = diff
		}
	}

	// Allow some deviation since the filters are not perfect inverses
	if maxDiff > 0.5 {
		t.Errorf("max difference = %f, expected < 0.5", maxDiff)
	}
}

func TestAnalyzePitchNoSignal(t *testing.T) {
	// Test with silence
	samples := make([]float64, 2048)
	config := AnalyzePitch(samples, 48000, minPitchPeriod, maxPitchPeriod)
	if config != nil {
		t.Error("expected nil config for silence")
	}
}

func TestAnalyzePitchShortSignal(t *testing.T) {
	// Test with too short signal
	samples := make([]float64, 100)
	for i := range samples {
		samples[i] = math.Sin(float64(i))
	}
	config := AnalyzePitch(samples, 48000, minPitchPeriod, maxPitchPeriod)
	if config != nil {
		t.Error("expected nil config for short signal")
	}
}

func TestAnalyzePitchPeriodicSignal(t *testing.T) {
	// Create a periodic signal
	period := 100
	n := period * 20 // Multiple periods
	samples := make([]float64, n)
	for i := 0; i < n; i++ {
		samples[i] = math.Sin(2 * math.Pi * float64(i) / float64(period))
	}

	config := AnalyzePitch(samples, 48000, minPitchPeriod, 512)
	if config == nil {
		t.Fatal("expected non-nil config for periodic signal")
	}

	// Period should be close to actual
	if math.Abs(float64(config.Period-period)) > 2 {
		t.Errorf("period = %d, want ~%d", config.Period, period)
	}

	// Gain should be positive
	if config.Gain <= 0 {
		t.Errorf("gain = %f, want > 0", config.Gain)
	}

	// Tapset should be valid
	if config.Tapset < 0 || config.Tapset > 2 {
		t.Errorf("tapset = %d, want 0-2", config.Tapset)
	}
}

func TestAnalyzePitchNoisySignal(t *testing.T) {
	// Create a noisy signal (low correlation)
	n := 2048
	samples := make([]float64, n)
	seed := uint32(12345)
	for i := 0; i < n; i++ {
		seed = celtLCGRand(seed)
		samples[i] = float64(int32(seed)) / float64(1<<31)
	}

	config := AnalyzePitch(samples, 48000, minPitchPeriod, 512)
	// May or may not detect periodicity in noise, but shouldn't crash
	_ = config
}

func TestComputeNormalizedCorrelation(t *testing.T) {
	// Test with identical delayed copies (should have correlation ~1)
	n := 200
	period := 50
	samples := make([]float64, n)
	for i := 0; i < n; i++ {
		samples[i] = math.Sin(2 * math.Pi * float64(i) / float64(period))
	}

	corr := computeNormalizedCorrelation(samples, period)
	if corr < 0.9 {
		t.Errorf("correlation = %f, want > 0.9 for periodic signal", corr)
	}

	// Test with zero lag (should be 1)
	corr0 := computeNormalizedCorrelation(samples, 0)
	if math.Abs(corr0-1.0) > 0.01 {
		t.Errorf("correlation at lag 0 = %f, want ~1.0", corr0)
	}
}

func TestSelectTapset(t *testing.T) {
	// Create a signal and test tapset selection
	period := 80
	n := period * 10
	samples := make([]float64, n)
	for i := 0; i < n; i++ {
		samples[i] = math.Sin(2 * math.Pi * float64(i) / float64(period))
	}

	tapset := selectTapset(samples, period)
	if tapset < 0 || tapset > 2 {
		t.Errorf("tapset = %d, want 0-2", tapset)
	}
}

func TestClampInt(t *testing.T) {
	tests := []struct {
		value, min, max, expected int
	}{
		{5, 0, 10, 5},
		{-5, 0, 10, 0},
		{15, 0, 10, 10},
		{0, 0, 10, 0},
		{10, 0, 10, 10},
	}

	for _, tc := range tests {
		result := clampInt(tc.value, tc.min, tc.max)
		if result != tc.expected {
			t.Errorf("clampInt(%d, %d, %d) = %d, want %d",
				tc.value, tc.min, tc.max, result, tc.expected)
		}
	}
}

func TestEncodeDecodePostFilter(t *testing.T) {
	testCases := []struct {
		name    string
		config  *PostFilterConfig
		enabled bool
	}{
		{
			name:    "disabled",
			config:  nil,
			enabled: false,
		},
		{
			name: "enabled with typical values",
			config: &PostFilterConfig{
				Period: 200,
				Gain:   0.5,
				Tapset: 1,
			},
			enabled: true,
		},
		{
			name: "enabled with min values",
			config: &PostFilterConfig{
				Period: minPitchPeriod,
				Gain:   0.15,
				Tapset: 0,
			},
			enabled: true,
		},
		{
			name: "enabled with max values",
			config: &PostFilterConfig{
				Period: maxPitchPeriod,
				Gain:   1.0,
				Tapset: 2,
			},
			enabled: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode
			enc := NewRangeEncoder()
			EncodePostFilter(enc, tc.config, tc.enabled)
			data := enc.Bytes()

			// Decode
			dec := NewRangeDecoder(data)
			decodedConfig, decodedEnabled := DecodePostFilter(dec)

			// Verify enabled flag
			expectedEnabled := tc.enabled && tc.config != nil && tc.config.Gain > 0.1
			if decodedEnabled != expectedEnabled {
				t.Errorf("enabled = %v, want %v", decodedEnabled, expectedEnabled)
			}

			if !decodedEnabled {
				return
			}

			// Verify parameters (with some tolerance for quantization)
			if decodedConfig.Period != tc.config.Period {
				t.Errorf("period = %d, want %d", decodedConfig.Period, tc.config.Period)
			}

			gainDiff := math.Abs(decodedConfig.Gain - tc.config.Gain)
			if gainDiff > 0.15 { // 3-bit quantization allows some error
				t.Errorf("gain = %f, want ~%f", decodedConfig.Gain, tc.config.Gain)
			}

			if decodedConfig.Tapset != tc.config.Tapset {
				t.Errorf("tapset = %d, want %d", decodedConfig.Tapset, tc.config.Tapset)
			}
		})
	}
}

func TestTapsetCoefficients(t *testing.T) {
	// Verify tapset coefficients are reasonable
	for i, taps := range tapsets {
		sum := 0.0
		for _, tap := range taps {
			if tap < 0 || tap > 1 {
				t.Errorf("tapset %d has invalid coefficient: %f", i, tap)
			}
			sum += tap
		}
		// Sum should be reasonable (not too large)
		if sum > 1.5 {
			t.Errorf("tapset %d has large sum: %f", i, sum)
		}
	}
}

func BenchmarkPostFilterApply(b *testing.B) {
	pf := NewPostFilter(0)
	pf.SetEnabled(true)

	n := 960
	period := 100
	samples := make([]float64, n)
	for i := 0; i < n; i++ {
		samples[i] = math.Sin(2 * math.Pi * float64(i) / float64(period))
	}

	config := &PostFilterConfig{
		Period: period,
		Gain:   0.5,
		Tapset: 1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pf.Reset()
		pf.SetEnabled(true)
		pf.Apply(samples, config)
	}
}

func BenchmarkAnalyzePitch(b *testing.B) {
	n := 2048
	period := 100
	samples := make([]float64, n)
	for i := 0; i < n; i++ {
		samples[i] = math.Sin(2 * math.Pi * float64(i) / float64(period))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AnalyzePitch(samples, 48000, minPitchPeriod, 512)
	}
}
