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

	// Pre-allocated buffers to reduce allocations
	residualBuf []float64 // Buffer for LPC residual computation
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
		residualBuf:   make([]float64, config.FrameSize),
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
	rc := NewRangeEncoder()

	// Step 1: Voice Activity Detection
	vadResult := enc.vad.Detect(samples)
	isVoiced := vadResult.Active && vadResult.Confidence > 0.3
	vadFlag := 0
	if isVoiced {
		vadFlag = 1
	}
	rc.EncodeLogP(vadFlag, 1)

	// Step 2: LBRR encoding
	hasLBRR := enc.encodeLBRR(rc)

	// Step 3: LPC Analysis
	lpcResult := enc.lpcAnalyzer.Analyze(samples)
	if lpcResult == nil {
		return nil, fmt.Errorf("SILK: LPC analysis failed")
	}

	// Step 4: Convert LPC to NLSF and quantize
	nlsfValues := LPCToNLSF(lpcResult.Coefficients)
	quantIndices, quantNLSF := enc.nlsfQuantizer.Quantize(nlsfValues, SILKNLSFBits)
	enc.encodeNLSF(rc, quantIndices)

	// Step 5: Pitch estimation and encoding (voiced frames only)
	pitchLags := enc.processPitch(rc, samples, isVoiced, lpcResult.Coefficients)

	// Step 6: Gain coding (use pre-allocated residual buffer)
	computeLPCResidualInto(samples, lpcResult.Coefficients, enc.residualBuf)
	residual := enc.residualBuf[:len(samples)]
	subframeLen := enc.config.FrameSize / SILKSubFrames
	frameGains := enc.gainCoder.ComputeGains(residual, subframeLen)
	enc.encodeGains(rc, frameGains)

	// Step 7: Excitation coding
	excFrame := enc.excEncoder.Encode(residual, SILKMaxPulsesHint)
	enc.encodeExcitation(rc, excFrame)

	// Step 8: Update state and store LBRR data
	enc.updateState(quantNLSF, frameGains, pitchLags)
	enc.storeLBRRFrame(lpcResult.Coefficients, frameGains, pitchLags, isVoiced, vadResult.EnergyDB)

	return &SILKEncodedFrame{
		Data:     rc.Bytes(),
		Bits:     len(rc.Bytes()) * 8,
		IsVoiced: isVoiced,
		HasLBRR:  hasLBRR,
	}, nil
}

// processPitch handles pitch estimation, LTP analysis, and encoding for voiced frames.
func (enc *SILKFrameEncoder) processPitch(rc *RangeEncoder, samples []float64, isVoiced bool, lpcCoeffs []float64) []int {
	if !isVoiced {
		return make([]int, SILKSubFrames)
	}

	// Estimate pitch
	pitchLags := enc.estimatePitchLags(samples)
	enc.encodePitch(rc, pitchLags)

	// LTP Analysis
	residual := computeLPCResidual(samples, lpcCoeffs)
	ltpResult := enc.ltpAnalyzer.Analyze(residual)
	enc.encodeLTP(rc, ltpResult)

	return pitchLags
}

// estimatePitchLags estimates pitch lags for the frame.
func (enc *SILKFrameEncoder) estimatePitchLags(samples []float64) []int {
	pitchResult := enc.pitchEstimate.Estimate(samples)
	if pitchResult != nil && len(pitchResult.SubframeLags) > 0 {
		return pitchResult.SubframeLags
	}
	// Fallback if pitch estimation fails
	pitchLags := make([]int, SILKSubFrames)
	midLag := (SILKMinPitchLag + SILKMaxPitchLag) / 2
	for i := range pitchLags {
		pitchLags[i] = midLag
	}
	return pitchLags
}

// computeLPCResidual computes the LPC residual (prediction error).
func computeLPCResidual(samples, lpc []float64) []float64 {
	n := len(samples)
	residual := make([]float64, n)
	computeLPCResidualInto(samples, lpc, residual)
	return residual
}

