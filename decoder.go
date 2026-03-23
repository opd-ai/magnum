package magnum

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
)

// maxDecompressedBytes is the maximum number of bytes that Decode will
// decompress from a single packet. Packets whose decompressed payload exceeds
// this limit are rejected with ErrPayloadTooLarge to prevent memory
// exhaustion from malformed or malicious inputs (zip-bomb mitigation).
//
// 65536 bytes comfortably covers the largest possible legitimate frame:
// 48 kHz × 60 ms × 2 channels × 2 bytes/sample = 11 520 bytes.
const maxDecompressedBytes = 65536

// Decoder is a simplified pure-Go Opus-compatible audio decoder.
//
// It follows the pion/opus API patterns and decodes packets produced by
// [Encoder]. The Decoder type provides API symmetry with the Encoder; for
// simple use cases, the standalone [Decode] and [DecodeWithInfo] functions
// may be more convenient.
//
// By default, the decoder uses flate decompression for backward compatibility.
// To decode CELT-encoded packets, call [Decoder.EnableCELT].
type Decoder struct {
	sampleRate int
	channels   int
	// useCELT controls whether to use the CELT codec for decoding.
	// When false (default), uses flate decompression.
	useCELT bool
	// rawBuffer is a reusable buffer for decompressed PCM bytes.
	// It reduces allocations when decoding multiple packets.
	rawBuffer []byte
	// readChunk is a reusable buffer for reading decompressed data in chunks.
	readChunk []byte
	// flateR is a reusable flate decompressor.
	flateR io.ReadCloser
	// celtDecoder for 24 kHz and 48 kHz sample rates when useCELT is true.
	celtDecoder *CELTFrameDecoder
	// hybridDecoder for 24 kHz hybrid mode (configurations 12-19).
	hybridDecoder *HybridDecoder
	// useHybrid controls whether to use the hybrid codec for decoding.
	useHybrid bool
	// plcState holds state for packet loss concealment when PLC is enabled.
	plcState *PLCState
	// usePLC controls whether PLC is enabled for this decoder.
	usePLC bool
}

// NewDecoder creates a new Decoder for the given sample rate and channel count.
//
// Supported sample rates: 8000, 16000, 24000, 48000 Hz.
// Supported channel counts: 1 (mono) or 2 (stereo).
//
// By default, the decoder uses flate decompression for backward compatibility.
// To decode CELT-encoded packets for 24/48 kHz, call [Decoder.EnableCELT].
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	if !isValidSampleRate(sampleRate) {
		return nil, ErrUnsupportedSampleRate
	}
	if channels < 1 || channels > 2 {
		return nil, ErrUnsupportedChannelCount
	}
	// Pre-allocate rawBuffer for typical frame sizes.
	// 48 kHz stereo 20 ms = 1920 samples × 2 bytes = 3840 bytes.
	initialCap := sampleRate * 20 / 1000 * channels * 2
	// Initialize flate reader with empty input; it will be reset on each decode.
	flateR := flate.NewReader(bytes.NewReader(nil))

	return &Decoder{
		sampleRate: sampleRate,
		channels:   channels,
		useCELT:    false, // default: use flate for backward compatibility
		rawBuffer:  make([]byte, 0, initialCap),
		readChunk:  make([]byte, 4096),
		flateR:     flateR,
	}, nil
}

// EnableCELT enables RFC 6716–style CELT decoding for 24 kHz and 48 kHz
// sample rates. Returns an error if the decoder's sample rate is not
// suitable for CELT (must be 24000 or 48000 Hz).
//
// When CELT is enabled, packets are decoded using the CELT codec as specified
// in RFC 6716 §4.3. Use this with packets from a CELT-enabled [Encoder].
func (d *Decoder) EnableCELT() error {
	if d.sampleRate != 24000 && d.sampleRate != 48000 {
		return fmt.Errorf("magnum: CELT requires 24000 or 48000 Hz sample rate, got %d", d.sampleRate)
	}

	if d.celtDecoder == nil {
		frameSize := d.sampleRate * 20 / 1000 // Samples per channel for 20 ms
		celtConfig := CELTFrameConfig{
			SampleRate: d.sampleRate,
			Channels:   d.channels,
			FrameSize:  frameSize,
			Bitrate:    64000, // Default bitrate
		}
		celtDec, err := NewCELTFrameDecoder(celtConfig)
		if err != nil {
			return fmt.Errorf("magnum: enable CELT: %w", err)
		}
		d.celtDecoder = celtDec
	}

	d.useCELT = true
	return nil
}

