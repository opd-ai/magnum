// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements the SILK frame assembly as specified in RFC 6716 §4.2.
// A SILK frame consists of:
// 1. VAD flags (for voice activity detection)
// 2. LBRR data (optional, for forward error correction)
// 3. Quantized NLSF parameters (LSF coefficients converted from LPC)
// 4. Pitch lags (for voiced frames)
// 5. LTP coefficients (long-term prediction gains)
// 6. Gains (subframe gain values)
// 7. Excitation (LPC residual encoded via shell coding)
//
// The encoder wires together all the SILK subcomponents (LPC, NLSF, pitch,
// LTP, gain, excitation) into a complete SILK bitstream.

package magnum

import (
	"fmt"
)

// SILK quantization constants.
const (
	// SILKNLSFBits is the number of bits per NLSF coefficient.
	SILKNLSFBits = 6
	// SILKMaxPulsesHint is the default number of pulses for excitation coding.
	SILKMaxPulsesHint = 5
)

// SILKFrameConfig holds configuration for SILK frame encoding.
type SILKFrameConfig struct {
	// SampleRate is the audio sample rate (8000 or 16000 Hz)
	SampleRate int
	// Channels is the number of audio channels (1 or 2)
	Channels int
	// FrameSize is the number of samples per frame (160 at 8kHz, 320 at 16kHz for 20ms)
	FrameSize int
	// Bitrate is the target bitrate in bits per second
	Bitrate int
}

// SILKFrameEncoder encodes SILK frames following RFC 6716 §4.2.
type SILKFrameEncoder struct {
	config SILKFrameConfig

	// Signal analysis components
	lpcAnalyzer   *LPCAnalyzer
	nlsfQuantizer *NLSFQuantizer
	pitchEstimate *PitchEstimator
	ltpAnalyzer   *LTPAnalyzer
	gainCoder     *GainCoder
	excEncoder    *ExcitationEncoder
	vad           *VAD
	lbrrEncoder   *LBRREncoder

	// Previous frame state for prediction
	prevNLSF      []float64
	prevGains     []float64
	prevPitchLags []int

	// LPC order for this configuration
	lpcOrder int

	// Frame counters
	frameCount int
}

// SILKEncodedFrame holds the encoded SILK frame data.
type SILKEncodedFrame struct {
	// Data is the encoded bitstream
	Data []byte
	// Bits is the number of bits used
	Bits int
	// IsVoiced indicates if this is a voiced frame
	IsVoiced bool
	// HasLBRR indicates if LBRR data is included
	HasLBRR bool
}

// NewSILKFrameEncoder creates a new SILK frame encoder.
func NewSILKFrameEncoder(config SILKFrameConfig) (*SILKFrameEncoder, error) {
	// Validate configuration
	if config.SampleRate != 8000 && config.SampleRate != 16000 {
		return nil, fmt.Errorf("SILK: invalid sample rate %d, must be 8000 or 16000", config.SampleRate)
	}
	if config.Channels < 1 || config.Channels > 2 {
		return nil, ErrInvalidChannels
	}

	// Determine LPC order based on bandwidth
	// Narrowband (8kHz): 10th order LPC
	// Wideband (16kHz): 16th order LPC
	lpcOrder := 10
	if config.SampleRate == 16000 {
		lpcOrder = 16
	}

	// Initialize components
	lpcAnalyzer := NewLPCAnalyzer(lpcOrder)
	nlsfQuantizer := NewNLSFQuantizer(lpcOrder)
	pitchEstimate := NewPitchEstimator(config.SampleRate)

	// Compute subframe length
	subframeLength := config.FrameSize / SILKSubFrames

	ltpAnalyzer := NewLTPAnalyzer(config.SampleRate)
	gainCoder := NewGainCoder()
	excEncoder := NewExcitationEncoder(subframeLength)
	vad := NewVAD(config.SampleRate)

	// Initialize LBRR encoder (disabled by default)
	lbrrConfig := DefaultLBRRConfig()
	lbrrEncoder := NewLBRREncoder(config.SampleRate, config.Channels, lbrrConfig)

	return &SILKFrameEncoder{
		config:        config,
		lpcAnalyzer:   lpcAnalyzer,
		nlsfQuantizer: nlsfQuantizer,
		pitchEstimate: pitchEstimate,
		ltpAnalyzer:   ltpAnalyzer,
		gainCoder:     gainCoder,
		excEncoder:    excEncoder,
		vad:           vad,
		lbrrEncoder:   lbrrEncoder,
		prevNLSF:      make([]float64, lpcOrder),
		prevGains:     make([]float64, SILKSubFrames),
		prevPitchLags: make([]int, SILKSubFrames),
		lpcOrder:      lpcOrder,
		frameCount:    0,
	}, nil
}

