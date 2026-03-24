// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements the CELT frame assembly as specified in RFC 6716 §4.3.
// A CELT frame consists of:
// 1. Silence flag (1 bit)
// 2. Post-filter parameters (optional, for voiced audio)
// 3. Transient flag (1 bit for non-transient modes)
// 4. Intra-frame flag (1 bit, controls energy prediction mode)
// 5. Coarse energy (entropy coded)
// 6. TF change parameters
// 7. Spreading decision
// 8. Band allocation
// 9. Fine energy (non-entropy coded)
// 10. Spectral data (PVQ coded)
//
// The encoder wires together all the subcomponents (range coder, energy coding,
// PVQ) into a complete CELT bitstream.

package magnum

import (
	"math"
)

// CELTFrameConfig holds configuration for CELT frame encoding.
type CELTFrameConfig struct {
	// SampleRate is the audio sample rate (24000 or 48000 Hz)
	SampleRate int
	// Channels is the number of audio channels (1 or 2)
	Channels int
	// FrameSize is the number of samples per frame (120, 240, 480, 960)
	FrameSize int
	// Bitrate is the target bitrate in bits per second
	Bitrate int
}

// CELTFrameEncoder encodes CELT frames following RFC 6716 §4.3.
type CELTFrameEncoder struct {
	config   CELTFrameConfig
	mdct     *MDCT
	pvq      *PVQ
	eq       *EnergyQuantizer
	spread   *SpreadingAnalyzer
	tf       *TFAnalyzer
	prevMDCT []float64 // Previous frame MDCT for overlap-add

	// Frame counters for intra/inter decisions
	frameCount int

	// Reusable working buffers to reduce allocations
	mdctCoeffs []float64 // MDCT output buffer (frameSize/2)
	normCoeffs []float64 // Normalized coefficients buffer
	pvqPulses  []int     // PVQ pulse buffer (max band size)
	pvqSigns   []int     // PVQ signs buffer (max band size)
}

// NewCELTFrameEncoder creates a new CELT frame encoder.
func NewCELTFrameEncoder(config CELTFrameConfig) (*CELTFrameEncoder, error) {
	// Validate configuration
	if config.SampleRate != 24000 && config.SampleRate != 48000 {
		return nil, ErrInvalidSampleRate
	}
	if config.Channels < 1 || config.Channels > 2 {
		return nil, ErrInvalidChannels
	}

	// Determine MDCT size (same as frame size for CELT)
	mdctSize := config.FrameSize
	if !isValidFrameSize(mdctSize) {
		return nil, ErrInvalidFrameSize
	}

	mdct := NewMDCT(mdctSize)
	if mdct == nil {
		return nil, ErrInvalidFrameSize
	}

	// Determine maximum band size for buffer allocation
	// CELT bands range from 1 to ~192 coefficients; allocate for largest
	maxBandSize := config.FrameSize / 2 // Safe upper bound

	// Compute LM (log2 of frame duration) for energy quantization prediction coefficients
	// LM = log2(frameSize / 120) where 120 is the minimum CELT frame size at 48kHz (2.5ms)
	lm := computeLM(config.FrameSize)

	return &CELTFrameEncoder{
		config:     config,
		mdct:       mdct,
		pvq:        NewPVQ(),
		eq:         NewEnergyQuantizer(lm),
		spread:     NewSpreadingAnalyzer(),
		tf:         NewTFAnalyzer(NumCELTBands),
		prevMDCT:   make([]float64, config.FrameSize),
		frameCount: 0,
		// Pre-allocate working buffers
		mdctCoeffs: make([]float64, config.FrameSize/2),
		normCoeffs: make([]float64, maxBandSize),
		pvqPulses:  make([]int, maxBandSize),
		pvqSigns:   make([]int, maxBandSize),
	}, nil
}

// isValidFrameSize checks if the frame size is valid for CELT.
func isValidFrameSize(size int) bool {
	switch size {
	case 120, 240, 480, 960:
		return true
	default:
		return false
	}
}

// CELTEncodedFrame holds the encoded CELT frame data.
type CELTEncodedFrame struct {
	// Data is the encoded bitstream
	Data []byte
	// Bits is the number of bits used
	Bits int
	// IsSilence indicates if this is a silence frame
	IsSilence bool
	// IsTransient indicates if transient mode was used
	IsTransient bool
}