// IsCELTEnabled returns true if CELT decoding is enabled for this decoder.
func (d *Decoder) IsCELTEnabled() bool {
	return d.useCELT && d.celtDecoder != nil
}

// EnableHybrid enables RFC 6716–style hybrid decoding for 24 kHz sample rate.
// Returns an error if the decoder's sample rate is not 24000 Hz.
//
// When hybrid is enabled, packets with TOC configurations 12-19 (hybrid SWB
// and hybrid FB modes) are decoded using the hybrid SILK+CELT codec as
// specified in RFC 6716 §3.1. This enables decoding of hybrid mode packets
// produced by standard Opus encoders.
//
// This method is part of ROADMAP Milestone 6 (hybrid decode path).
func (d *Decoder) EnableHybrid() error {
	if d.sampleRate != SampleRate24k {
		return fmt.Errorf("magnum: hybrid mode requires 24000 Hz sample rate, got %d", d.sampleRate)
	}

	if d.hybridDecoder == nil {
		hybridConfig := HybridDecoderConfig{
			SampleRate: d.sampleRate,
			Channels:   d.channels,
		}
		hybridDec, err := NewHybridDecoder(hybridConfig)
		if err != nil {
			return fmt.Errorf("magnum: enable hybrid: %w", err)
		}
		d.hybridDecoder = hybridDec
	}

	d.useHybrid = true
	return nil
}

// IsHybridEnabled returns true if hybrid decoding is enabled for this decoder.
func (d *Decoder) IsHybridEnabled() bool {
	return d.useHybrid && d.hybridDecoder != nil
}

// EnablePLC enables Packet Loss Concealment (PLC) for this decoder.
// When PLC is enabled, the decoder tracks frame data and can synthesize
// audio to fill gaps when packets are lost.
//
// PLC implements RFC 6716 §4.4 concealment using LPC-based extrapolation,
// pitch-periodic repetition for voiced frames, and gradual attenuation
// during extended loss periods.
//
// Use [Decoder.DecodePLC] to synthesize concealment audio when a packet
// is detected as lost.
func (d *Decoder) EnablePLC() {
	if d.plcState == nil {
		frameSize := d.sampleRate * 20 / 1000 // 20ms frame
		d.plcState = NewPLCState(d.sampleRate, frameSize, d.channels)
	}
	d.usePLC = true
}

// IsPLCEnabled returns true if PLC is enabled for this decoder.
func (d *Decoder) IsPLCEnabled() bool {
	return d.usePLC && d.plcState != nil
}

// DecodePLC synthesizes concealment audio for a lost packet.
// This should be called when a packet is detected as lost (e.g., via sequence
// number gap or timeout). Returns the number of samples synthesized.
//
// PLC is only effective if it has been enabled via [Decoder.EnablePLC] and
// at least one successful decode has occurred to provide frame data for
// concealment. If PLC is disabled or no previous frame data is available,
// the output is filled with silence.
//
// The out slice should be sized for one frame (e.g., 960 samples for 20ms
// at 48 kHz mono, 1920 for stereo).
func (d *Decoder) DecodePLC(out []int16) (int, error) {
	frameSize := d.sampleRate * 20 / 1000 * d.channels
	if out == nil || len(out) < frameSize {
		return 0, fmt.Errorf("magnum: DecodePLC: output buffer too small (need %d, got %d)", frameSize, len(out))
	}

	if !d.usePLC || d.plcState == nil {
		// PLC not enabled, output silence
		for i := 0; i < frameSize; i++ {
			out[i] = 0
		}
		return frameSize, nil
	}

	// Generate concealment audio
	concealed := d.plcState.PacketLost()

	// Convert float64 to int16 and copy to output
	for i := 0; i < len(concealed) && i < frameSize; i++ {
		sample := concealed[i] * 32767.0
		if sample > 32767 {
			sample = 32767
		} else if sample < -32768 {
			sample = -32768
		}
		out[i] = int16(sample)
	}

	return frameSize, nil
}

