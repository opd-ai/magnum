// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements the CELT pitch pre-filter and post-filter as specified
// in RFC 6716 §4.3. The pre-filter is applied at the encoder to enhance
// periodic (voiced) components, making them more resilient to quantization
// noise. The post-filter at the decoder reverses this effect.
//
// The filter is a comb filter: y[n] = x[n] + g * x[n - T]
// where T is the pitch period and g is the gain.

package magnum

import (
	"math"
)

// PostFilterConfig holds configuration for the pitch post-filter.
type PostFilterConfig struct {
	// Period is the estimated pitch period in samples (16-1024)
	Period int
	// Gain is the filter gain (0.0 to 1.0)
	Gain float64
	// Tapset selects the filter tap configuration (0, 1, or 2)
	Tapset int
}

// PostFilterState maintains state for the pitch post-filter.
type PostFilterState struct {
	// history holds the delay line for the comb filter
	history []float64
	// historyPos is the write position in the circular buffer
	historyPos int
	// prevGain is the gain from the previous frame (for smoothing)
	prevGain float64
	// prevPeriod is the period from the previous frame
	prevPeriod int
}

// PostFilter implements the CELT pitch pre/post-filter.
type PostFilter struct {
	// maxPeriod is the maximum pitch period supported
	maxPeriod int
	// state holds the filter state
	state *PostFilterState
	// enabled indicates if filtering is active
	enabled bool
}

// Tapset coefficients for the 3-tap pitch filter.
// These define the relative weights of adjacent samples around the pitch lag.
// tapsets[tapset][tap] where tap is -1, 0, +1
var tapsets = [][]float64{
	{0.3066406, 0.2170410, 0.1296387}, // Tapset 0: Smooth
	{0.4638672, 0.2680664, 0.0000000}, // Tapset 1: Medium
	{0.7998047, 0.1000977, 0.0000000}, // Tapset 2: Sharp
}

// Minimum and maximum pitch periods (in samples at 48kHz)
const (
	minPitchPeriod = 16
	maxPitchPeriod = 1024
)

// NewPostFilter creates a new pitch post-filter.
func NewPostFilter(maxPeriod int) *PostFilter {
	if maxPeriod <= 0 {
		maxPeriod = maxPitchPeriod
	}

	return &PostFilter{
		maxPeriod: maxPeriod,
		state: &PostFilterState{
			history:    make([]float64, maxPeriod+4), // +4 for tap access
			historyPos: 0,
			prevGain:   0,
			prevPeriod: minPitchPeriod,
		},
		enabled: false,
	}
}

// Reset resets the filter state.
func (pf *PostFilter) Reset() {
	for i := range pf.state.history {
		pf.state.history[i] = 0
	}
	pf.state.historyPos = 0
	pf.state.prevGain = 0
	pf.state.prevPeriod = minPitchPeriod
	pf.enabled = false
}

// SetEnabled enables or disables the post-filter.
func (pf *PostFilter) SetEnabled(enabled bool) {
	pf.enabled = enabled
}

// IsEnabled returns whether the post-filter is enabled.
func (pf *PostFilter) IsEnabled() bool {
	return pf.enabled
}

// Apply applies the pitch post-filter to a frame of samples.
// This is used in the decoder to remove the pre-filter effect.
func (pf *PostFilter) Apply(samples []float64, config *PostFilterConfig) {
	if !pf.enabled || config == nil || config.Gain <= 0 {
		return
	}

	period := clampInt(config.Period, minPitchPeriod, pf.maxPeriod)
	gain := math.Min(math.Max(config.Gain, 0.0), 1.0)
	tapset := clampInt(config.Tapset, 0, 2)

	// Apply the comb filter with smooth transition from previous parameters
	pf.applyComb(samples, period, gain, tapset)

	// Update state for next frame
	pf.state.prevGain = gain
	pf.state.prevPeriod = period
}

// ApplyPreFilter applies the pitch pre-filter to a frame of samples.
// This is used in the encoder to enhance periodic content.
func (pf *PostFilter) ApplyPreFilter(samples []float64, config *PostFilterConfig) {
	if !pf.enabled || config == nil || config.Gain <= 0 {
		return
	}

	period := clampInt(config.Period, minPitchPeriod, pf.maxPeriod)
	gain := math.Min(math.Max(config.Gain, 0.0), 1.0)
	tapset := clampInt(config.Tapset, 0, 2)

	// Pre-filter adds the pitch-delayed signal
	pf.applyPreComb(samples, period, gain, tapset)

	// Update state for next frame
	pf.state.prevGain = gain
	pf.state.prevPeriod = period
}

// applyComb applies the inverse comb filter for post-filtering.
func (pf *PostFilter) applyComb(samples []float64, period int, gain float64, tapset int) {
	n := len(samples)
	histLen := len(pf.state.history)
	taps := tapsets[tapset]

	// Process each sample
	for i := 0; i < n; i++ {
		// Read the delayed sample with interpolation from taps
		delayedSum := 0.0
		for t := -1; t <= 1; t++ {
			tapIdx := t + 1
			if tapIdx >= len(taps) {
				continue
			}
			tapGain := taps[tapIdx]
			if tapGain == 0 {
				continue
			}

			// Compute the history position for this tap
			histIdx := (pf.state.historyPos - period + t + histLen) % histLen
			delayedSum += tapGain * pf.state.history[histIdx]
		}

		// Apply the inverse comb: y[n] = x[n] - g * delayed[n]
		output := samples[i] - gain*delayedSum

		// Store current sample in history (after processing)
		pf.state.history[pf.state.historyPos] = samples[i]
		pf.state.historyPos = (pf.state.historyPos + 1) % histLen

		samples[i] = output
	}
}

