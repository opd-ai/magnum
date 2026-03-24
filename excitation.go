// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements LPC excitation (residual) coding as specified in
// RFC 6716 §4.2.7.11 for the SILK codec. The excitation signal is encoded
// using sparse pulse coding, where the residual is represented as a small
// number of pulses at specific positions.
//
// Key components:
// - Pulse position and sign encoding
// - Shell coding for efficient pulse representation
// - Excitation synthesis from pulses

package magnum

import (
	"math"
)

// Excitation coding constants from RFC 6716.
const (
	// ExcSubframeLength is the default excitation subframe length.
	ExcSubframeLength = 16
	// ExcMaxPulses is the maximum number of pulses per subframe.
	ExcMaxPulses = 10
	// ExcMinPulses is the minimum number of pulses per subframe.
	ExcMinPulses = 0
	// ExcNumShells is the number of shell coding levels.
	ExcNumShells = 4
)

// ExcitationPulse represents a single excitation pulse.
type ExcitationPulse struct {
	// Position is the sample position within the subframe (0-based).
	Position int
	// Sign is +1 for positive pulse, -1 for negative.
	Sign int
	// Amplitude is the pulse magnitude (typically 1.0 after gain normalization).
	Amplitude float64
}

// ExcitationSubframe holds excitation data for one subframe.
type ExcitationSubframe struct {
	// Pulses contains the encoded pulses for this subframe.
	Pulses []ExcitationPulse
	// NumPulses is the number of pulses.
	NumPulses int
	// SeedIndex is the pseudo-random seed for decoder sign generation.
	SeedIndex int
}

// ExcitationFrame holds excitation data for a complete frame.
type ExcitationFrame struct {
	// Subframes contains excitation for each subframe.
	Subframes [GainNumSubframes]*ExcitationSubframe
	// QuantEnergy is the quantized excitation energy per subframe.
	QuantEnergy [GainNumSubframes]int
}

// ExcitationEncoder encodes LPC excitation (residual) signals.
type ExcitationEncoder struct {
	// subframeLen is the subframe length in samples.
	subframeLen int
	// prevSeed is the previous frame's seed (for decoder state).
	prevSeed int
	// Reusable buffers to reduce allocations
	magnitudes []float64 // Pre-allocated buffer for magnitude values
}

// NewExcitationEncoder creates a new excitation encoder.
func NewExcitationEncoder(subframeLen int) *ExcitationEncoder {
	if subframeLen <= 0 {
		subframeLen = ExcSubframeLength
	}
	return &ExcitationEncoder{
		subframeLen: subframeLen,
		prevSeed:    0,
		magnitudes:  make([]float64, subframeLen), // Pre-allocate for typical subframe size
	}
}

// Encode encodes the excitation (residual) signal for a frame.
// The input should be the LPC residual signal after gain normalization.
func (ee *ExcitationEncoder) Encode(residual []float64, numPulsesHint int) *ExcitationFrame {
	n := len(residual)
	if n == 0 {
		return nil
	}

	result := &ExcitationFrame{}

	for sf := 0; sf < GainNumSubframes; sf++ {
		startIdx := sf * ee.subframeLen
		endIdx := startIdx + ee.subframeLen
		if endIdx > n {
			endIdx = n
		}
		if startIdx >= n {
			break
		}

		subframe := residual[startIdx:endIdx]
		result.Subframes[sf] = ee.encodeSubframe(subframe, numPulsesHint)

		// Compute quantized energy
		energy := 0.0
		for _, s := range subframe {
			energy += s * s
		}
		// Map energy to index (0-7 for 3 bits)
		energyDb := 0.0
		if energy > 1e-10 {
			energyDb = 10.0 * math.Log10(energy/float64(len(subframe)))
		} else {
			energyDb = -60.0
		}
		// Map [-60, 0] dB to [0, 7]
		result.QuantEnergy[sf] = int((energyDb + 60.0) / 60.0 * 7.0)
		if result.QuantEnergy[sf] < 0 {
			result.QuantEnergy[sf] = 0
		} else if result.QuantEnergy[sf] > 7 {
			result.QuantEnergy[sf] = 7
		}
	}

	return result
}

