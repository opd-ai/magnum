// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements Long-Term Prediction (LTP) analysis and quantization
// as specified in RFC 6716 §4.2.7 for the SILK codec. LTP models the periodic
// structure of voiced speech by predicting samples from past samples at the
// pitch lag.
//
// Key components:
// - Closed-loop pitch search (refinement of open-loop estimate)
// - LTP coefficient estimation via least-squares optimization
// - LTP gain quantization using vector codebooks
// - LTP filtering for residual signal computation

package magnum

import (
	"math"
)

// LTP filter constants from RFC 6716.
const (
	// LTPOrder is the number of LTP filter taps (5 in SILK).
	LTPOrder = 5
	// LTPNumSubframes is the number of subframes per SILK frame.
	LTPNumSubframes = 4
	// LTPMinPitchLag is the minimum pitch lag (samples at internal rate).
	LTPMinPitchLag = 2
	// LTPMaxPitchLag16k is the maximum pitch lag at 16 kHz.
	LTPMaxPitchLag16k = 288
	// LTPMaxPitchLag8k is the maximum pitch lag at 8 kHz.
	LTPMaxPitchLag8k = 144
)

// LTPGainCodebook defines the LTP gain quantization codebook.
// This is a simplified version; full SILK uses trained codebooks.
// Each entry has 5 taps centered on the pitch lag.
var LTPGainCodebook = [][]float64{
	{0.0, 0.0, 0.0, 0.0, 0.0},        // Index 0: No prediction
	{0.0, 0.0, 0.25, 0.0, 0.0},       // Index 1: Single tap
	{0.0, 0.0, 0.5, 0.0, 0.0},        // Index 2: Single tap stronger
	{0.0, 0.0, 0.75, 0.0, 0.0},       // Index 3: Single tap strong
	{0.0, 0.125, 0.5, 0.125, 0.0},    // Index 4: Symmetric 3-tap
	{0.0, 0.25, 0.5, 0.25, 0.0},      // Index 5: Symmetric 3-tap stronger
	{0.125, 0.25, 0.25, 0.25, 0.125}, // Index 6: Symmetric 5-tap
	{0.2, 0.2, 0.2, 0.2, 0.2},        // Index 7: Flat 5-tap
}

// LTPResult holds the result of LTP analysis for a subframe.
type LTPResult struct {
	// PitchLag is the pitch lag in samples for this subframe.
	PitchLag int
	// Gains are the LTP filter coefficients (5 taps).
	Gains [LTPOrder]float64
	// CodebookIndex is the quantized codebook entry.
	CodebookIndex int
	// PredictionGain is the prediction gain in dB.
	PredictionGain float64
}

// LTPFrameResult holds LTP analysis for an entire frame.
type LTPFrameResult struct {
	// Subframes contains LTP results for each subframe.
	Subframes [LTPNumSubframes]*LTPResult
	// ContourIndex indicates the pitch lag contour (delta pattern).
	ContourIndex int
	// Voiced indicates if the frame contains voiced speech.
	Voiced bool
}

// LTPAnalyzer performs Long-Term Prediction analysis.
type LTPAnalyzer struct {
	// sampleRate is the analysis sample rate.
	sampleRate int
	// maxLag is the maximum pitch lag.
	maxLag int
	// minLag is the minimum pitch lag.
	minLag int
	// pitchEstimator is used for open-loop pitch estimation.
	pitchEstimator *PitchEstimator
	// subframeLength is samples per subframe.
	subframeLength int
	// prevLags holds pitch lags from the previous frame.
	prevLags [LTPNumSubframes]int
}

// NewLTPAnalyzer creates a new LTP analyzer.
func NewLTPAnalyzer(sampleRate int) *LTPAnalyzer {
	var maxLag int
	switch sampleRate {
	case SampleRate8k:
		maxLag = LTPMaxPitchLag8k
	default:
		sampleRate = SampleRate16k
		maxLag = LTPMaxPitchLag16k
	}

	// Subframe length: 5ms at the given sample rate
	subframeLength := sampleRate / 200 // 5ms = sampleRate/200

	ltp := &LTPAnalyzer{
		sampleRate:     sampleRate,
		maxLag:         maxLag,
		minLag:         LTPMinPitchLag,
		pitchEstimator: NewPitchEstimator(sampleRate),
		subframeLength: subframeLength,
	}

	// Initialize previous lags to middle of range
	midLag := (ltp.minLag + ltp.maxLag) / 2
	for i := range ltp.prevLags {
		ltp.prevLags[i] = midLag
	}

	return ltp
}