// EncodeFrame encodes a single CELT frame from PCM samples.
func (enc *CELTFrameEncoder) EncodeFrame(samples []float64) (*CELTEncodedFrame, error) {
	if len(samples) != enc.config.FrameSize {
		return nil, ErrInvalidFrameSize
	}

	// Detect silence and encode silence frame if applicable
	energy := computeFrameEnergy(samples)
	isSilence := energy < 1e-10

	rc := NewRangeEncoder()
	if isSilence {
		rc.EncodeBits(1, 1)
		return &CELTEncodedFrame{Data: rc.Bytes(), Bits: 1, IsSilence: true}, nil
	}
	rc.EncodeBits(0, 1)

	// Compute spectral representation using pre-allocated buffer
	enc.mdct.ForwardInto(samples, enc.mdctCoeffs)
	bandEnergy := ComputeBandEnergy(enc.mdctCoeffs)
	isTransient := detectTransient(samples, enc.prevMDCT)

	// Encode frame flags
	isIntra := enc.encodeFrameFlags(rc, isTransient)

	// Quantize and encode energy
	quantizedEnergy := enc.encodeCoarseEnergy(rc, bandEnergy, isIntra)

	// Encode TF and spreading
	enc.encodeTFAndSpreading(rc, bandEnergy, enc.mdctCoeffs)

	// Encode spectral coefficients with PVQ
	enc.encodePVQBands(rc, enc.mdctCoeffs, bandEnergy)

	// Encode fine energy
	enc.encodeFineEnergy(rc, quantizedEnergy)

	// Update state for next frame
	copy(enc.prevMDCT, enc.mdctCoeffs)
	enc.frameCount++

	return &CELTEncodedFrame{
		Data:        rc.Bytes(),
		Bits:        computeUsedBits(rc),
		IsTransient: isTransient,
	}, nil
}

// encodeFrameFlags encodes transient and intra flags.
func (enc *CELTFrameEncoder) encodeFrameFlags(rc *RangeEncoder, isTransient bool) bool {
	transientFlag := 0
	if isTransient {
		transientFlag = 1
	}
	rc.EncodeBits(uint32(transientFlag), 1)

	isIntra := enc.frameCount%10 == 0
	intraFlag := 0
	if isIntra {
		intraFlag = 1
	}
	rc.EncodeBits(uint32(intraFlag), 1)
	return isIntra
}

// encodeCoarseEnergy quantizes and encodes coarse band energies.
func (enc *CELTFrameEncoder) encodeCoarseEnergy(rc *RangeEncoder, bandEnergy *BandEnergy, isIntra bool) *QuantizedEnergy {
	quantizedEnergy := enc.eq.QuantizeCoarse(bandEnergy.LogEnergy, isIntra)

	for i := 0; i < NumCELTBands; i++ {
		val := quantizedEnergy.CoarseQuant[i]
		encoded := val + 64
		if encoded < 0 {
			encoded = 0
		}
		if encoded > 127 {
			encoded = 127
		}
		rc.EncodeBits(uint32(encoded), 7)
	}
	return quantizedEnergy
}

// encodeTFAndSpreading encodes TF select and spreading mode.
func (enc *CELTFrameEncoder) encodeTFAndSpreading(rc *RangeEncoder, bandEnergy *BandEnergy, mdctCoeffs []float64) {
	tfRes := enc.tf.Analyze(bandEnergy)
	EncodeTFSelect(rc, tfRes, false)

	spreadMode := enc.spread.Analyze(mdctCoeffs, NumCELTBands)
	EncodeSpread(rc, spreadMode)
}