// EncodeFrame encodes a single SILK frame from PCM samples.
// The samples should be float64 PCM values in the range [-1, 1].
func (enc *SILKFrameEncoder) EncodeFrame(samples []float64) (*SILKEncodedFrame, error) {
	if len(samples) != enc.config.FrameSize {
		return nil, fmt.Errorf("SILK: invalid frame size %d, expected %d",
			len(samples), enc.config.FrameSize)
	}

	enc.frameCount++

	// Initialize range encoder for bitstream
	rc := NewRangeEncoder()

	// Step 1: Voice Activity Detection
	vadResult := enc.vad.Detect(samples)
	isVoiced := vadResult.Active && vadResult.Confidence > 0.3

	// Encode VAD flag (1 bit)
	vadFlag := 0
	if isVoiced {
		vadFlag = 1
	}
	rc.EncodeLogP(vadFlag, 1)

	// Step 2: Check for LBRR and encode if present
	hasLBRR := false
	if enc.lbrrEncoder.IsEnabled() {
		lbrr := enc.lbrrEncoder.EncodeLBRR()
		if lbrr != nil && lbrr.Valid {
			hasLBRR = true
			// Encode LBRR flag (1 bit)
			rc.EncodeLogP(1, 1)
			// Encode LBRR data length (8 bits, max 255 bytes)
			lbrrLen := len(lbrr.Data)
			if lbrrLen > 255 {
				lbrrLen = 255
			}
			rc.EncodeBits(uint32(lbrrLen), 8)
			// Copy LBRR data
			for _, b := range lbrr.Data[:lbrrLen] {
				rc.EncodeBits(uint32(b), 8)
			}
		} else {
			// No LBRR data
			rc.EncodeLogP(0, 1)
		}
	}

	// Step 3: LPC Analysis
	lpcResult := enc.lpcAnalyzer.Analyze(samples)
	if lpcResult == nil {
		return nil, fmt.Errorf("SILK: LPC analysis failed")
	}

	// Step 4: Convert LPC to NLSF and quantize
	nlsfValues := LPCToNLSF(lpcResult.Coefficients)
	quantIndices, quantNLSF := enc.nlsfQuantizer.Quantize(nlsfValues, SILKNLSFBits)

	// Encode quantized NLSF indices
	for _, idx := range quantIndices {
		// Use 6 bits per coefficient (range 0-63)
		val := idx
		if val < 0 {
			val = 0
		}
		if val > 63 {
			val = 63
		}
		rc.EncodeBits(uint32(val), SILKNLSFBits)
	}

	// Step 5: Pitch estimation (for voiced frames)
	var pitchLags []int

	if isVoiced {
		pitchResult := enc.pitchEstimate.Estimate(samples)
		if pitchResult != nil && len(pitchResult.SubframeLags) > 0 {
			pitchLags = pitchResult.SubframeLags
		} else {
			// Fallback if pitch estimation fails
			pitchLags = make([]int, SILKSubFrames)
			midLag := (SILKMinPitchLag + SILKMaxPitchLag) / 2
			for i := range pitchLags {
				pitchLags[i] = midLag
			}
		}

		// Encode pitch lags
		// First lag: absolute (9 bits, range 0-511)
		firstLag := pitchLags[0]
		if firstLag < SILKMinPitchLag {
			firstLag = SILKMinPitchLag
		}
		if firstLag > SILKMaxPitchLag {
			firstLag = SILKMaxPitchLag
		}
		rc.EncodeBits(uint32(firstLag-SILKMinPitchLag), 9)

		// Subsequent lags: delta coded (4 bits signed, range -8 to +7)
		for i := 1; i < len(pitchLags); i++ {
			delta := pitchLags[i] - pitchLags[i-1]
			if delta < -8 {
				delta = -8
			}
			if delta > 7 {
				delta = 7
			}
			rc.EncodeBits(uint32(delta+8), 4) // Offset to positive range
		}

		// Step 6: LTP Analysis
		residual := computeLPCResidual(samples, lpcResult.Coefficients)
		ltpResult := enc.ltpAnalyzer.Analyze(residual)

		// Encode LTP codebook indices
		if ltpResult != nil {
			for _, sf := range ltpResult.Subframes {
				if sf != nil {
					// Encode codebook index (3 bits, 0-7)
					rc.EncodeBits(uint32(sf.CodebookIndex&0x7), 3)
				}
			}
		}
	} else {
		// Unvoiced: use zero pitch lags
		pitchLags = make([]int, SILKSubFrames)
	}

	// Step 7: Subframe gain coding
	residual := computeLPCResidual(samples, lpcResult.Coefficients)
	subframeLen := enc.config.FrameSize / SILKSubFrames

	frameGains := enc.gainCoder.ComputeGains(residual, subframeLen)

	// Encode gains
	for i := 0; i < GainNumSubframes; i++ {
		// Encode gain index (6 bits, range 0-63)
		idx := frameGains.Subframes[i].QuantIndex
		if idx < 0 {
			idx = 0
		}
		if idx > 63 {
			idx = 63
		}
		rc.EncodeBits(uint32(idx), 6)
	}

	// Step 8: Excitation coding (LPC residual)
	excFrame := enc.excEncoder.Encode(residual, SILKMaxPulsesHint)
	enc.encodeExcitation(rc, excFrame)

	// Step 9: Update state for next frame
	copy(enc.prevNLSF, quantNLSF)
	for i := 0; i < GainNumSubframes; i++ {
		enc.prevGains[i] = frameGains.Subframes[i].LogGain
	}
	copy(enc.prevPitchLags, pitchLags)

	// Store frame data for LBRR
	if enc.lbrrEncoder.IsEnabled() {
		quantGains := make([]float64, GainNumSubframes)
		for i := 0; i < GainNumSubframes; i++ {
			quantGains[i] = frameGains.Subframes[i].LinearGain
		}
		lbrrData := &LBRRFrameData{
			LPCCoeffs: lpcResult.Coefficients,
			Gains:     quantGains,
			PitchLags: pitchLags,
			VADFlag:   isVoiced,
			Energy:    vadResult.EnergyDB,
		}
		enc.lbrrEncoder.StorePrimaryFrame(lbrrData)
	}

	// Finalize bitstream
	data := rc.Bytes()

	return &SILKEncodedFrame{
		Data:     data,
		Bits:     len(data) * 8,
		IsVoiced: isVoiced,
		HasLBRR:  hasLBRR,
	}, nil
}