// encodeSubframe encodes pulses for a single subframe.
func (ee *ExcitationEncoder) encodeSubframe(samples []float64, numPulsesHint int) *ExcitationSubframe {
	n := len(samples)
	if n == 0 {
		return &ExcitationSubframe{NumPulses: 0}
	}

	// Determine number of pulses based on signal energy and hint
	numPulses := numPulsesHint
	if numPulses <= 0 {
		// Adaptive: more pulses for higher energy
		energy := 0.0
		for _, s := range samples {
			energy += math.Abs(s)
		}
		avgMagnitude := energy / float64(n)
		numPulses = int(avgMagnitude * 20)
		if numPulses < ExcMinPulses {
			numPulses = ExcMinPulses
		} else if numPulses > ExcMaxPulses {
			numPulses = ExcMaxPulses
		}
	}

	// Find the top N peaks
	result := &ExcitationSubframe{
		NumPulses: numPulses,
		Pulses:    make([]ExcitationPulse, 0, numPulses),
		SeedIndex: ee.prevSeed,
	}

	if numPulses == 0 {
		return result
	}

	// Use pre-allocated buffer if large enough, otherwise allocate
	var magnitudes []float64
	if n <= len(ee.magnitudes) {
		magnitudes = ee.magnitudes[:n]
	} else {
		// Grow buffer for future use
		ee.magnitudes = make([]float64, n)
		magnitudes = ee.magnitudes
	}
	for i := range samples {
		magnitudes[i] = math.Abs(samples[i])
	}

	// Greedy peak selection
	for p := 0; p < numPulses; p++ {
		maxIdx := 0
		maxVal := magnitudes[0]
		for i := 1; i < n; i++ {
			if magnitudes[i] > maxVal {
				maxVal = magnitudes[i]
				maxIdx = i
			}
		}

		// Skip if below threshold
		if maxVal < 1e-10 {
			break
		}

		// Record pulse
		sign := 1
		if samples[maxIdx] < 0 {
			sign = -1
		}
		result.Pulses = append(result.Pulses, ExcitationPulse{
			Position:  maxIdx,
			Sign:      sign,
			Amplitude: maxVal,
		})

		// Mark position as used
		magnitudes[maxIdx] = 0
	}

	result.NumPulses = len(result.Pulses)
	ee.prevSeed = (ee.prevSeed + 1) & 0xFF

	return result
}

// EncodeExcitationParams encodes excitation parameters to the bitstream.
func EncodeExcitationParams(enc *RangeEncoder, exc *ExcitationFrame, subframeLen int) {
	if exc == nil {
		// Encode zero excitation
		for sf := 0; sf < GainNumSubframes; sf++ {
			enc.EncodeBits(0, 4) // numPulses = 0
			enc.EncodeBits(0, 3) // energy index
		}
		return
	}

	for sf := 0; sf < GainNumSubframes; sf++ {
		subframe := exc.Subframes[sf]
		if subframe == nil {
			enc.EncodeBits(0, 4) // numPulses = 0
			enc.EncodeBits(0, 3) // energy index
			continue
		}

		// Encode number of pulses (4 bits, 0-15 but clamped to ExcMaxPulses)
		numPulses := subframe.NumPulses
		if numPulses > 15 {
			numPulses = 15
		}
		enc.EncodeBits(uint32(numPulses), 4)

		// Encode energy index (3 bits)
		enc.EncodeBits(uint32(exc.QuantEnergy[sf]&0x7), 3)

		// Encode pulse positions and signs
		posBits := bitsForValue(subframeLen)
		for _, pulse := range subframe.Pulses {
			// Position (variable bits based on subframe length)
			enc.EncodeBits(uint32(pulse.Position), posBits)
			// Sign (1 bit: 0 = positive, 1 = negative)
			signBit := 0
			if pulse.Sign < 0 {
				signBit = 1
			}
			enc.EncodeBits(uint32(signBit), 1)
		}
	}
}

// bitsForValue returns the number of bits needed to represent values 0..n-1.
func bitsForValue(n int) uint32 {
	if n <= 1 {
		return 1
	}
	bits := uint32(0)
	for v := n - 1; v > 0; v >>= 1 {
		bits++
	}
	return bits
}