// Analyze performs LTP analysis on a frame of samples.
// The input should be the LPC residual signal for better results.
func (ltp *LTPAnalyzer) Analyze(samples []float64) *LTPFrameResult {
	n := len(samples)
	if n < ltp.subframeLength*LTPNumSubframes {
		return nil
	}

	// Step 1: Get open-loop pitch estimate
	pitchEst := ltp.pitchEstimator.Estimate(samples)
	if pitchEst == nil {
		return ltp.createUnvoicedResult()
	}

	// Check if frame is voiced
	if !pitchEst.Voiced {
		return ltp.createUnvoicedResult()
	}

	result := &LTPFrameResult{
		Voiced: true,
	}

	// Step 2: Closed-loop pitch search and LTP analysis per subframe
	for sf := 0; sf < LTPNumSubframes; sf++ {
		startIdx := sf * ltp.subframeLength
		endIdx := startIdx + ltp.subframeLength
		if endIdx > n {
			endIdx = n
		}

		subframe := samples[startIdx:endIdx]

		// Get initial lag estimate from open-loop or previous subframe
		var initLag int
		if len(pitchEst.SubframeLags) > sf {
			initLag = pitchEst.SubframeLags[sf]
		} else {
			initLag = pitchEst.Lag
		}

		// Closed-loop pitch refinement
		result.Subframes[sf] = ltp.analyzeSubframe(samples, startIdx, initLag, subframe)
	}

	// Step 3: Compute pitch lag contour index
	result.ContourIndex = ltp.computeContourIndex(result)

	// Update state for next frame
	for sf := 0; sf < LTPNumSubframes; sf++ {
		if result.Subframes[sf] != nil {
			ltp.prevLags[sf] = result.Subframes[sf].PitchLag
		}
	}

	return result
}

// analyzeSubframe performs closed-loop LTP analysis for one subframe.
func (ltp *LTPAnalyzer) analyzeSubframe(fullSamples []float64, offset, initLag int, subframe []float64) *LTPResult {
	sfLen := len(subframe)
	if sfLen == 0 {
		return &LTPResult{PitchLag: initLag}
	}

	// Closed-loop pitch search: refine around open-loop estimate
	bestLag := initLag
	bestCorr := 0.0
	searchRange := ltp.maxLag / 16
	if searchRange < 2 {
		searchRange = 2
	}

	for delta := -searchRange; delta <= searchRange; delta++ {
		testLag := initLag + delta
		if testLag < ltp.minLag || testLag > ltp.maxLag {
			continue
		}

		// Compute correlation at this lag using full signal context
		corr := ltp.computeLagCorrelation(fullSamples, offset, testLag, sfLen)
		if corr > bestCorr {
			bestCorr = corr
			bestLag = testLag
		}
	}

	// Estimate optimal LTP gains via least-squares
	gains := ltp.estimateGains(fullSamples, offset, bestLag, sfLen)

	// Quantize gains
	codebookIdx := ltp.quantizeGains(gains)
	quantizedGains := LTPGainCodebook[codebookIdx]

	// Compute prediction gain
	predGain := ltp.computePredictionGain(fullSamples, offset, bestLag, sfLen, quantizedGains)

	result := &LTPResult{
		PitchLag:       bestLag,
		CodebookIndex:  codebookIdx,
		PredictionGain: predGain,
	}
	copy(result.Gains[:], quantizedGains)

	return result
}

// computeLagCorrelation computes normalized correlation at a lag.
func (ltp *LTPAnalyzer) computeLagCorrelation(samples []float64, offset, lag, length int) float64 {
	n := len(samples)
	if offset+length > n || offset-lag < 0 {
		return 0
	}

	corr := 0.0
	energy1 := 0.0
	energy2 := 0.0

	for i := 0; i < length; i++ {
		idx := offset + i
		lagIdx := idx - lag
		if lagIdx < 0 || idx >= n {
			continue
		}
		corr += samples[idx] * samples[lagIdx]
		energy1 += samples[idx] * samples[idx]
		energy2 += samples[lagIdx] * samples[lagIdx]
	}

	denom := math.Sqrt(energy1 * energy2)
	if denom < 1e-10 {
		return 0
	}

	return corr / denom
}

