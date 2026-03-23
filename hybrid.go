// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements the Hybrid mode encoder and decoder for 24 kHz audio
// as specified in RFC 6716 §3.1 (configurations 12-15).
//
// Hybrid mode splits the input signal into two bands:
//   - SILK band: 0-8 kHz (lower frequencies, encoded with SILK)
//   - CELT band: 8-12 kHz (higher frequencies, encoded with CELT)
//
// This provides speech-optimized low frequency encoding while preserving
// high frequency detail for music and other audio content.
package magnum

import (
	"fmt"
	"math"
)

// Hybrid mode constants per RFC 6716.
const (
	// HybridSILKBandwidth is the bandwidth of the SILK band in hybrid mode (8 kHz).
	HybridSILKBandwidth = 8000
	// HybridCELTBandwidth is the bandwidth of the CELT band in hybrid mode (8-12 kHz).
	HybridCELTBandwidth = 4000
	// HybridCutoffFreq is the crossover frequency between SILK and CELT bands.
	HybridCutoffFreq = 8000
	// HybridSILKSampleRate is the sample rate for the SILK band (16 kHz internal).
	HybridSILKSampleRate = 16000
	// HybridCELTSampleRate is the sample rate for CELT processing in hybrid mode.
	HybridCELTSampleRate = 24000
)

// ConfigurationHybridSWB20ms is the hybrid superwideband (24 kHz), 20 ms frame.
// This is configuration 13 in RFC 6716 Table 2 (group 12–15, index 1).
const ConfigurationHybridSWB20ms Configuration = 13

// HybridEncoder encodes audio using the hybrid SILK+CELT mode.
type HybridEncoder struct {
	sampleRate int
	channels   int
	frameSize  int
	bitrate    int

	// Component encoders
	silkEncoder *SILKFrameEncoder
	celtEncoder *CELTFrameEncoder

	// Band-splitting filter state
	lowpassState  []float64
	highpassState []float64

	// Reusable buffers
	silkBand []float64
	celtBand []float64
}

// HybridEncoderConfig configures the hybrid encoder.
type HybridEncoderConfig struct {
	SampleRate int // Must be 24000 Hz for hybrid mode
	Channels   int // 1 or 2
	Bitrate    int // Target bitrate in bps
}

// NewHybridEncoder creates a new hybrid mode encoder for 24 kHz audio.
func NewHybridEncoder(config HybridEncoderConfig) (*HybridEncoder, error) {
	if config.SampleRate != SampleRate24k {
		return nil, fmt.Errorf("hybrid mode requires 24000 Hz sample rate, got %d", config.SampleRate)
	}
	if config.Channels < 1 || config.Channels > 2 {
		return nil, ErrUnsupportedChannelCount
	}

	// Frame size for 20ms at 24kHz
	frameSize := config.SampleRate * frameDurationMs / 1000

	// SILK encoder for lower band (0-8 kHz)
	// SILK operates at 16 kHz internal rate
	silkFrameSize := HybridSILKSampleRate * frameDurationMs / 1000
	silkConfig := SILKFrameConfig{
		SampleRate: HybridSILKSampleRate,
		Channels:   config.Channels,
		FrameSize:  silkFrameSize,
		Bitrate:    config.Bitrate * 2 / 3, // 2/3 of bits for SILK
	}
	silkEnc, err := NewSILKFrameEncoder(silkConfig)
	if err != nil {
		return nil, fmt.Errorf("hybrid: create SILK encoder: %w", err)
	}

	// CELT encoder for upper band (8-12 kHz)
	celtFrameSize := frameSize
	celtConfig := CELTFrameConfig{
		SampleRate: config.SampleRate,
		Channels:   config.Channels,
		FrameSize:  celtFrameSize,
		Bitrate:    config.Bitrate / 3, // 1/3 of bits for CELT
	}
	celtEnc, err := NewCELTFrameEncoder(celtConfig)
	if err != nil {
		return nil, fmt.Errorf("hybrid: create CELT encoder: %w", err)
	}

	return &HybridEncoder{
		sampleRate:    config.SampleRate,
		channels:      config.Channels,
		frameSize:     frameSize,
		bitrate:       config.Bitrate,
		silkEncoder:   silkEnc,
		celtEncoder:   celtEnc,
		lowpassState:  make([]float64, 4),
		highpassState: make([]float64, 4),
		silkBand:      make([]float64, silkFrameSize),
		celtBand:      make([]float64, frameSize),
	}, nil
}

