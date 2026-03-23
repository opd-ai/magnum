// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements open-loop pitch estimation as specified in RFC 6716
// Appendix B.7 for the SILK codec. The pitch estimator detects periodicity
// in voiced speech by searching for the lag with maximum normalized
// cross-correlation in the LPC residual signal.
//
// Key components:
// - Open-loop pitch lag search with correlation-based scoring
// - Voicing detection to distinguish voiced from unvoiced frames
// - Sub-frame pitch tracking for smoother pitch contours
// - LPC-based spectral whitening for robust pitch detection

package magnum

import (
	"math"
)

// Pitch period constraints for SILK codec (RFC 6716 §4.2.7.5).
// These are defined in samples at the internal SILK sample rate.
const (
	// SILKMinPitchLag is the minimum pitch lag in samples (at 16 kHz = ~0.5 ms = ~2000 Hz).
	SILKMinPitchLag = 8
	// SILKMaxPitchLag is the maximum pitch lag in samples (at 16 kHz = ~18 ms = ~55 Hz).
	SILKMaxPitchLag = 288
	// SILKSubFrames is the number of sub-frames per SILK frame.
	SILKSubFrames = 4
	// voicingThreshold is the minimum correlation for voiced detection.
	voicingThreshold = 0.3
)

// PitchEstimate holds the result of pitch estimation for a frame.
type PitchEstimate struct {
	// Lag is the estimated pitch lag in samples.
	Lag int
	// Correlation is the normalized correlation at the estimated lag (0.0 to 1.0).
	Correlation float64
	// Voiced indicates whether the frame is detected as voiced speech.
	Voiced bool
	// SubframeLags holds pitch lags for each sub-frame (optional).
	SubframeLags []int
	// Gain is the voicing strength suitable for LTP (0.0 to 1.0).
	Gain float64
}

// PitchEstimator performs open-loop pitch estimation for SILK encoding.
type PitchEstimator struct {
	// sampleRate is the analysis sample rate (8000 or 16000 Hz).
	sampleRate int
	// minLag is the minimum pitch lag in samples.
	minLag int
	// maxLag is the maximum pitch lag in samples.
	maxLag int
	// lpc is the LPC analyzer for spectral whitening.
	lpc *LPCAnalyzer
	// prevPitch is the pitch from the previous frame (for continuity).
	prevPitch int
	// prevVoiced indicates if previous frame was voiced.
	prevVoiced bool
	// autocorr is a pre-allocated buffer for autocorrelation.
	autocorr []float64
	// residual is a pre-allocated buffer for LPC residual.
	residual []float64
}

// NewPitchEstimator creates a new open-loop pitch estimator.
// The sample rate must be 8000 Hz (narrowband) or 16000 Hz (wideband).
func NewPitchEstimator(sampleRate int) *PitchEstimator {
	// Determine LPC order based on sample rate
	var lpcOrder int
	switch sampleRate {
	case SampleRate8k:
		lpcOrder = LPCOrderNarrowband
	case SampleRate16k:
		lpcOrder = LPCOrderWideband
	default:
		// Default to wideband parameters
		sampleRate = SampleRate16k
		lpcOrder = LPCOrderWideband
	}

	// Scale pitch lag range by sample rate ratio
	// Base values are for 16 kHz; scale for 8 kHz
	scale := float64(sampleRate) / 16000.0
	minLag := int(float64(SILKMinPitchLag) * scale)
	maxLag := int(float64(SILKMaxPitchLag) * scale)

	// Ensure minimum viable range
	if minLag < 4 {
		minLag = 4
	}

	return &PitchEstimator{
		sampleRate: sampleRate,
		minLag:     minLag,
		maxLag:     maxLag,
		lpc:        NewLPCAnalyzer(lpcOrder),
		prevPitch:  (minLag + maxLag) / 2,
		prevVoiced: false,
		autocorr:   make([]float64, maxLag+1),
		residual:   nil, // Allocated on first use
	}
}