// Decode decodes an Opus packet into the provided output buffer.
//
// The out slice must be large enough to hold the decoded samples. For a 20 ms
// frame at 48 kHz mono, this is 960 samples; for stereo, 1920 samples.
//
// Returns the number of samples decoded. If out is provided and large enough,
// samples are copied into out and no additional allocation occurs for the sample
// data. If out is nil or too small, the decoded samples are still available but
// callers should use [DecodeAlloc] for that use case.
//
// Performance note: To avoid per-packet allocations, reuse the out slice across
// calls. The internal decompression still allocates, but the sample slice copy
// is avoided when out is sufficiently sized.
//
// The Decoder validates that the packet's channel configuration and sample rate
// match the decoder's settings. Returns [ErrChannelMismatch] if the packet's
// stereo flag doesn't match d.channels, and [ErrSampleRateMismatch] if the
// packet's TOC configuration indicates a different sample rate.
//
// This method follows the pion/opus Decoder.Decode signature pattern.
func (d *Decoder) Decode(packet []byte, out []int16) (int, error) {
	var n int
	var err error

	// Parse TOC to determine codec path
	if len(packet) >= 1 {
		toc := tocHeader(packet[0])
		config := toc.configuration()

		// Check for hybrid configurations (12-19)
		if isHybridConfig(config) && d.useHybrid && d.hybridDecoder != nil {
			n, err = d.decodeHybrid(packet, out)
			if err == nil && d.usePLC && d.plcState != nil && n > 0 {
				d.updatePLCState(out, n)
			}
			return n, err
		}
	}

	// Use CELT when enabled and available
	if d.useCELT && d.celtDecoder != nil {
		n, err = d.decodeCELT(packet, out)
	} else {
		// Fallback to flate for 8 kHz and 16 kHz
		n, err = d.decodeFlate(packet, out)
	}

	// Update PLC state on successful decode
	if err == nil && d.usePLC && d.plcState != nil && n > 0 {
		d.updatePLCState(out, n)
	}

	return n, err
}

// updatePLCState stores frame data from a successfully decoded frame for PLC.
func (d *Decoder) updatePLCState(samples []int16, count int) {
	if d.plcState == nil {
		return
	}

	// Convert int16 samples to float64 for PLC storage
	floatSamples := make([]float64, count)
	for i := 0; i < count; i++ {
		floatSamples[i] = float64(samples[i]) / 32768.0
	}

	// Create frame data for PLC
	frameData := &PLCFrameData{
		Samples:   floatSamples,
		Voiced:    false, // Simplified: we could analyze for voicing
		Gain:      1.0,
		PitchLag:  0,
		PitchGain: 0,
	}

	d.plcState.PacketReceived(frameData)
}

// validateTOCHeader validates the TOC header and returns stereo flag and config.
func (d *Decoder) validateTOCHeader(packet []byte) (toc tocHeader, err error) {
	if len(packet) < 2 {
		return 0, ErrTooShortForTableOfContentsHeader
	}

	toc = tocHeader(packet[0])
	if toc.frameCode() != frameCodeOneFrame {
		return 0, ErrUnsupportedFrameCode
	}

	stereo := toc.isStereo()
	packetChannels := 1
	if stereo {
		packetChannels = 2
	}
	if packetChannels != d.channels {
		return 0, fmt.Errorf("magnum: decode: %w", ErrChannelMismatch)
	}

	config := toc.configuration()
	packetSampleRate := sampleRateForConfig(config)
	if packetSampleRate != d.sampleRate {
		return 0, fmt.Errorf("magnum: decode: %w", ErrSampleRateMismatch)
	}

	return toc, nil
}