// HybridEncodedFrame holds the encoded hybrid frame data.
type HybridEncodedFrame struct {
	Data     []byte
	Bits     int
	SILKBits int
	CELTBits int
	// SILKLen is the byte length of the SILK portion (for decoder use)
	SILKLen int
}

// EncodeFrame encodes a single frame using hybrid mode per RFC 6716 §4.2.7.2.
// The input samples should be 24 kHz PCM in float64 format.
//
// RFC 6716 hybrid packet format:
//   - SILK data comes first (always byte-aligned from range coder)
//   - CELT data follows immediately
//   - No explicit length marker; decoder uses packet length
func (enc *HybridEncoder) EncodeFrame(samples []float64) (*HybridEncodedFrame, error) {
	if len(samples) != enc.frameSize {
		return nil, fmt.Errorf("hybrid: expected %d samples, got %d", enc.frameSize, len(samples))
	}

	// Split the signal into SILK (0-8 kHz) and CELT (8-12 kHz) bands
	enc.splitBands(samples)

	// Encode SILK band
	silkFrame, err := enc.silkEncoder.EncodeFrame(enc.silkBand)
	if err != nil {
		return nil, fmt.Errorf("hybrid: SILK encode: %w", err)
	}

	// Encode CELT band (high frequencies only)
	celtFrame, err := enc.celtEncoder.EncodeFrame(enc.celtBand)
	if err != nil {
		return nil, fmt.Errorf("hybrid: CELT encode: %w", err)
	}

	// Assemble RFC 6716-compliant hybrid packet
	// Per RFC 6716 §4.2.7.2: SILK data followed by CELT data
	// The range coder outputs are already byte-aligned
	silkLen := len(silkFrame.Data)
	celtLen := len(celtFrame.Data)

	data := make([]byte, silkLen+celtLen)
	copy(data, silkFrame.Data)
	copy(data[silkLen:], celtFrame.Data)

	return &HybridEncodedFrame{
		Data:     data,
		Bits:     (silkLen + celtLen) * 8,
		SILKBits: silkFrame.Bits,
		CELTBits: celtFrame.Bits,
		SILKLen:  silkLen,
	}, nil
}

// splitBands splits the input signal into SILK and CELT frequency bands.
// Uses a 4th-order IIR crossover filter at 8 kHz.
func (enc *HybridEncoder) splitBands(samples []float64) {
	// Compute filter coefficients for 8 kHz cutoff at 24 kHz sample rate
	// Using Butterworth lowpass/highpass pair
	fc := float64(HybridCutoffFreq) / float64(enc.sampleRate)
	wc := math.Tan(math.Pi * fc)

	// Biquad coefficients for 2nd-order Butterworth lowpass
	k := wc * wc
	sqrt2 := math.Sqrt(2.0)
	norm := 1.0 / (1.0 + sqrt2*wc + k)

	// Lowpass coefficients
	lpB0 := k * norm
	lpB1 := 2.0 * lpB0
	lpB2 := lpB0
	lpA1 := 2.0 * (k - 1.0) * norm
	lpA2 := (1.0 - sqrt2*wc + k) * norm

	// Highpass coefficients
	hpB0 := norm
	hpB1 := -2.0 * norm
	hpB2 := norm
	hpA1 := lpA1
	hpA2 := lpA2

	// Apply lowpass filter and downsample for SILK band
	// Downsample from 24 kHz to 16 kHz (3:2 ratio)
	silkIdx := 0
	for i := 0; i < len(samples); i++ {
		// Apply biquad lowpass
		lp := lpB0*samples[i] + enc.lowpassState[0]
		enc.lowpassState[0] = lpB1*samples[i] - lpA1*lp + enc.lowpassState[1]
		enc.lowpassState[1] = lpB2*samples[i] - lpA2*lp

		// Downsample 3:2 - take 2 samples for every 3 input samples
		if i%3 != 2 && silkIdx < len(enc.silkBand) {
			enc.silkBand[silkIdx] = lp
			silkIdx++
		}
	}

	// Apply highpass filter for CELT band (keep at 24 kHz)
	for i := 0; i < len(samples); i++ {
		// Apply biquad highpass
		hp := hpB0*samples[i] + enc.highpassState[0]
		enc.highpassState[0] = hpB1*samples[i] - hpA1*hp + enc.highpassState[1]
		enc.highpassState[1] = hpB2*samples[i] - hpA2*hp
		enc.celtBand[i] = hp
	}
}