// estimateGains estimates optimal LTP gains via least-squares.
func (ltp *LTPAnalyzer) estimateGains(samples []float64, offset, lag, length int) [LTPOrder]float64 {
	var gains [LTPOrder]float64
	n := len(samples)

	// Simple approach: compute gain for center tap only
	// Full implementation would solve normal equations for all 5 taps

	centerTap := LTPOrder / 2

	// Ensure we have enough history
	startSample := offset - lag - centerTap
	if startSample < 0 || offset+length > n {
		return gains
	}

	// Compute optimal gain for center tap: g = (x . x_lag) / (x_lag . x_lag)
	corr := 0.0
	energy := 0.0

	for i := 0; i < length; i++ {
		idx := offset + i
		lagIdx := idx - lag
		if lagIdx < 0 || idx >= n {
			continue
		}
		corr += samples[idx] * samples[lagIdx]
		energy += samples[lagIdx] * samples[lagIdx]
	}

	if energy > 1e-10 {
		gains[centerTap] = corr / energy
		// Clamp to valid range
		if gains[centerTap] > 1.0 {
			gains[centerTap] = 1.0
		} else if gains[centerTap] < 0.0 {
			gains[centerTap] = 0.0
		}
	}

	return gains
}

// quantizeGains finds the best codebook entry for the given gains.
func (ltp *LTPAnalyzer) quantizeGains(gains [LTPOrder]float64) int {
	bestIdx := 0
	bestDist := math.MaxFloat64

	for idx, cb := range LTPGainCodebook {
		dist := 0.0
		for i := 0; i < LTPOrder; i++ {
			d := gains[i] - cb[i]
			dist += d * d
		}
		if dist < bestDist {
			bestDist = dist
			bestIdx = idx
		}
	}

	return bestIdx
}

// computePredictionGain computes the prediction gain in dB.
func (ltp *LTPAnalyzer) computePredictionGain(samples []float64, offset, lag, length int, gains []float64) float64 {
	n := len(samples)

	signalEnergy := 0.0
	residualEnergy := 0.0
	centerTap := LTPOrder / 2

	for i := 0; i < length; i++ {
		idx := offset + i
		if idx >= n {
			break
		}

		// Original signal energy
		signalEnergy += samples[idx] * samples[idx]

		// Compute LTP prediction
		pred := 0.0
		for t := 0; t < LTPOrder; t++ {
			tapOffset := t - centerTap
			lagIdx := idx - lag + tapOffset
			if lagIdx >= 0 && lagIdx < n {
				pred += gains[t] * samples[lagIdx]
			}
		}

		// Residual
		residual := samples[idx] - pred
		residualEnergy += residual * residual
	}

	if residualEnergy < 1e-10 {
		return 30.0 // Maximum gain cap
	}

	gain := signalEnergy / residualEnergy
	if gain < 1.0 {
		gain = 1.0
	}

	return 10.0 * math.Log10(gain)
}

// computeContourIndex determines the pitch lag contour pattern.
func (ltp *LTPAnalyzer) computeContourIndex(result *LTPFrameResult) int {
	// Compute deltas between subframe lags
	deltas := make([]int, LTPNumSubframes-1)
	for i := 0; i < LTPNumSubframes-1; i++ {
		if result.Subframes[i] != nil && result.Subframes[i+1] != nil {
			deltas[i] = result.Subframes[i+1].PitchLag - result.Subframes[i].PitchLag
		}
	}

	// Simple contour classification (simplified from full SILK)
	// 0: Flat (all deltas near zero)
	// 1: Rising (positive deltas)
	// 2: Falling (negative deltas)
	// 3: Variable

	sumDelta := 0
	for _, d := range deltas {
		sumDelta += d
	}

	avgDelta := sumDelta / len(deltas)
	if avgDelta >= -1 && avgDelta <= 1 {
		return 0 // Flat
	} else if avgDelta > 1 {
		return 1 // Rising
	} else if avgDelta < -1 {
		return 2 // Falling
	}
	return 3 // Variable
}

// createUnvoicedResult creates an LTP result for unvoiced frames.
func (ltp *LTPAnalyzer) createUnvoicedResult() *LTPFrameResult {
	result := &LTPFrameResult{
		Voiced:       false,
		ContourIndex: 0,
	}

	midLag := (ltp.minLag + ltp.maxLag) / 2
	for sf := 0; sf < LTPNumSubframes; sf++ {
		result.Subframes[sf] = &LTPResult{
			PitchLag:      midLag,
			CodebookIndex: 0, // No prediction
		}
	}

	return result
}

// ApplyLTP applies the LTP filter to a signal.
// prediction[n] = sum(gains[k] * signal[n - lag + k - center]) for k=0..4
func ApplyLTP(signal []float64, lag int, gains []float64) []float64 {
	n := len(signal)
	if n == 0 || lag < LTPOrder/2 {
		return make([]float64, n)
	}

	prediction := make([]float64, n)
	centerTap := LTPOrder / 2

	for i := 0; i < n; i++ {
		pred := 0.0
		for t := 0; t < len(gains) && t < LTPOrder; t++ {
			tapOffset := t - centerTap
			lagIdx := i - lag + tapOffset
			if lagIdx >= 0 && lagIdx < n {
				pred += gains[t] * signal[lagIdx]
			}
		}
		prediction[i] = pred
	}

	return prediction
}