// convertFloatSamplesToInt16 converts float64 samples to int16, handling mono/stereo.
func (d *Decoder) convertFloatSamplesToInt16(floatSamples []float64, out []int16) int {
	numSamples := len(floatSamples)
	if d.channels == 2 {
		numSamples *= 2
	}

	if out == nil || len(out) < numSamples {
		return numSamples
	}

	if d.channels == 1 {
		for i, s := range floatSamples {
			out[i] = clampToInt16(s * 32767.0)
		}
	} else {
		for i, s := range floatSamples {
			sample := clampToInt16(s * 32767.0)
			out[i*2] = sample
			out[i*2+1] = sample
		}
	}
	return numSamples
}

// clampToInt16 clamps a float64 value to int16 range.
func clampToInt16(sample float64) int16 {
	if sample > 32767 {
		sample = 32767
	} else if sample < -32768 {
		sample = -32768
	}
	return int16(sample)
}

// decodeCELT decodes a CELT-encoded packet.
func (d *Decoder) decodeCELT(packet []byte, out []int16) (int, error) {
	_, err := d.validateTOCHeader(packet)
	if err != nil {
		return 0, err
	}

	floatSamples, err := d.celtDecoder.DecodeFrame(packet[1:])
	if err != nil {
		return 0, fmt.Errorf("magnum: decode: CELT: %w", err)
	}

	return d.convertFloatSamplesToInt16(floatSamples, out), nil
}

// decodeHybrid decodes a hybrid SILK+CELT encoded packet.
func (d *Decoder) decodeHybrid(packet []byte, out []int16) (int, error) {
	_, err := d.validateTOCHeader(packet)
	if err != nil {
		return 0, err
	}

	floatSamples, err := d.hybridDecoder.DecodeFrame(packet[1:])
	if err != nil {
		return 0, fmt.Errorf("magnum: decode: hybrid: %w", err)
	}

	return d.convertFloatSamplesToInt16(floatSamples, out), nil
}