// encodePVQBands encodes spectral coefficients for all bands using PVQ.
func (enc *CELTFrameEncoder) encodePVQBands(rc *RangeEncoder, mdctCoeffs []float64, bandEnergy *BandEnergy) {
	spectrumSize := len(mdctCoeffs)
	starts, ends := ScaledBandBoundaries(spectrumSize)

	totalBits := enc.config.Bitrate * enc.config.FrameSize / enc.config.SampleRate
	usedBits := computeUsedBits(rc)
	pvqBits := totalBits - usedBits - 50
	if pvqBits < 0 {
		pvqBits = 0
	}

	bandBits := AllocateBandBits(pvqBits, NumCELTBands, bandEnergy,
		enc.config.Bitrate, enc.config.SampleRate, enc.config.FrameSize)

	// Use pre-allocated buffer for normalized coefficients
	normalizeSpectrumInto(mdctCoeffs, bandEnergy, enc.normCoeffs)
	normalizedCoeffs := enc.normCoeffs[:spectrumSize]
	spreadMode := enc.spread.Analyze(mdctCoeffs, NumCELTBands)

	for i := 0; i < NumCELTBands; i++ {
		ApplySpreading(normalizedCoeffs, spreadMode, starts[i], ends[i], uint32(enc.frameCount+i))
	}

	for i := 0; i < NumCELTBands; i++ {
		enc.encodePVQBand(rc, normalizedCoeffs, bandEnergy, bandBits, starts, ends, i)
	}
}

// encodePVQBand encodes a single band using PVQ.
func (enc *CELTFrameEncoder) encodePVQBand(rc *RangeEncoder, normalizedCoeffs []float64, bandEnergy *BandEnergy, bandBits []int, starts, ends [NumCELTBands]int, bandIdx int) {
	if !bandEnergy.Valid[bandIdx] || bandBits[bandIdx] <= 0 {
		return
	}

	n := ends[bandIdx] - starts[bandIdx]
	if n <= 0 || starts[bandIdx] >= len(normalizedCoeffs) || ends[bandIdx] > len(normalizedCoeffs) {
		return
	}

	// Use slice reference instead of allocating a new buffer
	bandCoeffs := normalizedCoeffs[starts[bandIdx]:ends[bandIdx]]

	k := enc.pvq.SelectK(n, bandBits[bandIdx])
	if k == 0 {
		return
	}

	rc.EncodeBits(uint32(k), 5)
	// Use EncodeIndex to avoid PVQCodeword allocation
	index := enc.pvq.EncodeIndex(bandCoeffs, k, enc.pvqPulses[:n], enc.pvqSigns[:n])

	v := enc.pvq.V(n, k)
	if v > 1 {
		bits := enc.pvq.BitsRequired(n, k)
		rc.EncodeBits(uint32(index), uint32(bits))
	}
}

// encodeFineEnergy encodes fine energy bits.
func (enc *CELTFrameEncoder) encodeFineEnergy(rc *RangeEncoder, quantizedEnergy *QuantizedEnergy) {
	enc.eq.QuantizeFine(quantizedEnergy, NumCELTBands*2)

	for i := 0; i < NumCELTBands; i++ {
		val := quantizedEnergy.FineQuant[i]
		if val < 0 {
			val = 0
		}
		if val > 3 {
			val = 3
		}
		rc.EncodeBits(uint32(val), 2)
	}
}

// CELTFrameDecoder decodes CELT frames following RFC 6716 §4.3.
type CELTFrameDecoder struct {
	config   CELTFrameConfig
	mdct     *MDCT
	pvq      *PVQ
	eq       *EnergyQuantizer
	prevMDCT []float64 // Previous frame MDCT for overlap-add
}

// NewCELTFrameDecoder creates a new CELT frame decoder.
func NewCELTFrameDecoder(config CELTFrameConfig) (*CELTFrameDecoder, error) {
	// Validate configuration
	if config.SampleRate != 24000 && config.SampleRate != 48000 {
		return nil, ErrInvalidSampleRate
	}
	if config.Channels < 1 || config.Channels > 2 {
		return nil, ErrInvalidChannels
	}

	// Determine MDCT size (same as frame size for CELT)
	mdctSize := config.FrameSize
	if !isValidFrameSize(mdctSize) {
		return nil, ErrInvalidFrameSize
	}

	mdct := NewMDCT(mdctSize)
	if mdct == nil {
		return nil, ErrInvalidFrameSize
	}

	// Compute LM (log2 of frame duration) for energy quantization prediction coefficients
	// LM = log2(frameSize / 120) where 120 is the minimum CELT frame size at 48kHz (2.5ms)
	lm := computeLM(config.FrameSize)

	return &CELTFrameDecoder{
		config:   config,
		mdct:     mdct,
		pvq:      NewPVQ(),
		eq:       NewEnergyQuantizer(lm),
		prevMDCT: make([]float64, config.FrameSize),
	}, nil
}

