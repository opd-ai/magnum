// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements subframe gain coding as specified in RFC 6716 §4.2.7.3
// for the SILK codec. The gain coding system quantizes the energy of each
// subframe using a log-domain representation with prediction from previous
// subframes and frames.
//
// Key components:
// - Log-domain gain computation from signal energy
// - Delta coding with prediction from previous gains
// - Entropy-coded gain indices
// - Gain dequantization for the decoder

package magnum

import (
	"math"
)

// Gain quantization constants from RFC 6716.
const (
	// GainNumSubframes is the number of subframes per frame.
	GainNumSubframes = 4
	// GainQuantLevels is the number of quantization levels for delta gain.
	GainQuantLevels = 64
	// GainQuantStep is the quantization step size in dB.
	GainQuantStep = 1.0
	// GainMinDB is the minimum representable gain in dB.
	GainMinDB = -40.0
	// GainMaxDB is the maximum representable gain in dB.
	GainMaxDB = 40.0
	// GainPredCoeff is the prediction coefficient for gain smoothing.
	GainPredCoeff = 0.75
)

// SubframeGain holds gain information for a single subframe.
type SubframeGain struct {
	// LinearGain is the gain as a linear multiplier.
	LinearGain float64
	// LogGain is the gain in dB (20 * log10(LinearGain)).
	LogGain float64
	// QuantIndex is the quantized gain index.
	QuantIndex int
	// Delta is the delta from predicted gain.
	Delta float64
}

// FrameGains holds gain information for all subframes in a frame.
type FrameGains struct {
	// Subframes contains gains for each subframe.
	Subframes [GainNumSubframes]SubframeGain
	// FrameGainIndex is the frame-level base gain index.
	FrameGainIndex int
	// Type indicates the frame type (voiced, unvoiced, etc.).
	Type int
}

// GainCoder handles subframe gain coding and decoding.
type GainCoder struct {
	// prevGains holds the previous frame's gains for prediction.
	prevGains [GainNumSubframes]float64
	// prevFrameGain is the previous frame-level gain.
	prevFrameGain float64
	// predCoeff is the prediction coefficient.
	predCoeff float64
}

// NewGainCoder creates a new gain coder.
func NewGainCoder() *GainCoder {
	gc := &GainCoder{
		predCoeff: GainPredCoeff,
	}
	// Initialize previous gains to 0 dB (unity gain)
	for i := range gc.prevGains {
		gc.prevGains[i] = 0.0
	}
	gc.prevFrameGain = 0.0
	return gc
}

// ComputeGains computes subframe gains from the input signal.
// Returns the gain structure with computed and quantized gains.
func (gc *GainCoder) ComputeGains(samples []float64, subframeLength int) *FrameGains {
	result := &FrameGains{}
	n := len(samples)

	for sf := 0; sf < GainNumSubframes; sf++ {
		startIdx := sf * subframeLength
		endIdx := startIdx + subframeLength
		if endIdx > n {
			endIdx = n
		}

		// Compute subframe energy
		energy := 0.0
		count := 0
		for i := startIdx; i < endIdx; i++ {
			energy += samples[i] * samples[i]
			count++
		}

		if count > 0 {
			energy /= float64(count) // RMS squared
		}

		// Convert to dB scale
		var logGain float64
		if energy > 1e-20 {
			// dB = 10 * log10(energy) = 20 * log10(sqrt(energy)) = 10 * log10(RMS²)
			logGain = 10.0 * math.Log10(energy)
		} else {
			logGain = GainMinDB
		}

		// Clamp to valid range
		if logGain < GainMinDB {
			logGain = GainMinDB
		} else if logGain > GainMaxDB {
			logGain = GainMaxDB
		}

		// Compute prediction from previous subframe
		var predicted float64
		if sf == 0 {
			predicted = gc.prevFrameGain * gc.predCoeff
		} else {
			predicted = result.Subframes[sf-1].LogGain * gc.predCoeff
		}

		// Compute delta from prediction
		delta := logGain - predicted

		// Quantize delta
		quantIndex := gc.quantizeDelta(delta)

		// Dequantize to get actual transmitted gain
		dequantDelta := gc.dequantizeDelta(quantIndex)
		quantizedLogGain := predicted + dequantDelta

		// Store results
		result.Subframes[sf] = SubframeGain{
			LinearGain: math.Pow(10, quantizedLogGain/20.0),
			LogGain:    quantizedLogGain,
			QuantIndex: quantIndex,
			Delta:      delta,
		}
	}

	// Compute frame-level gain index (based on first subframe)
	result.FrameGainIndex = gc.computeFrameGainIndex(result.Subframes[0].LogGain)

	// Update state for next frame
	for sf := 0; sf < GainNumSubframes; sf++ {
		gc.prevGains[sf] = result.Subframes[sf].LogGain
	}
	gc.prevFrameGain = result.Subframes[GainNumSubframes-1].LogGain

	return result
}

// quantizeDelta quantizes a delta gain value.
func (gc *GainCoder) quantizeDelta(delta float64) int {
	// Uniform quantization centered at 0
	index := int(math.Round(delta/GainQuantStep)) + GainQuantLevels/2

	// Clamp to valid range
	if index < 0 {
		index = 0
	} else if index >= GainQuantLevels {
		index = GainQuantLevels - 1
	}

	return index
}

// dequantizeDelta dequantizes a gain index to delta value.
func (gc *GainCoder) dequantizeDelta(index int) float64 {
	return float64(index-GainQuantLevels/2) * GainQuantStep
}