// Estimate performs open-loop pitch estimation on a frame of samples.
// The input samples should be a complete SILK frame (20 ms).
// Returns nil if the frame is too short for pitch analysis.
func (pe *PitchEstimator) Estimate(samples []float64) *PitchEstimate {
	n := len(samples)
	if n < pe.maxLag*2 {
		return nil
	}

	// Step 1: Apply Hamming window
	windowed := ApplyHammingWindow(samples)

	// Step 2: Compute LPC coefficients for spectral whitening
	lpcResult := pe.lpc.Analyze(windowed)

	// Step 3: Compute LPC residual (whitened signal)
	residual := ApplyLPCFilter(samples, lpcResult.Coefficients)
	if residual == nil {
		return nil
	}

	// Step 4: Search for best pitch lag using normalized correlation
	bestLag, bestCorr := pe.searchPitchLag(residual)

	// Step 5: Determine if frame is voiced
	voiced := bestCorr >= voicingThreshold

	// Step 6: Apply pitch continuity constraint if previously voiced
	if pe.prevVoiced && voiced {
		bestLag, bestCorr = pe.refinePitchWithContinuity(residual, bestLag)
	}

	// Step 7: Estimate sub-frame pitch lags for finer tracking
	subframeLags := pe.estimateSubframePitches(residual, bestLag, voiced)

	// Compute gain for LTP
	gain := 0.0
	if voiced {
		gain = math.Min(bestCorr, 0.95)
	}

	// Update state
	pe.prevPitch = bestLag
	pe.prevVoiced = voiced

	return &PitchEstimate{
		Lag:          bestLag,
		Correlation:  bestCorr,
		Voiced:       voiced,
		SubframeLags: subframeLags,
		Gain:         gain,
	}
}

// searchPitchLag searches for the pitch lag with maximum correlation.
func (pe *PitchEstimator) searchPitchLag(residual []float64) (int, float64) {
	n := len(residual)
	bestLag := pe.minLag
	bestCorr := 0.0

	// Compute autocorrelation at each candidate lag
	for lag := pe.minLag; lag <= pe.maxLag && lag < n; lag++ {
		corr := pe.computeNormalizedCorr(residual, lag)
		if corr > bestCorr {
			bestCorr = corr
			bestLag = lag
		}
	}

	// Check for octave errors by examining correlation at lag/2 and lag*2
	bestLag, bestCorr = pe.resolveOctaveErrors(residual, bestLag, bestCorr)

	return bestLag, bestCorr
}

// computeNormalizedCorr computes the normalized cross-correlation at a lag.
// This follows RFC 6716's formulation: corr / sqrt(energy0 * energyLag).
func (pe *PitchEstimator) computeNormalizedCorr(samples []float64, lag int) float64 {
	n := len(samples)
	if lag >= n || lag < 1 {
		return 0
	}

	// Compute correlation and energies over the analysis window
	// Using a window that excludes the initial transient
	startIdx := lag
	endIdx := n

	corr := 0.0
	energy0 := 0.0
	energyLag := 0.0

	for i := startIdx; i < endIdx; i++ {
		corr += samples[i] * samples[i-lag]
		energy0 += samples[i] * samples[i]
		energyLag += samples[i-lag] * samples[i-lag]
	}

	denom := math.Sqrt(energy0 * energyLag)
	if denom < 1e-10 {
		return 0
	}

	return corr / denom
}

// resolveOctaveErrors checks for pitch doubling/halving errors.
// For pure tones and harmonic signals, prefers shorter lags (higher frequencies)
// when they have comparable correlation to reduce octave errors.
func (pe *PitchEstimator) resolveOctaveErrors(residual []float64, lag int, corr float64) (int, float64) {
	// Iteratively check if lag/2 has high correlation (prefer fundamental over harmonics)
	// This handles cases where a subharmonic has equal or near-equal correlation
	currentLag := lag
	currentCorr := corr

	for {
		halfLag := currentLag / 2
		if halfLag < pe.minLag {
			break
		}

		halfCorr := pe.computeNormalizedCorr(residual, halfLag)
		// Prefer shorter lag if correlation is within 5% (strong preference for fundamental)
		if halfCorr >= currentCorr*0.95 {
			currentLag = halfLag
			currentCorr = halfCorr
		} else {
			break
		}
	}

	// Also check lag/3 for detecting third harmonics
	thirdLag := lag / 3
	if thirdLag >= pe.minLag {
		thirdCorr := pe.computeNormalizedCorr(residual, thirdLag)
		if thirdCorr >= currentCorr*0.95 {
			currentLag = thirdLag
			currentCorr = thirdCorr
		}
	}

	return currentLag, currentCorr
}

