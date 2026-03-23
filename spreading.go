// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements spectral spreading and time-frequency resolution
// parameters as specified in RFC 6716 §4.3. These parameters control
// how energy is distributed across time and frequency to reduce artifacts
// in transform-based coding.
//
// Spreading mitigates tonal artifacts by distributing pulse energy.
// TF (time-frequency) resolution controls whether bands use finer time
// resolution (for transients) or finer frequency resolution (for tones).

package magnum

import (
	"math"
)

// SpreadingMode indicates the amount of spectral spreading to apply.
// Higher spreading values help reduce tonal artifacts at the cost of
// reduced tonal fidelity.
type SpreadingMode int

const (
	// SpreadNone disables spreading (maximum tonal fidelity)
	SpreadNone SpreadingMode = iota
	// SpreadLight applies minimal spreading
	SpreadLight
	// SpreadNormal applies moderate spreading (default)
	SpreadNormal
	// SpreadAggressive applies maximum spreading
	SpreadAggressive
)

// TFResolution controls time vs frequency resolution for each band.
// When TF=1, the band uses higher time resolution (better for transients).
// When TF=0, the band uses higher frequency resolution (better for tones).
type TFResolution struct {
	// TF is a bitmask where bit i indicates resolution for band i
	// 1 = time resolution, 0 = frequency resolution
	TF []int
	// NumBands is the number of bands
	NumBands int
}

// SpreadingAnalyzer analyzes spectrum characteristics to determine
// the optimal spreading mode.
type SpreadingAnalyzer struct {
	// Running average for spreading decision hysteresis
	average      int
	lastDecision SpreadingMode
	// High-frequency content tracking
	hfAverage int
	// Tapset decision for pitch pre/post filter
	tapsetDecision int
}

// NewSpreadingAnalyzer creates a new analyzer for spreading decisions.
func NewSpreadingAnalyzer() *SpreadingAnalyzer {
	return &SpreadingAnalyzer{
		average:        0,
		lastDecision:   SpreadNormal,
		hfAverage:      0,
		tapsetDecision: 0,
	}
}

// Analyze determines the optimal spreading mode based on spectrum characteristics.
// It uses hysteresis to avoid rapid switching between modes.
func (sa *SpreadingAnalyzer) Analyze(spectrum []float64, numBands int) SpreadingMode {
	if len(spectrum) == 0 || numBands == 0 {
		return SpreadNormal
	}

	// Compute spectral flatness as a measure of tonality
	// Flatter spectrum (white noise-like) needs more spreading
	// Peaky spectrum (tonal) needs less spreading
	flatness := computeSpectralFlatness(spectrum)

	// Compute high-frequency energy ratio
	hfRatio := computeHFRatio(spectrum)

	// Update running averages with exponential smoothing
	alpha := 0.25
	sa.average = int(float64(sa.average)*(1-alpha) + flatness*1000*alpha)

	// Spreading thresholds with hysteresis
	// These values are calibrated to match reference implementation behavior
	thresholds := []int{200, 500, 800}
	hysteresis := []int{50, 50, 50}

	decision := hysteresisDecision(sa.average, thresholds, hysteresis, int(sa.lastDecision))
	sa.lastDecision = SpreadingMode(decision)

	// Adjust for high-frequency content
	if hfRatio < 0.1 && decision < int(SpreadAggressive) {
		decision++
	}

	return SpreadingMode(decision)
}

// TFAnalyzer analyzes transient characteristics to determine
// time-frequency resolution per band.
type TFAnalyzer struct {
	// Previous frame energy per band for change detection
	prevEnergy []float64
	// Change accumulator for hysteresis
	changeAccum []float64
}

// NewTFAnalyzer creates a new analyzer for TF resolution decisions.
func NewTFAnalyzer(numBands int) *TFAnalyzer {
	return &TFAnalyzer{
		prevEnergy:  make([]float64, numBands),
		changeAccum: make([]float64, numBands),
	}
}