// decodeFlate decodes a flate-compressed packet (fallback for SILK paths).
func (d *Decoder) decodeFlate(packet []byte, out []int16) (int, error) {
	// Reuse the decoder's internal buffers and flate reader for decompression.
	raw, stereo, config, err := decodePayloadWithReader(packet, d.rawBuffer, d.readChunk, d.flateR)
	if err != nil {
		return 0, fmt.Errorf("magnum: decode: %w", err)
	}
	// Update rawBuffer to retain capacity for next decode.
	d.rawBuffer = raw[:0]

	// Validate channel configuration.
	packetChannels := 1
	if stereo {
		packetChannels = 2
	}
	if packetChannels != d.channels {
		return 0, fmt.Errorf("magnum: decode: %w", ErrChannelMismatch)
	}

	// Validate sample rate configuration.
	packetSampleRate := sampleRateForConfig(config)
	if packetSampleRate != d.sampleRate {
		return 0, fmt.Errorf("magnum: decode: %w", ErrSampleRateMismatch)
	}

	// Convert raw bytes to int16 samples.
	numSamples := len(raw) / 2

	// If out is provided and large enough, decode directly into it.
	if out != nil && len(out) >= numSamples {
		for i := 0; i < numSamples; i++ {
			out[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
		}
		return numSamples, nil
	}

	// out is nil or undersized; decode what we can (if anything).
	if out != nil {
		for i := 0; i < len(out) && i < numSamples; i++ {
			out[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
		}
	}
	return numSamples, nil
}

// DecodeAlloc decodes an Opus packet and returns the decoded samples directly.
//
// Unlike [Decoder.Decode], this method allocates and returns the sample slice,
// making it suitable for callers who do not want to pre-allocate an output buffer.
// For performance-critical code where the output buffer can be reused, prefer
// [Decoder.Decode] with a pre-allocated out slice.
//
// Like [Decoder.Decode], this method validates the packet's channel and sample
// rate configuration against the decoder's settings.
func (d *Decoder) DecodeAlloc(packet []byte) ([]int16, error) {
	// Parse TOC to determine codec path
	if len(packet) >= 1 {
		toc := tocHeader(packet[0])
		config := toc.configuration()

		// Check for hybrid configurations (12-19)
		if isHybridConfig(config) && d.useHybrid && d.hybridDecoder != nil {
			return d.decodeAllocHybrid(packet)
		}
	}

	// Use CELT when enabled and available
	if d.useCELT && d.celtDecoder != nil {
		return d.decodeAllocCELT(packet)
	}

	// Default: flate decompression (backward compatible)
	return d.decodeAllocFlate(packet)
}

// decodeAllocCELT decodes a CELT-encoded packet and allocates the result.
func (d *Decoder) decodeAllocCELT(packet []byte) ([]int16, error) {
	if len(packet) < 2 {
		return nil, ErrTooShortForTableOfContentsHeader
	}

	// Parse TOC header
	toc := tocHeader(packet[0])
	if toc.frameCode() != frameCodeOneFrame {
		return nil, ErrUnsupportedFrameCode
	}

	// Validate channel configuration
	stereo := toc.isStereo()
	packetChannels := 1
	if stereo {
		packetChannels = 2
	}
	if packetChannels != d.channels {
		return nil, fmt.Errorf("magnum: decode: %w", ErrChannelMismatch)
	}

	// Validate sample rate configuration
	config := toc.configuration()
	packetSampleRate := sampleRateForConfig(config)
	if packetSampleRate != d.sampleRate {
		return nil, fmt.Errorf("magnum: decode: %w", ErrSampleRateMismatch)
	}

	// Decode CELT payload
	celtPayload := packet[1:]
	floatSamples, err := d.celtDecoder.DecodeFrame(celtPayload)
	if err != nil {
		return nil, fmt.Errorf("magnum: decode: CELT: %w", err)
	}

	// Convert float64 samples to int16
	var samples []int16
	if d.channels == 1 {
		samples = make([]int16, len(floatSamples))
		for i, s := range floatSamples {
			sample := s * 32767.0
			if sample > 32767 {
				sample = 32767
			} else if sample < -32768 {
				sample = -32768
			}
			samples[i] = int16(sample)
		}
	} else {
		// Stereo: duplicate mono to both channels (simplification)
		samples = make([]int16, len(floatSamples)*2)
		for i, s := range floatSamples {
			sample := s * 32767.0
			if sample > 32767 {
				sample = 32767
			} else if sample < -32768 {
				sample = -32768
			}
			samples[i*2] = int16(sample)
			samples[i*2+1] = int16(sample)
		}
	}

	return samples, nil
}

// decodeAllocHybrid decodes a hybrid SILK+CELT encoded packet and allocates the result.
func (d *Decoder) decodeAllocHybrid(packet []byte) ([]int16, error) {
	if len(packet) < 2 {
		return nil, ErrTooShortForTableOfContentsHeader
	}

	// Parse TOC header
	toc := tocHeader(packet[0])
	if toc.frameCode() != frameCodeOneFrame {
		return nil, ErrUnsupportedFrameCode
	}

	// Validate channel configuration
	stereo := toc.isStereo()
	packetChannels := 1
	if stereo {
		packetChannels = 2
	}
	if packetChannels != d.channels {
		return nil, fmt.Errorf("magnum: decode: %w", ErrChannelMismatch)
	}

	// Validate sample rate configuration
	config := toc.configuration()
	packetSampleRate := sampleRateForConfig(config)
	if packetSampleRate != d.sampleRate {
		return nil, fmt.Errorf("magnum: decode: %w", ErrSampleRateMismatch)
	}

	// Decode hybrid payload
	hybridPayload := packet[1:]
	floatSamples, err := d.hybridDecoder.DecodeFrame(hybridPayload)
	if err != nil {
		return nil, fmt.Errorf("magnum: decode: hybrid: %w", err)
	}

	// Convert float64 samples to int16
	var samples []int16
	if d.channels == 1 {
		samples = make([]int16, len(floatSamples))
		for i, s := range floatSamples {
			sample := s * 32767.0
			if sample > 32767 {
				sample = 32767
			} else if sample < -32768 {
				sample = -32768
			}
			samples[i] = int16(sample)
		}
	} else {
		// Stereo: duplicate mono to both channels (simplification)
		samples = make([]int16, len(floatSamples)*2)
		for i, s := range floatSamples {
			sample := s * 32767.0
			if sample > 32767 {
				sample = 32767
			} else if sample < -32768 {
				sample = -32768
			}
			samples[i*2] = int16(sample)
			samples[i*2+1] = int16(sample)
		}
	}

	return samples, nil
}

// decodeAllocFlate decodes a flate-compressed packet and allocates the result.
func (d *Decoder) decodeAllocFlate(packet []byte) ([]int16, error) {
	// Reuse the decoder's internal buffers and flate reader for decompression.
	raw, stereo, config, err := decodePayloadWithReader(packet, d.rawBuffer, d.readChunk, d.flateR)
	if err != nil {
		return nil, fmt.Errorf("magnum: decode: %w", err)
	}
	// Update rawBuffer to retain capacity for next decode.
	d.rawBuffer = raw[:0]

	// Validate channel configuration.
	packetChannels := 1
	if stereo {
		packetChannels = 2
	}
	if packetChannels != d.channels {
		return nil, fmt.Errorf("magnum: decode: %w", ErrChannelMismatch)
	}

	// Validate sample rate configuration.
	packetSampleRate := sampleRateForConfig(config)
	if packetSampleRate != d.sampleRate {
		return nil, fmt.Errorf("magnum: decode: %w", ErrSampleRateMismatch)
	}

	// Convert raw bytes to int16 samples.
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return samples, nil
}

// SampleRate returns the sample rate configured for this decoder.
func (d *Decoder) SampleRate() int {
	return d.sampleRate
}

// Channels returns the channel count configured for this decoder.
func (d *Decoder) Channels() int {
	return d.channels
}

// Decode decodes an Opus packet that was produced by [Encoder.Encode].
//
// This is the inverse of Encode and is provided for round-trip testing.
// It does not decode packets produced by standard Opus encoders.
//
// Returns [ErrTooShortForTableOfContentsHeader] if the packet is completely
// empty, [io.ErrUnexpectedEOF] if only the TOC byte is present without a
// payload, [ErrUnsupportedFrameCode] if the packet uses multi-frame encoding,
// and [ErrPayloadTooLarge] if the decompressed payload exceeds
// maxDecompressedBytes.
func Decode(packet []byte) ([]int16, error) {
	samples, _, _, err := decodeInternal(packet)
	return samples, err
}

// DecodeWithInfo decodes an Opus packet and returns the stereo flag from the
// TOC header along with the decoded samples.
//
// This variant of [Decode] enables callers to verify the channel configuration
// of the packet at decode time. The stereo parameter is true if the packet was
// encoded with 2 channels (stereo), false for 1 channel (mono).
//
// See [Decode] for error conditions.
func DecodeWithInfo(packet []byte) (samples []int16, stereo bool, err error) {
	samples, stereo, _, err = decodeInternal(packet)
	return samples, stereo, err
}

// decodeInternal is the shared decode implementation used by all decode functions.
// It returns the decoded samples, stereo flag, and configuration for validation.
func decodeInternal(packet []byte) (samples []int16, stereo bool, config Configuration, err error) {
	raw, stereo, config, err := decodePayload(packet, nil, nil)
	if err != nil {
		return nil, false, 0, err
	}

	samples = make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return samples, stereo, config, nil
}

// decodePayload decompresses the packet payload into raw PCM bytes.
// If buf is provided and has sufficient capacity, it is reused to reduce allocations.
// If chunk is provided, it is used as the read buffer; otherwise a temporary one is allocated.
// Returns the raw bytes slice (possibly a resliced buf), stereo flag, and config.
func decodePayload(packet, buf, chunk []byte) (raw []byte, stereo bool, config Configuration, err error) {
	return decodePayloadWithReader(packet, buf, chunk, nil)
}

// decodePayloadWithReader is like decodePayload but accepts a reusable flate reader.
// If flateR is provided, it is reset and reused instead of creating a new one.
func decodePayloadWithReader(packet, buf, chunk []byte, flateR io.ReadCloser) (raw []byte, stereo bool, config Configuration, err error) {
	if len(packet) < 1 {
		return nil, false, 0, ErrTooShortForTableOfContentsHeader
	}
	if len(packet) == 1 {
		return nil, false, 0, io.ErrUnexpectedEOF
	}

	toc := tocHeader(packet[0])
	fc := toc.frameCode()
	stereo = toc.isStereo()
	config = toc.configuration()

	switch fc {
	case frameCodeOneFrame:
		return decodeFlatePayload(packet[1:], buf, chunk, flateR, stereo, config)
	case frameCodeTwoEqualFrames:
		return decodeTwoEqualFrames(packet, buf, chunk, flateR, stereo, config)
	case frameCodeTwoDifferentFrames:
		return decodeTwoDifferentFrames(packet, buf, chunk, flateR, stereo, config)
	case frameCodeArbitraryFrames:
		return decodeArbitraryFrames(packet, buf, chunk, stereo, config)
	default:
		return nil, false, 0, ErrUnsupportedFrameCode
	}
}

// decodeTwoEqualFrames handles frame code 1: two equal-size frames.
func decodeTwoEqualFrames(packet, buf, chunk []byte, flateR io.ReadCloser, stereo bool, config Configuration) ([]byte, bool, Configuration, error) {
	payloadLen := len(packet) - 1
	if payloadLen%2 != 0 {
		return nil, false, 0, ErrInvalidFrameData
	}
	frameLen := payloadLen / 2
	frame1 := packet[1 : 1+frameLen]
	frame2 := packet[1+frameLen:]
	return decodeTwoFrames(frame1, frame2, buf, chunk, flateR, stereo, config)
}

// decodeTwoDifferentFrames handles frame code 2: two different-size frames.
func decodeTwoDifferentFrames(packet, buf, chunk []byte, flateR io.ReadCloser, stereo bool, config Configuration) ([]byte, bool, Configuration, error) {
	if len(packet) < 2 {
		return nil, false, 0, io.ErrUnexpectedEOF
	}
	frame1Len, consumed := decodeFrameLength(packet[1:])
	if consumed == 0 || 1+consumed+frame1Len > len(packet) {
		return nil, false, 0, ErrInvalidFrameData
	}
	frame1Start := 1 + consumed
	frame1 := packet[frame1Start : frame1Start+frame1Len]
	frame2 := packet[frame1Start+frame1Len:]
	return decodeTwoFrames(frame1, frame2, buf, chunk, flateR, stereo, config)
}

// decodeArbitraryFrames handles frame code 3: VBR or CBR multi-frame packets.
func decodeArbitraryFrames(packet, buf, chunk []byte, stereo bool, config Configuration) ([]byte, bool, Configuration, error) {
	if len(packet) < 2 {
		return nil, false, 0, io.ErrUnexpectedEOF
	}
	mByte := packet[1]
	frameCount := int(mByte >> 2)
	isVBR := (mByte & 0x01) != 0

	if frameCount == 0 || frameCount > 48 {
		return nil, false, 0, ErrInvalidFrameData
	}

	if !isVBR {
		return decodeCBRFrames(packet[2:], frameCount, buf, chunk, stereo, config)
	}
	return decodeVBRFrames(packet, frameCount, buf, chunk, stereo, config)
}

// decodeCBRFrames decodes CBR multi-frame packets (all frames same size).
func decodeCBRFrames(payload []byte, frameCount int, buf, chunk []byte, stereo bool, config Configuration) ([]byte, bool, Configuration, error) {
	if len(payload)%frameCount != 0 {
		return nil, false, 0, ErrInvalidFrameData
	}
	frameLen := len(payload) / frameCount
	frames := make([][]byte, frameCount)
	for i := 0; i < frameCount; i++ {
		frames[i] = payload[i*frameLen : (i+1)*frameLen]
	}
	return decodeMultipleFrames(frames, buf, chunk, stereo, config)
}

// decodeVBRFrames decodes VBR multi-frame packets (variable frame sizes).
func decodeVBRFrames(packet []byte, frameCount int, buf, chunk []byte, stereo bool, config Configuration) ([]byte, bool, Configuration, error) {
	offset := 2
	frames := make([][]byte, frameCount)
	for i := 0; i < frameCount-1; i++ {
		if offset >= len(packet) {
			return nil, false, 0, ErrInvalidFrameData
		}
		frameLen, consumed := decodeFrameLength(packet[offset:])
		if consumed == 0 {
			return nil, false, 0, ErrInvalidFrameData
		}
		offset += consumed
		if offset+frameLen > len(packet) {
			return nil, false, 0, ErrInvalidFrameData
		}
		frames[i] = packet[offset : offset+frameLen]
		offset += frameLen
	}
	frames[frameCount-1] = packet[offset:]
	return decodeMultipleFrames(frames, buf, chunk, stereo, config)
}

// decodeFlatePayload decompresses a single flate-compressed frame payload.
func decodeFlatePayload(payload, buf, chunk []byte, flateR io.ReadCloser, stereo bool, config Configuration) ([]byte, bool, Configuration, error) {
	if len(payload) == 0 {
		return nil, false, 0, io.ErrUnexpectedEOF
	}

	// Decompress the payload.
	var r io.ReadCloser
	if flateR != nil {
		if resetter, ok := flateR.(flate.Resetter); ok {
			if err := resetter.Reset(bytes.NewReader(payload), nil); err != nil {
				return nil, false, 0, err
			}
			r = flateR
		} else {
			r = flate.NewReader(bytes.NewReader(payload))
			defer r.Close()
		}
	} else {
		r = flate.NewReader(bytes.NewReader(payload))
		defer r.Close()
	}

	// Use provided buffer if available, otherwise allocate.
	if buf != nil {
		buf = buf[:0]
	} else {
		buf = make([]byte, 0, 4096)
	}

	// Use provided chunk buffer if available, otherwise allocate.
	if chunk == nil {
		chunk = make([]byte, 4096)
	}

	// Read in chunks to reuse buffer and enforce limit.
	for {
		n, readErr := r.Read(chunk)
		if n > 0 {
			if len(buf)+n > maxDecompressedBytes {
				return nil, false, 0, ErrPayloadTooLarge
			}
			buf = append(buf, chunk[:n]...)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, false, 0, readErr
		}
	}

	if len(buf)%2 != 0 {
		return nil, false, 0, ErrInvalidFrameData
	}

	return buf, stereo, config, nil
}

// decodeTwoFrames decodes two flate-compressed frames and concatenates them.
func decodeTwoFrames(frame1, frame2, buf, chunk []byte, flateR io.ReadCloser, stereo bool, config Configuration) ([]byte, bool, Configuration, error) {
	// Decode first frame
	raw1, _, _, err := decodeFlatePayload(frame1, nil, chunk, nil, stereo, config)
	if err != nil {
		return nil, false, 0, fmt.Errorf("decode frame 1: %w", err)
	}

	// Decode second frame
	raw2, _, _, err := decodeFlatePayload(frame2, nil, chunk, nil, stereo, config)
	if err != nil {
		return nil, false, 0, fmt.Errorf("decode frame 2: %w", err)
	}

	// Concatenate frames
	if buf != nil {
		buf = buf[:0]
	} else {
		buf = make([]byte, 0, len(raw1)+len(raw2))
	}
	buf = append(buf, raw1...)
	buf = append(buf, raw2...)

	return buf, stereo, config, nil
}

// decodeMultipleFrames decodes multiple flate-compressed frames and concatenates them.
func decodeMultipleFrames(frames [][]byte, buf, chunk []byte, stereo bool, config Configuration) ([]byte, bool, Configuration, error) {
	var totalLen int
	decodedFrames := make([][]byte, len(frames))

	for i, frame := range frames {
		raw, _, _, err := decodeFlatePayload(frame, nil, chunk, nil, stereo, config)
		if err != nil {
			return nil, false, 0, fmt.Errorf("decode frame %d: %w", i, err)
		}
		decodedFrames[i] = raw
		totalLen += len(raw)
	}

	// Concatenate all frames
	if buf != nil {
		buf = buf[:0]
	} else {
		buf = make([]byte, 0, totalLen)
	}
	for _, decoded := range decodedFrames {
		buf = append(buf, decoded...)
	}

	return buf, stereo, config, nil
}