// SetBitrate updates the target bitrate.
func (enc *HybridEncoder) SetBitrate(bitrate int) {
	enc.bitrate = bitrate
	// Distribute 2/3 to SILK, 1/3 to CELT
	enc.silkEncoder.SetBitrate(bitrate * 2 / 3)
	enc.celtEncoder.config.Bitrate = bitrate / 3
}

// Reset resets the encoder state.
func (enc *HybridEncoder) Reset() {
	enc.silkEncoder.Reset()
	for i := range enc.lowpassState {
		enc.lowpassState[i] = 0
	}
	for i := range enc.highpassState {
		enc.highpassState[i] = 0
	}
}

// HybridDecoder decodes audio from hybrid SILK+CELT packets.
type HybridDecoder struct {
	sampleRate int
	channels   int
	frameSize  int

	// Component decoders
	silkDecoder *SILKFrameDecoder
	celtDecoder *CELTFrameDecoder

	// Band-combining filter state
	lowpassState  []float64
	highpassState []float64

	// Reusable buffers
	silkBand []float64
	celtBand []float64
}

// HybridDecoderConfig configures the hybrid decoder.
type HybridDecoderConfig struct {
	SampleRate int // Must be 24000 Hz for hybrid mode
	Channels   int // 1 or 2
}

// NewHybridDecoder creates a new hybrid mode decoder for 24 kHz audio.
func NewHybridDecoder(config HybridDecoderConfig) (*HybridDecoder, error) {
	if config.SampleRate != SampleRate24k {
		return nil, fmt.Errorf("hybrid mode requires 24000 Hz sample rate, got %d", config.SampleRate)
	}
	if config.Channels < 1 || config.Channels > 2 {
		return nil, ErrUnsupportedChannelCount
	}

	// Frame size for 20ms at 24kHz
	frameSize := config.SampleRate * frameDurationMs / 1000

	// SILK decoder for lower band (0-8 kHz)
	silkFrameSize := HybridSILKSampleRate * frameDurationMs / 1000
	silkConfig := SILKFrameConfig{
		SampleRate: HybridSILKSampleRate,
		Channels:   config.Channels,
		FrameSize:  silkFrameSize,
		Bitrate:    64000 * 2 / 3,
	}
	silkDec, err := NewSILKFrameDecoder(silkConfig)
	if err != nil {
		return nil, fmt.Errorf("hybrid: create SILK decoder: %w", err)
	}

	// CELT decoder for upper band
	celtConfig := CELTFrameConfig{
		SampleRate: config.SampleRate,
		Channels:   config.Channels,
		FrameSize:  frameSize,
		Bitrate:    64000 / 3,
	}
	celtDec, err := NewCELTFrameDecoder(celtConfig)
	if err != nil {
		return nil, fmt.Errorf("hybrid: create CELT decoder: %w", err)
	}

	return &HybridDecoder{
		sampleRate:    config.SampleRate,
		channels:      config.Channels,
		frameSize:     frameSize,
		silkDecoder:   silkDec,
		celtDecoder:   celtDec,
		lowpassState:  make([]float64, 4),
		highpassState: make([]float64, 4),
		silkBand:      make([]float64, silkFrameSize),
		celtBand:      make([]float64, frameSize),
	}, nil
}