// computeLPCResidual computes the LPC residual (prediction error).
func computeLPCResidual(samples, lpc []float64) []float64 {
	n := len(samples)
	order := len(lpc)
	residual := make([]float64, n)

	for i := 0; i < n; i++ {
		pred := 0.0
		for j := 0; j < order && i-j-1 >= 0; j++ {
			pred += lpc[j] * samples[i-j-1]
		}
		residual[i] = samples[i] - pred
	}

	return residual
}

// encodeExcitation encodes the excitation frame using the range coder.
func (enc *SILKFrameEncoder) encodeExcitation(rc *RangeEncoder, frame *ExcitationFrame) {
	if frame == nil {
		// Encode empty frame
		rc.EncodeBits(0, 4) // 0 subframes
		return
	}

	// Count valid subframes
	numSubframes := 0
	for _, sf := range frame.Subframes {
		if sf != nil {
			numSubframes++
		}
	}
	if numSubframes > 15 {
		numSubframes = 15
	}
	rc.EncodeBits(uint32(numSubframes), 4)

	for i := 0; i < numSubframes; i++ {
		sf := frame.Subframes[i]
		if sf == nil {
			continue
		}

		// Encode pulse count (4 bits, max 15)
		pulseCount := sf.NumPulses
		if pulseCount > 15 {
			pulseCount = 15
		}
		rc.EncodeBits(uint32(pulseCount), 4)

		// Encode each pulse
		for j := 0; j < pulseCount && j < len(sf.Pulses); j++ {
			pulse := sf.Pulses[j]
			// Position (5 bits, range 0-31)
			pos := pulse.Position
			if pos > 31 {
				pos = 31
			}
			rc.EncodeBits(uint32(pos), 5)

			// Sign (1 bit)
			sign := 0
			if pulse.Sign < 0 {
				sign = 1
			}
			rc.EncodeLogP(sign, 1)
		}
	}
}

// EnableLBRR enables Low Bit-Rate Redundancy (FEC) for the SILK encoder.
func (enc *SILKFrameEncoder) EnableLBRR(mode LBRRMode) {
	enc.lbrrEncoder.SetMode(mode)
}

// DisableLBRR disables LBRR encoding.
func (enc *SILKFrameEncoder) DisableLBRR() {
	enc.lbrrEncoder.SetMode(LBRRModeOff)
}

// SetBitrate updates the target bitrate for the SILK encoder.
func (enc *SILKFrameEncoder) SetBitrate(bitrate int) {
	enc.config.Bitrate = bitrate
}

// Reset resets the encoder state.
func (enc *SILKFrameEncoder) Reset() {
	enc.frameCount = 0
	for i := range enc.prevNLSF {
		enc.prevNLSF[i] = 0
	}
	for i := range enc.prevGains {
		enc.prevGains[i] = 0
	}
	for i := range enc.prevPitchLags {
		enc.prevPitchLags[i] = 0
	}
	enc.vad.Reset()
	enc.lbrrEncoder.Reset()
}