// DecodeFrame decodes a single CELT frame to PCM samples.
func (dec *CELTFrameDecoder) DecodeFrame(data []byte) ([]float64, error) {
	if len(data) == 0 {
		return nil, ErrInvalidPacket
	}

	rc := NewRangeDecoder(data)

	// Decode silence flag
	silenceFlag := rc.DecodeBits(1)
	if silenceFlag == 1 {
		// Return silence
		return make([]float64, dec.config.FrameSize), nil
	}

	// Decode transient flag
	isTransient := rc.DecodeBits(1) == 1

	// Decode intra flag
	isIntra := rc.DecodeBits(1) == 1

	// Decode coarse energy
	var coarseQuant [NumCELTBands]int
	for i := 0; i < NumCELTBands; i++ {
		encoded := int(rc.DecodeBits(7))
		coarseQuant[i] = encoded - 64 // Remove offset
	}

	// Decode TF select
	tfRes := DecodeTFSelect(rc, NumCELTBands, isTransient)
	_ = tfRes // Used for TF changes if needed

	// Decode spreading
	spreadMode := DecodeSpread(rc)

	// Decode fine energy before PVQ to use consistent energy for allocation
	// RFC 6716 §4.3 requires same energy for allocation and denormalization
	var fineQuant [NumCELTBands]int
	var fineBits [NumCELTBands]int
	for i := 0; i < NumCELTBands; i++ {
		fineQuant[i] = int(rc.DecodeBits(2))
		fineBits[i] = 2
	}

	// Compute bit budget using actual frame structure
	// Bits used so far: silence(1) + transient(1) + intra(1) + coarse(21*7) + TF(~21) + spread(2) + fine(21*2)
	totalBits := dec.config.Bitrate * dec.config.FrameSize / dec.config.SampleRate
	usedBits := 3 + NumCELTBands*7 + NumCELTBands + 2 + NumCELTBands*2
	pvqBits := totalBits - usedBits - 20 // Reserve 20 bits for remaining overhead
	if pvqBits < 0 {
		pvqBits = 0
	}

	// Dequantize energy using proper inter-frame prediction (RFC 6716 §4.3.2)
	// This ensures bit allocation uses the same energy as denormalization
	bandEnergy := dec.eq.Dequantize(coarseQuant, fineQuant, fineBits, isIntra)

	// Create band energy structure for allocation using the dequantized values
	allocEnergy := &BandEnergy{}
	for i := 0; i < NumCELTBands; i++ {
		allocEnergy.Valid[i] = true
		allocEnergy.LogEnergy[i] = bandEnergy[i]
	}
	bandBits := allocateBandBits(pvqBits, NumCELTBands, allocEnergy)

	// Get scaled band boundaries
	spectrumSize := dec.config.FrameSize / 2 // MDCT produces half the samples
	starts, ends := ScaledBandBoundaries(spectrumSize)

	// Decode PVQ coefficients
	normalizedCoeffs := make([]float64, spectrumSize)

	for i := 0; i < NumCELTBands; i++ {
		if !allocEnergy.Valid[i] || bandBits[i] <= 0 {
			continue
		}

		n := ends[i] - starts[i]
		if n <= 0 || starts[i] >= len(normalizedCoeffs) || ends[i] > len(normalizedCoeffs) {
			continue
		}

		// Decode K
		k := int(rc.DecodeBits(5))
		if k == 0 {
			continue
		}

		// Decode PVQ index
		v := dec.pvq.V(n, k)
		var index uint64
		if v > 1 {
			bits := dec.pvq.BitsRequired(n, k)
			index = uint64(rc.DecodeBits(uint32(bits)))
		}

		// Decode codeword
		cw := dec.pvq.DecodeFromIndex(index, n, k)
		decoded := dec.pvq.Decode(cw)

		// Copy to coefficients
		if len(decoded) == n {
			copy(normalizedCoeffs[starts[i]:ends[i]], decoded)
		}
	}

	// Remove spreading
	for i := 0; i < NumCELTBands; i++ {
		if starts[i] < len(normalizedCoeffs) && ends[i] <= len(normalizedCoeffs) {
			RemoveSpreading(normalizedCoeffs, spreadMode, starts[i], ends[i])
		}
	}

	// Denormalize spectrum using the already-dequantized band energy
	mdctCoeffs := denormalizeSpectrum(normalizedCoeffs, bandEnergy[:], dec.config.FrameSize)

	// Apply inverse MDCT
	inverted := dec.mdct.Inverse(mdctCoeffs)

	// Overlap-add with previous frame
	samples := make([]float64, dec.config.FrameSize/2)
	dec.mdct.OverlapAdd(dec.prevMDCT, inverted, samples)

	// Store for next frame
	copy(dec.prevMDCT, inverted)

	return samples, nil
}