// computeFrameGainIndex computes the frame-level gain index.
func (gc *GainCoder) computeFrameGainIndex(baseGain float64) int {
	// Map gain from [GainMinDB, GainMaxDB] to [0, 255]
	normalized := (baseGain - GainMinDB) / (GainMaxDB - GainMinDB)
	index := int(normalized * 255.0)
	if index < 0 {
		index = 0
	} else if index > 255 {
		index = 255
	}
	return index
}

// DecodeGains decodes subframe gains from quantization indices.
func (gc *GainCoder) DecodeGains(indices []int) *FrameGains {
	result := &FrameGains{}

	for sf := 0; sf < GainNumSubframes && sf < len(indices); sf++ {
		// Compute prediction
		var predicted float64
		if sf == 0 {
			predicted = gc.prevFrameGain * gc.predCoeff
		} else {
			predicted = result.Subframes[sf-1].LogGain * gc.predCoeff
		}

		// Dequantize delta and add to prediction
		dequantDelta := gc.dequantizeDelta(indices[sf])
		logGain := predicted + dequantDelta

		// Store results
		result.Subframes[sf] = SubframeGain{
			LinearGain: math.Pow(10, logGain/20.0),
			LogGain:    logGain,
			QuantIndex: indices[sf],
			Delta:      dequantDelta,
		}
	}

	// Update state for next frame
	for sf := 0; sf < GainNumSubframes; sf++ {
		gc.prevGains[sf] = result.Subframes[sf].LogGain
	}
	gc.prevFrameGain = result.Subframes[GainNumSubframes-1].LogGain

	return result
}

// ApplyGains applies subframe gains to a signal.
// Scales each subframe by its linear gain.
func ApplyGains(samples []float64, gains *FrameGains, subframeLength int) []float64 {
	if gains == nil {
		return samples
	}

	output := make([]float64, len(samples))
	n := len(samples)

	for sf := 0; sf < GainNumSubframes; sf++ {
		startIdx := sf * subframeLength
		endIdx := startIdx + subframeLength
		if endIdx > n {
			endIdx = n
		}

		gain := gains.Subframes[sf].LinearGain
		for i := startIdx; i < endIdx; i++ {
			output[i] = samples[i] * gain
		}
	}

	return output
}

// NormalizeByGains normalizes a signal by subframe gains (inverse of ApplyGains).
func NormalizeByGains(samples []float64, gains *FrameGains, subframeLength int) []float64 {
	if gains == nil {
		return samples
	}

	output := make([]float64, len(samples))
	n := len(samples)

	for sf := 0; sf < GainNumSubframes; sf++ {
		startIdx := sf * subframeLength
		endIdx := startIdx + subframeLength
		if endIdx > n {
			endIdx = n
		}

		gain := gains.Subframes[sf].LinearGain
		if gain < 1e-10 {
			gain = 1e-10 // Avoid division by zero
		}
		invGain := 1.0 / gain

		for i := startIdx; i < endIdx; i++ {
			output[i] = samples[i] * invGain
		}
	}

	return output
}

// EncodeGains encodes gain indices to the bitstream.
func EncodeGains(enc *RangeEncoder, gains *FrameGains) {
	if gains == nil {
		// Encode zero gains
		for sf := 0; sf < GainNumSubframes; sf++ {
			enc.EncodeBits(uint32(GainQuantLevels/2), 6) // 6 bits for 64 levels
		}
		return
	}

	// Encode frame-level gain index (8 bits)
	enc.EncodeBits(uint32(gains.FrameGainIndex&0xFF), 8)

	// Encode subframe delta indices (6 bits each)
	for sf := 0; sf < GainNumSubframes; sf++ {
		idx := gains.Subframes[sf].QuantIndex
		if idx < 0 {
			idx = 0
		} else if idx >= GainQuantLevels {
			idx = GainQuantLevels - 1
		}
		enc.EncodeBits(uint32(idx), 6)
	}
}

// DecodeGainsFromBitstream decodes gain indices from the bitstream.
func DecodeGainsFromBitstream(dec *RangeDecoder, gc *GainCoder) *FrameGains {
	result := &FrameGains{}

	// Decode frame-level gain index
	result.FrameGainIndex = int(dec.DecodeBits(8))

	// Decode subframe delta indices
	indices := make([]int, GainNumSubframes)
	for sf := 0; sf < GainNumSubframes; sf++ {
		indices[sf] = int(dec.DecodeBits(6))
	}

	// Decode gains using the coder
	decoded := gc.DecodeGains(indices)
	result.Subframes = decoded.Subframes

	return result
}

// Reset resets the gain coder state.
func (gc *GainCoder) Reset() {
	for i := range gc.prevGains {
		gc.prevGains[i] = 0.0
	}
	gc.prevFrameGain = 0.0
}

// ComputeSignalEnergy computes the RMS energy of a signal in dB.
func ComputeSignalEnergy(samples []float64) float64 {
	if len(samples) == 0 {
		return GainMinDB
	}

	energy := 0.0
	for _, s := range samples {
		energy += s * s
	}
	energy /= float64(len(samples))

	if energy < 1e-20 {
		return GainMinDB
	}

	return 10.0 * math.Log10(energy)
}

// LinearToDb converts a linear gain value to dB.
func LinearToDb(linear float64) float64 {
	if linear <= 0 {
		return GainMinDB
	}
	return 20.0 * math.Log10(linear)
}

// DbToLinear converts a dB gain value to linear.
func DbToLinear(db float64) float64 {
	return math.Pow(10, db/20.0)
}