// Analyze determines the optimal TF resolution for each band.
// Returns TF=1 for bands with transients (need time resolution),
// TF=0 for bands with stationary content (need frequency resolution).
func (ta *TFAnalyzer) Analyze(bandEnergy *BandEnergy) *TFResolution {
	numBands := len(bandEnergy.LogEnergy)

	// Ensure we have storage
	if len(ta.prevEnergy) != numBands {
		ta.prevEnergy = make([]float64, numBands)
		ta.changeAccum = make([]float64, numBands)
	}

	tf := &TFResolution{
		TF:       make([]int, numBands),
		NumBands: numBands,
	}

	// Energy change threshold for transient detection (in dB)
	const transientThreshold = 6.0

	for i := 0; i < numBands; i++ {
		if !bandEnergy.Valid[i] {
			tf.TF[i] = 0
			continue
		}

		// Compute energy change from previous frame
		energyChange := 0.0
		if ta.prevEnergy[i] > 0 {
			energyChange = math.Abs(bandEnergy.LogEnergy[i] - ta.prevEnergy[i])
		}

		// Accumulate change with decay
		ta.changeAccum[i] = ta.changeAccum[i]*0.7 + energyChange

		// High accumulated change indicates transient activity
		if ta.changeAccum[i] > transientThreshold {
			tf.TF[i] = 1 // Use time resolution
		} else {
			tf.TF[i] = 0 // Use frequency resolution
		}

		// Update previous energy
		ta.prevEnergy[i] = bandEnergy.LogEnergy[i]
	}

	return tf
}

// ApplyTFChange modifies the spectrum based on TF resolution decisions.
// When TF=1 (transient), the band coefficients are interleaved with
// time-domain subdivision. When TF=0, coefficients remain in standard order.
func ApplyTFChange(spectrum []float64, tf *TFResolution, bandStart, bandEnd int, shortBlocks bool) {
	if !shortBlocks || tf.TF[0] == 0 {
		// No change needed for long blocks or frequency resolution
		return
	}

	// For short blocks with TF=1, apply Haar wavelet-like interleaving
	// This effectively increases time resolution at the cost of frequency resolution
	n := bandEnd - bandStart
	if n < 2 {
		return
	}

	// Haar transform: split into sum and difference
	temp := make([]float64, n)
	copy(temp, spectrum[bandStart:bandEnd])

	half := n / 2
	for i := 0; i < half; i++ {
		sum := (temp[2*i] + temp[2*i+1]) * 0.7071067811865476 // 1/sqrt(2)
		diff := (temp[2*i] - temp[2*i+1]) * 0.7071067811865476
		spectrum[bandStart+i] = sum
		spectrum[bandStart+half+i] = diff
	}
}

// InvertTFChange reverses the TF change for decoding.
func InvertTFChange(spectrum []float64, tf *TFResolution, bandStart, bandEnd int, shortBlocks bool) {
	if !shortBlocks || tf.TF[0] == 0 {
		return
	}

	n := bandEnd - bandStart
	if n < 2 {
		return
	}

	// Inverse Haar transform
	temp := make([]float64, n)
	copy(temp, spectrum[bandStart:bandEnd])

	half := n / 2
	for i := 0; i < half; i++ {
		sum := temp[i]
		diff := temp[half+i]
		spectrum[bandStart+2*i] = (sum + diff) * 0.7071067811865476
		spectrum[bandStart+2*i+1] = (sum - diff) * 0.7071067811865476
	}
}

// ApplySpreading applies spectral spreading to reduce tonal artifacts.
// This pre-rotates spectrum coefficients before quantization.
func ApplySpreading(spectrum []float64, mode SpreadingMode, bandStart, bandEnd int, seed uint32) {
	if mode == SpreadNone {
		return
	}

	n := bandEnd - bandStart
	if n < 2 {
		return
	}

	// Spreading factor increases with mode
	spreadFactors := []float64{0.0, 0.2, 0.4, 0.6}
	factor := spreadFactors[mode]

	// Apply spreading by adding controlled randomness
	// This distributes pulse energy more evenly
	for i := bandStart; i < bandEnd; i++ {
		seed = celtLCGRand(seed)
		noise := float64(int32(seed)) / float64(1<<31) * factor
		spectrum[i] = spectrum[i]*(1.0-factor*0.5) + noise*math.Abs(spectrum[i])
	}
}

// RemoveSpreading removes the spreading effect during decoding.
// Note: exact inversion is not possible since spreading adds noise,
// but this function provides a reasonable approximation.
func RemoveSpreading(spectrum []float64, mode SpreadingMode, bandStart, bandEnd int) {
	// Spreading removal is approximate - mainly involves renormalization
	if mode == SpreadNone {
		return
	}

	n := bandEnd - bandStart
	if n < 2 {
		return
	}

	// Renormalize to unit energy
	sumSq := 0.0
	for i := bandStart; i < bandEnd; i++ {
		sumSq += spectrum[i] * spectrum[i]
	}
	if sumSq > 1e-10 {
		scale := 1.0 / math.Sqrt(sumSq)
		for i := bandStart; i < bandEnd; i++ {
			spectrum[i] *= scale
		}
	}
}