// computeLPCResidualInto computes the LPC residual into a pre-allocated buffer.
func computeLPCResidualInto(samples, lpc, residual []float64) {
	n := len(samples)
	order := len(lpc)
	if len(residual) < n {
		return
	}

	for i := 0; i < n; i++ {
		pred := 0.0
		for j := 0; j < order && i-j-1 >= 0; j++ {
			pred += lpc[j] * samples[i-j-1]
		}
		residual[i] = samples[i] - pred
	}
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

// encodeLBRR encodes LBRR (Low Bit-Rate Redundancy) data if enabled.
// Returns true if LBRR data was encoded.
func (enc *SILKFrameEncoder) encodeLBRR(rc *RangeEncoder) bool {
	if !enc.lbrrEncoder.IsEnabled() {
		return false
	}

	lbrr := enc.lbrrEncoder.EncodeLBRR()
	if lbrr == nil || !lbrr.Valid {
		rc.EncodeLogP(0, 1)
		return false
	}

	// Encode LBRR flag
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
	return true
}

// encodeNLSF encodes the quantized NLSF indices to the range coder.
func (enc *SILKFrameEncoder) encodeNLSF(rc *RangeEncoder, quantIndices []int) {
	for _, idx := range quantIndices {
		val := idx
		if val < 0 {
			val = 0
		}
		if val > 63 {
			val = 63
		}
		rc.EncodeBits(uint32(val), SILKNLSFBits)
	}
}

// encodePitch encodes pitch lags for voiced frames.
func (enc *SILKFrameEncoder) encodePitch(rc *RangeEncoder, pitchLags []int) {
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
		rc.EncodeBits(uint32(delta+8), 4)
	}
}

// encodeLTP encodes LTP (Long-Term Prediction) codebook indices.
func (enc *SILKFrameEncoder) encodeLTP(rc *RangeEncoder, ltpResult *LTPFrameResult) {
	if ltpResult == nil {
		return
	}
	for _, sf := range ltpResult.Subframes {
		if sf != nil {
			rc.EncodeBits(uint32(sf.CodebookIndex&0x7), 3)
		}
	}
}

// encodeGains encodes subframe gain indices.
func (enc *SILKFrameEncoder) encodeGains(rc *RangeEncoder, frameGains *FrameGains) {
	for i := 0; i < GainNumSubframes; i++ {
		idx := frameGains.Subframes[i].QuantIndex
		if idx < 0 {
			idx = 0
		}
		if idx > 63 {
			idx = 63
		}
		rc.EncodeBits(uint32(idx), 6)
	}
}

// updateState updates encoder state for the next frame.
func (enc *SILKFrameEncoder) updateState(quantNLSF []float64, frameGains *FrameGains, pitchLags []int) {
	copy(enc.prevNLSF, quantNLSF)
	for i := 0; i < GainNumSubframes; i++ {
		enc.prevGains[i] = frameGains.Subframes[i].LogGain
	}
	copy(enc.prevPitchLags, pitchLags)
}

// storeLBRRFrame stores frame data for LBRR encoding.
func (enc *SILKFrameEncoder) storeLBRRFrame(lpcCoeffs []float64, frameGains *FrameGains, pitchLags []int, isVoiced bool, energyDB float64) {
	if !enc.lbrrEncoder.IsEnabled() {
		return
	}
	quantGains := make([]float64, GainNumSubframes)
	for i := 0; i < GainNumSubframes; i++ {
		quantGains[i] = frameGains.Subframes[i].LinearGain
	}
	lbrrData := &LBRRFrameData{
		LPCCoeffs: lpcCoeffs,
		Gains:     quantGains,
		PitchLags: pitchLags,
		VADFlag:   isVoiced,
		Energy:    energyDB,
	}
	enc.lbrrEncoder.StorePrimaryFrame(lbrrData)
}

// SILKFrameDecoder decodes SILK frames following RFC 6716 §4.2.
type SILKFrameDecoder struct {
	config SILKFrameConfig

	// Signal synthesis components
	nlsfQuantizer *NLSFQuantizer
	gainDecoder   *GainCoder
	excDecoder    *ExcitationDecoder
	plc           *PLCState

	// Previous frame state for prediction
	prevNLSF      []float64
	prevGains     []float64
	prevPitchLags []int
	prevSamples   []float64

	// LPC order for this configuration
	lpcOrder int

	// Frame counters
	frameCount int
	lostFrames int
}