// applyPreComb applies the forward comb filter for pre-filtering.
func (pf *PostFilter) applyPreComb(samples []float64, period int, gain float64, tapset int) {
	n := len(samples)
	histLen := len(pf.state.history)
	taps := tapsets[tapset]

	// Process each sample
	for i := 0; i < n; i++ {
		// Store current sample in history before processing
		pf.state.history[pf.state.historyPos] = samples[i]

		// Read the delayed sample with interpolation from taps
		delayedSum := 0.0
		for t := -1; t <= 1; t++ {
			tapIdx := t + 1
			if tapIdx >= len(taps) {
				continue
			}
			tapGain := taps[tapIdx]
			if tapGain == 0 {
				continue
			}

			// Compute the history position for this tap
			histIdx := (pf.state.historyPos - period + t + histLen) % histLen
			delayedSum += tapGain * pf.state.history[histIdx]
		}

		// Apply the forward comb: y[n] = x[n] + g * delayed[n]
		samples[i] = samples[i] + gain*delayedSum

		// Advance history position
		pf.state.historyPos = (pf.state.historyPos + 1) % histLen
	}
}

// AnalyzePitch estimates the pitch period and gain for a frame.
// Returns the optimal PostFilterConfig or nil if no periodicity is detected.
func AnalyzePitch(samples []float64, sampleRate, minPeriod, maxPeriod int) *PostFilterConfig {
	n := len(samples)
	if n < maxPeriod*2 {
		return nil
	}

	// Compute autocorrelation for different lags
	bestPeriod := minPeriod
	bestCorr := 0.0
	energy := 0.0

	// Compute energy of the signal
	for _, s := range samples {
		energy += s * s
	}
	if energy < 1e-10 {
		return nil // Signal too weak
	}

	// Search for the pitch period with highest correlation
	for period := minPeriod; period <= maxPeriod; period++ {
		corr := computeNormalizedCorrelation(samples, period)
		if corr > bestCorr {
			bestCorr = corr
			bestPeriod = period
		}
	}

	// If correlation is too weak, no periodicity detected
	if bestCorr < 0.3 {
		return nil
	}

	// Compute gain from correlation
	gain := math.Min(bestCorr, 0.9)

	// Select tapset based on signal characteristics
	tapset := selectTapset(samples, bestPeriod)

	return &PostFilterConfig{
		Period: bestPeriod,
		Gain:   gain,
		Tapset: tapset,
	}
}

// computeNormalizedCorrelation computes the normalized correlation at a lag.
func computeNormalizedCorrelation(samples []float64, lag int) float64 {
	n := len(samples)
	if lag >= n {
		return 0
	}

	// Compute correlation and energies
	corr := 0.0
	energy1 := 0.0
	energy2 := 0.0

	for i := lag; i < n; i++ {
		corr += samples[i] * samples[i-lag]
		energy1 += samples[i] * samples[i]
		energy2 += samples[i-lag] * samples[i-lag]
	}

	denom := math.Sqrt(energy1 * energy2)
	if denom < 1e-10 {
		return 0
	}

	return corr / denom
}

// selectTapset selects the optimal tapset based on signal characteristics.
func selectTapset(samples []float64, period int) int {
	n := len(samples)
	if n < period*2 {
		return 0
	}

	// Measure the sharpness of the pitch peak
	// by comparing correlation at period vs period+/-1
	corrMain := computeNormalizedCorrelation(samples, period)
	corrMinus := computeNormalizedCorrelation(samples, period-1)
	corrPlus := computeNormalizedCorrelation(samples, period+1)

	// If the peak is sharp, use tapset 2; if broad, use tapset 0
	peakSharpness := corrMain - (corrMinus+corrPlus)/2

	if peakSharpness > 0.3 {
		return 2 // Sharp peak
	} else if peakSharpness > 0.1 {
		return 1 // Medium
	}
	return 0 // Smooth
}

// clampInt clamps an integer to a range.
func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// EncodePostFilter encodes the post-filter parameters to the bitstream.
func EncodePostFilter(enc *RangeEncoder, config *PostFilterConfig, enabled bool) {
	// Encode enabled flag (1 bit)
	enabledBit := 0
	if enabled && config != nil && config.Gain > 0.1 {
		enabledBit = 1
	}
	enc.EncodeBits(uint32(enabledBit), 1)

	if enabledBit == 0 {
		return
	}

	// Encode period (10 bits for range 16-1024)
	periodCode := clampInt(config.Period-minPitchPeriod, 0, 1008)
	enc.EncodeBits(uint32(periodCode), 10)

	// Encode gain (3 bits for 8 levels)
	gainCode := int(config.Gain * 7.0)
	if gainCode > 7 {
		gainCode = 7
	}
	enc.EncodeBits(uint32(gainCode), 3)

	// Encode tapset (2 bits)
	enc.EncodeBits(uint32(clampInt(config.Tapset, 0, 2)), 2)
}

// DecodePostFilter decodes the post-filter parameters from the bitstream.
func DecodePostFilter(dec *RangeDecoder) (config *PostFilterConfig, enabled bool) {
	// Decode enabled flag
	enabledBit := dec.DecodeBits(1)
	if enabledBit == 0 {
		return nil, false
	}

	// Decode period
	periodCode := dec.DecodeBits(10)
	period := int(periodCode) + minPitchPeriod

	// Decode gain
	gainCode := dec.DecodeBits(3)
	gain := float64(gainCode) / 7.0

	// Decode tapset
	tapset := int(dec.DecodeBits(2))

	return &PostFilterConfig{
		Period: period,
		Gain:   gain,
		Tapset: tapset,
	}, true
}