// computeSpectralFlatness computes the spectral flatness measure.
// Returns 0 for pure tone, 1 for white noise.
func computeSpectralFlatness(spectrum []float64) float64 {
	if len(spectrum) == 0 {
		return 0.5
	}

	// Spectral flatness = geometric mean / arithmetic mean
	// Use log domain for numerical stability
	n := len(spectrum)
	logSum := 0.0
	arithSum := 0.0

	for _, v := range spectrum {
		absV := math.Abs(v) + 1e-10
		logSum += math.Log(absV)
		arithSum += absV
	}

	geometricMean := math.Exp(logSum / float64(n))
	arithmeticMean := arithSum / float64(n)

	if arithmeticMean < 1e-10 {
		return 0.5
	}

	flatness := geometricMean / arithmeticMean
	return math.Min(1.0, math.Max(0.0, flatness))
}

// computeHFRatio computes the ratio of high-frequency to total energy.
func computeHFRatio(spectrum []float64) float64 {
	if len(spectrum) < 4 {
		return 0.5
	}

	totalEnergy := 0.0
	hfEnergy := 0.0
	threshold := len(spectrum) * 3 / 4 // Top quarter is "high frequency"

	for i, v := range spectrum {
		energy := v * v
		totalEnergy += energy
		if i >= threshold {
			hfEnergy += energy
		}
	}

	if totalEnergy < 1e-10 {
		return 0.0
	}

	return hfEnergy / totalEnergy
}

// hysteresisDecision implements hysteresis-based decision making.
// This prevents rapid switching between modes when values are near thresholds.
func hysteresisDecision(value int, thresholds, hysteresis []int, prev int) int {
	n := len(thresholds)
	decision := n // Default to highest level

	for i := 0; i < n; i++ {
		if value < thresholds[i] {
			decision = i
			break
		}
	}

	// Apply hysteresis
	if decision > prev && prev < n && value < thresholds[prev]+hysteresis[prev] {
		decision = prev
	}
	if decision < prev && prev > 0 && value > thresholds[prev-1]-hysteresis[prev-1] {
		decision = prev
	}

	return decision
}

// celtLCGRand is the CELT linear congruential generator for deterministic noise.
func celtLCGRand(seed uint32) uint32 {
	return 1664525*seed + 1013904223
}

// EncodeTFSelect encodes the TF resolution selection for a frame.
// Returns the number of bits used.
func EncodeTFSelect(enc *RangeEncoder, tf *TFResolution, isTransient bool) int {
	// TF selection is signaled with 1 bit per band when there are changes
	bits := 0

	if isTransient {
		// For transient frames, TF can vary per band
		for i := 0; i < tf.NumBands; i++ {
			enc.EncodeBits(uint32(tf.TF[i]), 1)
			bits++
		}
	} else {
		// For non-transient frames, use uniform TF
		// Signal with single bit
		tf0 := 0
		if tf.NumBands > 0 {
			tf0 = tf.TF[0]
		}
		enc.EncodeBits(uint32(tf0), 1)
		bits++
	}

	return bits
}

// DecodeTFSelect decodes the TF resolution selection from a frame.
func DecodeTFSelect(dec *RangeDecoder, numBands int, isTransient bool) *TFResolution {
	tf := &TFResolution{
		TF:       make([]int, numBands),
		NumBands: numBands,
	}

	if isTransient {
		// Decode per-band TF
		for i := 0; i < numBands; i++ {
			tf.TF[i] = int(dec.DecodeBits(1))
		}
	} else {
		// Uniform TF
		tf0 := int(dec.DecodeBits(1))
		for i := 0; i < numBands; i++ {
			tf.TF[i] = tf0
		}
	}

	return tf
}

// EncodeSpread encodes the spreading mode for a frame.
func EncodeSpread(enc *RangeEncoder, mode SpreadingMode) {
	// Spreading mode is encoded with 2 bits (4 possible values)
	enc.EncodeBits(uint32(mode), 2)
}

// DecodeSpread decodes the spreading mode from a frame.
func DecodeSpread(dec *RangeDecoder) SpreadingMode {
	return SpreadingMode(dec.DecodeBits(2))
}
