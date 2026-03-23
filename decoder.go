package magnum

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
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
// Note: This decoder only handles packets produced by magnum's Encoder.
// It does not decode packets from standard Opus encoders (libopus, etc.).
type Decoder struct {
	sampleRate int
	channels   int
}

// NewDecoder creates a new Decoder for the given sample rate and channel count.
//
// Supported sample rates: 8000, 16000, 24000, 48000 Hz.
// Supported channel counts: 1 (mono) or 2 (stereo).
//
// Design note: Unlike pion/opus (which uses a parameterless constructor), magnum
// requires sample rate and channels upfront. This "configuration-first" model
// enables early validation of incoming packets against expected parameters,
// catching mismatches (e.g., decoding a stereo packet with a mono decoder)
// as explicit errors rather than silent data corruption. This design choice
// prioritizes safety and explicit error handling over API similarity.
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	if !isValidSampleRate(sampleRate) {
		return nil, ErrUnsupportedSampleRate
	}
	if channels < 1 || channels > 2 {
		return nil, ErrUnsupportedChannelCount
	}
	return &Decoder{
		sampleRate: sampleRate,
		channels:   channels,
	}, nil
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
	samples, stereo, config, err := decodeInternal(packet)
	if err != nil {
		return 0, err
	}

	// Validate channel configuration.
	packetChannels := 1
	if stereo {
		packetChannels = 2
	}
	if packetChannels != d.channels {
		return 0, ErrChannelMismatch
	}

	// Validate sample rate configuration.
	packetSampleRate := sampleRateForConfig(config)
	if packetSampleRate != d.sampleRate {
		return 0, ErrSampleRateMismatch
	}

	// If out is provided and large enough, copy into it.
	if out != nil && len(out) >= len(samples) {
		copy(out, samples)
		return len(samples), nil
	}

	// out is nil or undersized; copy what we can (if anything).
	if out != nil {
		copy(out, samples)
	}
	return len(samples), nil
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
	samples, stereo, config, err := decodeInternal(packet)
	if err != nil {
		return nil, err
	}

	// Validate channel configuration.
	packetChannels := 1
	if stereo {
		packetChannels = 2
	}
	if packetChannels != d.channels {
		return nil, ErrChannelMismatch
	}

	// Validate sample rate configuration.
	packetSampleRate := sampleRateForConfig(config)
	if packetSampleRate != d.sampleRate {
		return nil, ErrSampleRateMismatch
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
	if len(packet) < 1 {
		return nil, false, 0, ErrTooShortForTableOfContentsHeader
	}
	if len(packet) == 1 {
		return nil, false, 0, io.ErrUnexpectedEOF
	}

	// Parse and validate TOC header.
	toc := tocHeader(packet[0])
	if toc.frameCode() != frameCodeOneFrame {
		return nil, false, 0, ErrUnsupportedFrameCode
	}
	stereo = toc.isStereo()
	config = toc.configuration()

	// Decompress the payload (everything after the TOC byte).
	// Limit decompressed output to maxDecompressedBytes+1 so that zip-bomb
	// payloads are caught without exhausting memory.
	r := flate.NewReader(bytes.NewReader(packet[1:]))
	limited := io.LimitReader(r, int64(maxDecompressedBytes)+1)
	raw, readErr := io.ReadAll(limited)
	closeErr := r.Close()

	if readErr != nil {
		return nil, false, 0, readErr
	}
	if closeErr != nil {
		return nil, false, 0, closeErr
	}
	if len(raw) > maxDecompressedBytes {
		return nil, false, 0, ErrPayloadTooLarge
	}

	if len(raw)%2 != 0 {
		return nil, false, 0, ErrInvalidFrameData
	}

	samples = make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return samples, stereo, config, nil
}