// NewSILKFrameDecoder creates a new SILK frame decoder.
func NewSILKFrameDecoder(config SILKFrameConfig) (*SILKFrameDecoder, error) {
	// Validate configuration
	if config.SampleRate != 8000 && config.SampleRate != 16000 {
		return nil, fmt.Errorf("SILK: invalid sample rate %d, must be 8000 or 16000", config.SampleRate)
	}
	if config.Channels < 1 || config.Channels > 2 {
		return nil, ErrInvalidChannels
	}

	// Determine LPC order based on bandwidth
	lpcOrder := 10
	if config.SampleRate == 16000 {
		lpcOrder = 16
	}

	// Compute subframe length
	subframeLength := config.FrameSize / SILKSubFrames

	return &SILKFrameDecoder{
		config:        config,
		nlsfQuantizer: NewNLSFQuantizer(lpcOrder),
		gainDecoder:   NewGainCoder(),
		excDecoder:    NewExcitationDecoder(subframeLength),
		plc:           NewPLCState(config.SampleRate, config.FrameSize, config.Channels),
		prevNLSF:      make([]float64, lpcOrder),
		prevGains:     make([]float64, SILKSubFrames),
		prevPitchLags: make([]int, SILKSubFrames),
		prevSamples:   make([]float64, config.FrameSize),
		lpcOrder:      lpcOrder,
		frameCount:    0,
		lostFrames:    0,
	}, nil
}

// DecodeFrame decodes a single SILK frame to PCM samples.
// The returned samples are float64 PCM values in the range [-1, 1].
func (dec *SILKFrameDecoder) DecodeFrame(data []byte) ([]float64, error) {
	if len(data) == 0 {
		// Packet loss - use PLC
		return dec.concealLoss()
	}

	dec.frameCount++
	dec.lostFrames = 0

	rc := NewRangeDecoder(data)

	// Step 1: Decode VAD flag
	vadFlag := rc.DecodeLogP(1)
	isVoiced := vadFlag == 1

	// Step 2: Check for LBRR data
	// For simplicity, skip LBRR data if present
	// Full implementation would use it for error correction
	// LBRR decoding is optional for basic playback

	// Step 3: Decode NLSF coefficients
	nlsfIndices := make([]int, dec.lpcOrder)
	for i := 0; i < dec.lpcOrder; i++ {
		nlsfIndices[i] = int(rc.DecodeBits(SILKNLSFBits))
	}

	// Dequantize NLSF
	nlsfValues := dec.nlsfQuantizer.Dequantize(nlsfIndices, SILKNLSFBits)

	// Convert NLSF to LPC coefficients
	lpcCoeffs := NLSFToLPC(nlsfValues)

	// Step 4: Decode pitch lags (for voiced frames)
	pitchLags := make([]int, SILKSubFrames)
	if isVoiced {
		// First lag: absolute
		firstLag := int(rc.DecodeBits(9)) + SILKMinPitchLag
		pitchLags[0] = firstLag

		// Subsequent lags: delta coded
		for i := 1; i < SILKSubFrames; i++ {
			delta := int(rc.DecodeBits(4)) - 8
			pitchLags[i] = pitchLags[i-1] + delta
		}

		// Skip LTP codebook indices (3 bits each)
		for i := 0; i < SILKSubFrames; i++ {
			rc.DecodeBits(3)
		}
	}

	// Step 5: Decode gains
	gains := make([]float64, GainNumSubframes)
	for i := 0; i < GainNumSubframes; i++ {
		gainIdx := int(rc.DecodeBits(6))
		gains[i] = dec.gainDecoder.DequantizeGain(gainIdx)
	}

	// Step 6: Decode excitation
	excFrame := dec.decodeExcitation(rc)

	// Step 7: Synthesize output using LPC filter
	samples := dec.synthesize(lpcCoeffs, gains, excFrame, pitchLags, isVoiced)

	// Update state for next frame
	copy(dec.prevNLSF, nlsfValues)
	copy(dec.prevGains, gains)
	copy(dec.prevPitchLags, pitchLags)
	copy(dec.prevSamples, samples)

	return samples, nil
}