// Helper functions

func computeFrameEnergy(samples []float64) float64 {
	energy := 0.0
	for _, s := range samples {
		energy += s * s
	}
	return energy / float64(len(samples))
}

func detectTransient(current, previous []float64) bool {
	if len(previous) == 0 {
		return false
	}

	// Simple transient detection: compare energy ratios
	n := len(current)
	quarterLen := n / 4

	// Compute energy in each quarter
	energies := make([]float64, 4)
	for q := 0; q < 4; q++ {
		for i := q * quarterLen; i < (q+1)*quarterLen; i++ {
			energies[q] += current[i] * current[i]
		}
	}

	// Check for sudden energy increase
	maxRatio := 0.0
	for q := 1; q < 4; q++ {
		if energies[q-1] > 1e-10 {
			ratio := energies[q] / energies[q-1]
			if ratio > maxRatio {
				maxRatio = ratio
			}
		}
	}

	// Transient if energy ratio exceeds threshold
	return maxRatio > 10.0
}

func computeLM(frameSize int) int {
	// LM = log2(frameSize / 120)
	switch frameSize {
	case 120:
		return 0
	case 240:
		return 1
	case 480:
		return 2
	case 960:
		return 3
	default:
		return 2 // Default
	}
}

func computeUsedBits(rc *RangeEncoder) int {
	// Estimate bits used so far
	bytes := rc.Bytes()
	return len(bytes) * 8
}

func allocateBandBits(totalBits, numBands int, energy *BandEnergy) []int {
	// Use the new bitrate allocation system with default parameters
	// This provides backward compatibility while connecting to the proper
	// CELT allocation table system (RFC 6716 §4.3.4)
	return AllocateBandBits(totalBits, numBands, energy, 64000, 48000, 960)
}

func normalizeSpectrum(mdctCoeffs []float64, energy *BandEnergy) []float64 {
	n := len(mdctCoeffs)
	normalized := make([]float64, n)
	normalizeSpectrumInto(mdctCoeffs, energy, normalized)
	return normalized
}

// normalizeSpectrumInto performs spectrum normalization into a pre-allocated buffer.
func normalizeSpectrumInto(mdctCoeffs []float64, energy *BandEnergy, out []float64) {
	n := len(mdctCoeffs)
	if len(out) < n {
		return
	}
	copy(out[:n], mdctCoeffs)

	starts, ends := ScaledBandBoundaries(n)

	for i := 0; i < NumCELTBands; i++ {
		if !energy.Valid[i] {
			continue
		}

		start := starts[i]
		end := ends[i]
		if start >= n || end > n {
			continue
		}

		// Compute band energy
		bandSum := 0.0
		for j := start; j < end; j++ {
			bandSum += out[j] * out[j]
		}

		// Normalize to unit energy
		if bandSum > 1e-10 {
			scale := 1.0 / math.Sqrt(bandSum)
			for j := start; j < end; j++ {
				out[j] *= scale
			}
		}
	}
}

func denormalizeSpectrum(normalizedCoeffs, bandEnergy []float64, frameSize int) []float64 {
	n := len(normalizedCoeffs)
	denormalized := make([]float64, n)
	copy(denormalized, normalizedCoeffs)

	starts, ends := ScaledBandBoundaries(n)

	for i := 0; i < NumCELTBands && i < len(bandEnergy); i++ {
		start := starts[i]
		end := ends[i]
		if start >= n || end > n {
			continue
		}

		// Apply band energy (convert from dB to linear)
		gain := math.Pow(10.0, bandEnergy[i]/20.0)
		for j := start; j < end; j++ {
			denormalized[j] *= gain
		}
	}

	return denormalized
}