// refinePitchWithContinuity refines pitch estimate using continuity from previous frame.
func (pe *PitchEstimator) refinePitchWithContinuity(residual []float64, initialLag int) (int, float64) {
	// Search around both the initial estimate and previous pitch
	searchRange := pe.maxLag / 16 // ±6% of max lag
	if searchRange < 2 {
		searchRange = 2
	}

	bestLag := initialLag
	bestCorr := pe.computeNormalizedCorr(residual, initialLag)

	// Search around initial estimate
	for delta := -searchRange; delta <= searchRange; delta++ {
		testLag := initialLag + delta
		if testLag < pe.minLag || testLag > pe.maxLag {
			continue
		}
		testCorr := pe.computeNormalizedCorr(residual, testLag)
		if testCorr > bestCorr {
			bestCorr = testCorr
			bestLag = testLag
		}
	}

	// Search around previous pitch with a bias toward continuity
	for delta := -searchRange; delta <= searchRange; delta++ {
		testLag := pe.prevPitch + delta
		if testLag < pe.minLag || testLag > pe.maxLag {
			continue
		}
		testCorr := pe.computeNormalizedCorr(residual, testLag)
		// Apply continuity bonus (5% boost for matching previous)
		effectiveCorr := testCorr
		if delta == 0 {
			effectiveCorr *= 1.05
		}
		if effectiveCorr > bestCorr {
			bestCorr = testCorr // Store actual correlation, not boosted
			bestLag = testLag
		}
	}

	return bestLag, bestCorr
}

// estimateSubframePitches estimates pitch for each sub-frame.
func (pe *PitchEstimator) estimateSubframePitches(residual []float64, frameLag int, voiced bool) []int {
	n := len(residual)
	subframeLen := n / SILKSubFrames
	subframeLags := make([]int, SILKSubFrames)

	if !voiced || subframeLen < pe.maxLag {
		// Unvoiced or too short: use frame-level lag for all
		for i := range subframeLags {
			subframeLags[i] = frameLag
		}
		return subframeLags
	}

	// Search around frame-level lag for each sub-frame
	searchRange := pe.maxLag / 32
	if searchRange < 1 {
		searchRange = 1
	}

	for sf := 0; sf < SILKSubFrames; sf++ {
		startIdx := sf * subframeLen
		endIdx := startIdx + subframeLen
		if endIdx > n {
			endIdx = n
		}

		subframe := residual[startIdx:endIdx]
		if len(subframe) < pe.maxLag {
			subframeLags[sf] = frameLag
			continue
		}

		// Search for best lag around frame estimate
		bestSubLag := frameLag
		bestSubCorr := 0.0

		for delta := -searchRange; delta <= searchRange; delta++ {
			testLag := frameLag + delta
			if testLag < pe.minLag || testLag > pe.maxLag || testLag >= len(subframe) {
				continue
			}
			testCorr := pe.computeNormalizedCorr(subframe, testLag)
			if testCorr > bestSubCorr {
				bestSubCorr = testCorr
				bestSubLag = testLag
			}
		}

		subframeLags[sf] = bestSubLag
	}

	return subframeLags
}

// EstimateVoicingStrength returns a voicing strength metric (0.0 to 1.0).
// This can be used for VAD (Voice Activity Detection).
func (pe *PitchEstimator) EstimateVoicingStrength(samples []float64) float64 {
	estimate := pe.Estimate(samples)
	if estimate == nil {
		return 0
	}
	return estimate.Correlation
}

// Reset resets the pitch estimator state.
func (pe *PitchEstimator) Reset() {
	pe.prevPitch = (pe.minLag + pe.maxLag) / 2
	pe.prevVoiced = false
}

// MinLag returns the minimum pitch lag in samples.
func (pe *PitchEstimator) MinLag() int {
	return pe.minLag
}

// MaxLag returns the maximum pitch lag in samples.
func (pe *PitchEstimator) MaxLag() int {
	return pe.maxLag
}

// SampleRate returns the configured sample rate.
func (pe *PitchEstimator) SampleRate() int {
	return pe.sampleRate
}

// LagToFrequency converts a pitch lag in samples to frequency in Hz.
func (pe *PitchEstimator) LagToFrequency(lag int) float64 {
	if lag <= 0 {
		return 0
	}
	return float64(pe.sampleRate) / float64(lag)
}

// FrequencyToLag converts a frequency in Hz to pitch lag in samples.
func (pe *PitchEstimator) FrequencyToLag(freq float64) int {
	if freq <= 0 {
		return pe.maxLag
	}
	lag := int(float64(pe.sampleRate) / freq)
	if lag < pe.minLag {
		return pe.minLag
	}
	if lag > pe.maxLag {
		return pe.maxLag
	}
	return lag
}

// PitchLagToHz converts a pitch lag to frequency at the given sample rate.
func PitchLagToHz(lag, sampleRate int) float64 {
	if lag <= 0 {
		return 0
	}
	return float64(sampleRate) / float64(lag)
}

// HzToPitchLag converts a frequency to pitch lag at the given sample rate.
func HzToPitchLag(hz float64, sampleRate int) int {
	if hz <= 0 {
		return 0
	}
	return int(float64(sampleRate) / hz)
}