// decodeExcitation decodes the excitation signal from the bitstream.
func (dec *SILKFrameDecoder) decodeExcitation(rc *RangeDecoder) *ExcitationFrame {
	frame := &ExcitationFrame{}

	numSubframes := int(rc.DecodeBits(4))
	if numSubframes > SILKSubFrames {
		numSubframes = SILKSubFrames
	}

	for i := 0; i < numSubframes; i++ {
		sf := &ExcitationSubframe{}

		pulseCount := int(rc.DecodeBits(4))
		sf.NumPulses = pulseCount
		sf.Pulses = make([]ExcitationPulse, pulseCount)

		for j := 0; j < pulseCount; j++ {
			pos := int(rc.DecodeBits(5))
			signBit := rc.DecodeLogP(1)
			sign := 1
			if signBit == 1 {
				sign = -1
			}
			sf.Pulses[j] = ExcitationPulse{Position: pos, Sign: sign, Amplitude: 1.0}
		}

		frame.Subframes[i] = sf
	}

	return frame
}

// synthesize generates PCM samples from decoded parameters.
func (dec *SILKFrameDecoder) synthesize(lpc, gains []float64, exc *ExcitationFrame,
	pitchLags []int, isVoiced bool,
) []float64 {
	samples := make([]float64, dec.config.FrameSize)
	subframeLen := dec.config.FrameSize / SILKSubFrames

	for sf := 0; sf < SILKSubFrames; sf++ {
		startIdx := sf * subframeLen
		gain := gains[sf]

		// Generate excitation signal for this subframe
		excSamples := make([]float64, subframeLen)
		if exc != nil && sf < len(exc.Subframes) && exc.Subframes[sf] != nil {
			excSf := exc.Subframes[sf]
			for _, p := range excSf.Pulses {
				if p.Position < subframeLen {
					excSamples[p.Position] = float64(p.Sign) * gain
				}
			}
		}

		// Apply LTP for voiced frames
		if isVoiced && sf < len(pitchLags) {
			lag := pitchLags[sf]
			if lag > 0 {
				for i := 0; i < subframeLen; i++ {
					srcIdx := startIdx + i - lag
					if srcIdx >= 0 && srcIdx < len(dec.prevSamples) {
						excSamples[i] += 0.5 * dec.prevSamples[srcIdx]
					}
				}
			}
		}

		// LPC synthesis filter
		for i := 0; i < subframeLen; i++ {
			outIdx := startIdx + i
			samples[outIdx] = excSamples[i]

			// Add LPC prediction
			for j := 0; j < len(lpc) && j < outIdx; j++ {
				samples[outIdx] += lpc[j] * samples[outIdx-j-1]
			}
		}
	}

	return samples
}

// concealLoss generates a replacement frame when a packet is lost.
func (dec *SILKFrameDecoder) concealLoss() ([]float64, error) {
	dec.lostFrames++

	// Use PLC to generate concealment
	if dec.plc != nil {
		return dec.plc.PacketLost(), nil
	}

	// Fallback: fade out previous samples
	samples := make([]float64, dec.config.FrameSize)
	attenuation := 0.9
	if dec.lostFrames > 1 {
		attenuation = 0.7
	}
	if dec.lostFrames > 3 {
		attenuation = 0.3
	}
	for i := range samples {
		if i < len(dec.prevSamples) {
			samples[i] = dec.prevSamples[i] * attenuation
		}
	}
	return samples, nil
}

// Reset resets the decoder state.
func (dec *SILKFrameDecoder) Reset() {
	dec.frameCount = 0
	dec.lostFrames = 0
	for i := range dec.prevNLSF {
		dec.prevNLSF[i] = 0
	}
	for i := range dec.prevGains {
		dec.prevGains[i] = 0
	}
	for i := range dec.prevPitchLags {
		dec.prevPitchLags[i] = 0
	}
	for i := range dec.prevSamples {
		dec.prevSamples[i] = 0
	}
	if dec.plc != nil {
		dec.plc.Reset()
	}
}