// ComputeLTPResidual computes the residual after LTP prediction.
// residual[n] = signal[n] - prediction[n]
func ComputeLTPResidual(signal []float64, lag int, gains []float64) []float64 {
	prediction := ApplyLTP(signal, lag, gains)
	residual := make([]float64, len(signal))

	for i := range signal {
		residual[i] = signal[i] - prediction[i]
	}

	return residual
}

// SynthesizeLTP reconstructs signal from LTP residual.
// signal[n] = residual[n] + prediction[n]
func SynthesizeLTP(residual []float64, lag int, gains []float64) []float64 {
	n := len(residual)
	signal := make([]float64, n)
	centerTap := LTPOrder / 2

	// IIR synthesis: need to use previously synthesized samples
	for i := 0; i < n; i++ {
		pred := 0.0
		for t := 0; t < len(gains) && t < LTPOrder; t++ {
			tapOffset := t - centerTap
			lagIdx := i - lag + tapOffset
			if lagIdx >= 0 && lagIdx < i {
				// Use previously synthesized samples
				pred += gains[t] * signal[lagIdx]
			}
		}
		signal[i] = residual[i] + pred
	}

	return signal
}

// EncodeLTPParams encodes LTP parameters to the bitstream.
func EncodeLTPParams(enc *RangeEncoder, result *LTPFrameResult, frameLag int) {
	if result == nil || !result.Voiced {
		// Encode unvoiced flag
		enc.EncodeBits(0, 1)
		return
	}

	// Encode voiced flag
	enc.EncodeBits(1, 1)

	// Encode frame-level pitch lag (using delta from previous or absolute)
	// Simplified: encode absolute lag with 9 bits (0-511 range)
	lagCode := clampInt(frameLag, 0, 511)
	enc.EncodeBits(uint32(lagCode), 9)

	// Encode contour index (2 bits for 4 contours)
	enc.EncodeBits(uint32(result.ContourIndex&0x3), 2)

	// Encode codebook indices for each subframe (3 bits each for 8 entries)
	for sf := 0; sf < LTPNumSubframes; sf++ {
		cbIdx := 0
		if result.Subframes[sf] != nil {
			cbIdx = result.Subframes[sf].CodebookIndex
		}
		enc.EncodeBits(uint32(cbIdx&0x7), 3)
	}
}

// DecodeLTPParams decodes LTP parameters from the bitstream.
func DecodeLTPParams(dec *RangeDecoder) (*LTPFrameResult, int) {
	result := &LTPFrameResult{}

	// Decode voiced flag
	voicedBit := dec.DecodeBits(1)
	if voicedBit == 0 {
		result.Voiced = false
		// Initialize unvoiced subframes
		for sf := 0; sf < LTPNumSubframes; sf++ {
			result.Subframes[sf] = &LTPResult{
				PitchLag:      0,
				CodebookIndex: 0,
			}
		}
		return result, 0
	}

	result.Voiced = true

	// Decode frame-level pitch lag
	frameLag := int(dec.DecodeBits(9))

	// Decode contour index
	result.ContourIndex = int(dec.DecodeBits(2))

	// Decode codebook indices and reconstruct subframe lags
	for sf := 0; sf < LTPNumSubframes; sf++ {
		cbIdx := int(dec.DecodeBits(3))
		result.Subframes[sf] = &LTPResult{
			PitchLag:      frameLag, // Simplified: same lag for all subframes
			CodebookIndex: cbIdx,
		}
		copy(result.Subframes[sf].Gains[:], LTPGainCodebook[cbIdx])
	}

	return result, frameLag
}

// Reset resets the LTP analyzer state.
func (ltp *LTPAnalyzer) Reset() {
	midLag := (ltp.minLag + ltp.maxLag) / 2
	for i := range ltp.prevLags {
		ltp.prevLags[i] = midLag
	}
	ltp.pitchEstimator.Reset()
}

// SampleRate returns the configured sample rate.
func (ltp *LTPAnalyzer) SampleRate() int {
	return ltp.sampleRate
}

// MaxLag returns the maximum pitch lag.
func (ltp *LTPAnalyzer) MaxLag() int {
	return ltp.maxLag
}

// MinLag returns the minimum pitch lag.
func (ltp *LTPAnalyzer) MinLag() int {
	return ltp.minLag
}