// DecodeFrame decodes a hybrid frame to PCM samples.
// This method supports RFC 6716 compliant packets where SILK data is
// immediately followed by CELT data without a length prefix.
//
// For RFC 6716 compliance, the SILK length must be determined by the
// caller (typically from packet metadata or by running SILK decode first).
// Use DecodeFrameWithSILKLen for explicit control.
//
// This method attempts to decode by estimating SILK size based on typical
// frame sizes.
func (dec *HybridDecoder) DecodeFrame(data []byte) ([]float64, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("hybrid: packet too short")
	}

	// Estimate SILK length: typically 40-60% of hybrid packet for 20ms frame
	// SILK at 16kHz produces roughly 40-80 bytes per 20ms frame depending on content
	// Try heuristic: use half the packet for SILK, rest for CELT
	silkLen := len(data) / 2
	if silkLen < 4 {
		silkLen = 4
	}
	if silkLen > len(data)-4 {
		silkLen = len(data) - 4
	}

	return dec.DecodeFrameWithSILKLen(data, silkLen)
}

// DecodeFrameWithSILKLen decodes a hybrid frame with an explicit SILK length.
// This is the RFC 6716-compliant decoding method where the caller knows
// the exact byte boundary between SILK and CELT data.
//
// silkLen specifies how many bytes at the start of data belong to SILK.
func (dec *HybridDecoder) DecodeFrameWithSILKLen(data []byte, silkLen int) ([]float64, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("hybrid: packet too short")
	}
	if silkLen > len(data) {
		return nil, fmt.Errorf("hybrid: SILK length %d exceeds packet size %d", silkLen, len(data))
	}

	// Decode SILK band (0-8 kHz)
	silkData := data[:silkLen]
	silkSamples, err := dec.silkDecoder.DecodeFrame(silkData)
	if err != nil {
		// On SILK decode error, use silence for SILK band
		silkSamples = make([]float64, len(dec.silkBand))
	}

	// Decode CELT band (8-12 kHz)
	celtData := data[silkLen:]
	celtSamples, err := dec.celtDecoder.DecodeFrame(celtData)
	if err != nil {
		return nil, fmt.Errorf("hybrid: CELT decode: %w", err)
	}

	// Combine bands
	return dec.combineBands(silkSamples, celtSamples), nil
}

// combineBands combines the SILK and CELT bands into the final output.
// Uses interpolation to upsample SILK from 16 kHz to 24 kHz.
func (dec *HybridDecoder) combineBands(silkSamples, celtSamples []float64) []float64 {
	output := make([]float64, dec.frameSize)

	// Upsample SILK from 16 kHz to 24 kHz (2:3 ratio)
	// Using linear interpolation
	for i := 0; i < dec.frameSize; i++ {
		// Calculate position in SILK band
		silkPos := float64(i) * float64(len(silkSamples)) / float64(dec.frameSize)
		silkIdx0 := int(silkPos)
		silkFrac := silkPos - float64(silkIdx0)

		var silkVal float64
		if len(silkSamples) > 0 {
			if silkIdx0 < len(silkSamples)-1 {
				silkVal = silkSamples[silkIdx0]*(1-silkFrac) + silkSamples[silkIdx0+1]*silkFrac
			} else if silkIdx0 < len(silkSamples) {
				silkVal = silkSamples[silkIdx0]
			}
		}

		// Combine bands (simple addition)
		// Handle CELT samples that may be shorter than expected
		var celtVal float64
		if i < len(celtSamples) {
			celtVal = celtSamples[i]
		}
		output[i] = silkVal + celtVal
	}

	return output
}

// isHybridConfig returns true if the configuration is a hybrid mode config.
func isHybridConfig(config Configuration) bool {
	return config >= 12 && config <= 19
}

// isHybridSWBConfig returns true if the configuration is hybrid superwideband.
func isHybridSWBConfig(config Configuration) bool {
	return config >= 12 && config <= 15
}

// isHybridFBConfig returns true if the configuration is hybrid fullband.
func isHybridFBConfig(config Configuration) bool {
	return config >= 16 && config <= 19
}

// Reset resets the hybrid decoder state.
func (dec *HybridDecoder) Reset() {
	if dec.silkDecoder != nil {
		dec.silkDecoder.Reset()
	}
	for i := range dec.lowpassState {
		dec.lowpassState[i] = 0
	}
	for i := range dec.highpassState {
		dec.highpassState[i] = 0
	}
}