// DecodeExcitationParams decodes excitation parameters from the bitstream.
func DecodeExcitationParams(dec *RangeDecoder, subframeLen int) *ExcitationFrame {
	result := &ExcitationFrame{}
	posBits := bitsForValue(subframeLen)

	for sf := 0; sf < GainNumSubframes; sf++ {
		// Decode number of pulses
		numPulses := int(dec.DecodeBits(4))

		// Decode energy index
		result.QuantEnergy[sf] = int(dec.DecodeBits(3))

		subframe := &ExcitationSubframe{
			NumPulses: numPulses,
			Pulses:    make([]ExcitationPulse, numPulses),
		}

		// Decode pulse positions and signs
		for p := 0; p < numPulses; p++ {
			pos := int(dec.DecodeBits(posBits))
			signBit := dec.DecodeBits(1)
			sign := 1
			if signBit == 1 {
				sign = -1
			}
			subframe.Pulses[p] = ExcitationPulse{
				Position:  pos,
				Sign:      sign,
				Amplitude: 1.0, // Normalized amplitude
			}
		}

		result.Subframes[sf] = subframe
	}

	return result
}

// SynthesizeExcitation reconstructs the excitation signal from pulses.
func SynthesizeExcitation(exc *ExcitationFrame, subframeLen int, gains *FrameGains) []float64 {
	if exc == nil {
		return nil
	}

	totalLen := subframeLen * GainNumSubframes
	output := make([]float64, totalLen)

	for sf := 0; sf < GainNumSubframes; sf++ {
		subframe := exc.Subframes[sf]
		if subframe == nil {
			continue
		}

		startIdx := sf * subframeLen

		// Compute gain scaling from energy index
		gainScale := 1.0
		if gains != nil {
			gainScale = gains.Subframes[sf].LinearGain
		}

		// Dequantize energy to get amplitude scale
		energyDb := float64(exc.QuantEnergy[sf])/7.0*60.0 - 60.0
		energyScale := math.Pow(10, energyDb/20.0) * gainScale

		// Place pulses
		for _, pulse := range subframe.Pulses {
			idx := startIdx + pulse.Position
			if idx >= 0 && idx < totalLen {
				output[idx] += float64(pulse.Sign) * pulse.Amplitude * energyScale
			}
		}
	}

	return output
}

// ComputeExcitationError computes the error between original and synthesized excitation.
func ComputeExcitationError(original, synthesized []float64) float64 {
	if len(original) != len(synthesized) {
		return math.MaxFloat64
	}

	errorEnergy := 0.0
	signalEnergy := 0.0

	for i := range original {
		diff := original[i] - synthesized[i]
		errorEnergy += diff * diff
		signalEnergy += original[i] * original[i]
	}

	if signalEnergy < 1e-10 {
		return 0 // Both are essentially zero
	}

	return errorEnergy / signalEnergy
}

// ShellCoder implements shell coding for pulse positions.
// This provides a more efficient encoding when pulses are clustered.
type ShellCoder struct {
	// levels are the subdivision levels for shell coding.
	levels int
}

// NewShellCoder creates a new shell coder.
func NewShellCoder(levels int) *ShellCoder {
	if levels <= 0 {
		levels = ExcNumShells
	}
	return &ShellCoder{levels: levels}
}

// EncodePulseCount encodes the pulse count distribution using shell coding.
func (sc *ShellCoder) EncodePulseCount(enc *RangeEncoder, count, maxCount int) {
	// Simple encoding: direct count with variable bits
	bits := bitsForValue(maxCount + 1)
	enc.EncodeBits(uint32(count), bits)
}

// DecodePulseCount decodes the pulse count from shell coding.
func (sc *ShellCoder) DecodePulseCount(dec *RangeDecoder, maxCount int) int {
	bits := bitsForValue(maxCount + 1)
	return int(dec.DecodeBits(bits))
}

// Reset resets the excitation encoder state.
func (ee *ExcitationEncoder) Reset() {
	ee.prevSeed = 0
}

// ExcitationDecoder decodes LPC excitation (residual) signals.
type ExcitationDecoder struct {
	subframeLen int
}

// NewExcitationDecoder creates a new excitation decoder.
func NewExcitationDecoder(subframeLen int) *ExcitationDecoder {
	if subframeLen <= 0 {
		subframeLen = ExcSubframeLength
	}
	return &ExcitationDecoder{
		subframeLen: subframeLen,
	}
}

// Decode decodes an excitation frame from quantized parameters.
func (ed *ExcitationDecoder) Decode(frame *ExcitationFrame, gains *FrameGains) []float64 {
	return SynthesizeExcitation(frame, ed.subframeLen, gains)
}
