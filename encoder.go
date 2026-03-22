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

// Encoder is a simplified pure-Go Opus-compatible audio encoder.
//
// It follows the pion/opus API patterns and wraps PCM audio frames in
// Opus-structured packets. The frame payload is compressed with the standard
// library flate codec rather than SILK or CELT, so packets are not
// interoperable with standard Opus decoders. Use the [Decode] function
// (or a matching magnum decoder) to recover the original PCM samples.
type Encoder struct {
	sampleRate int
	channels   int
	bitrate    int
	buffer     *frameBuffer
}

// NewEncoder creates a new Encoder for the given sample rate and channel count.
//
// Supported sample rates: 8000, 16000, 24000, 48000 Hz.
// Supported channel counts: 1 (mono) or 2 (stereo).
func NewEncoder(sampleRate, channels int) (*Encoder, error) {
	switch sampleRate {
	case 8000, 16000, 24000, 48000:
	default:
		return nil, ErrUnsupportedSampleRate
	}
	if channels < 1 || channels > 2 {
		return nil, ErrUnsupportedChannelCount
	}
	return &Encoder{
		sampleRate: sampleRate,
		channels:   channels,
		bitrate:    64000, // default: 64 kbps
		buffer:     newFrameBuffer(sampleRate, channels),
	}, nil
}

// SetBitrate sets the target encoding bitrate in bits per second.
// Values below 6000 are clamped to 6000; values above 510000 are clamped to
// 510000. The bitrate is stored for future use when a full codec backend is
// integrated; the current simplified implementation does not use it.
func (e *Encoder) SetBitrate(bitrate int) {
	const (
		minBitrate = 6000
		maxBitrate = 510000
	)
	switch {
	case bitrate < minBitrate:
		e.bitrate = minBitrate
	case bitrate > maxBitrate:
		e.bitrate = maxBitrate
	default:
		e.bitrate = bitrate
	}
}

// Encode encodes signed 16-bit interleaved PCM samples into an Opus packet.
//
// For stereo encoders, samples must be interleaved (L0, R0, L1, R1, …).
// One 20 ms frame requires sampleRate/50 samples per channel, i.e.
// sampleRate/50 samples for mono and sampleRate/25 samples for stereo.
//
// Samples are buffered internally. When a complete frame becomes available
// (including any frames buffered from a previous call), it is encoded and
// returned. Returns nil (with no error) when no complete frame is ready.
// Callers may pass nil or an empty slice to drain any buffered frames without
// supplying new data.
func (e *Encoder) Encode(pcm []int16) ([]byte, error) {
	if len(pcm) > 0 {
		e.buffer.write(pcm)
	}
	frame := e.buffer.next()
	if frame == nil {
		return nil, nil
	}
	return e.encodeFrame(frame)
}

// encodeFrame encodes a single audio frame into an Opus-structured packet.
//
// Packet layout:
//
//	byte 0   : TOC header (configuration | stereo flag | frame code)
//	bytes 1… : flate-compressed little-endian int16 PCM samples
func (e *Encoder) encodeFrame(frame []int16) ([]byte, error) {
	isStereo := e.channels == 2
	config := configForSampleRate(e.sampleRate)
	toc := newTOCHeader(config, isStereo, frameCodeOneFrame)

	// Serialise the frame as little-endian int16 bytes.
	rawPCM := make([]byte, len(frame)*2)
	for i, sample := range frame {
		binary.LittleEndian.PutUint16(rawPCM[i*2:], uint16(sample))
	}

	// Write TOC byte followed by flate-compressed PCM into the output buffer.
	var buf bytes.Buffer
	buf.WriteByte(byte(toc))

	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err = w.Write(rawPCM); err != nil {
		return nil, err
	}
	if err = w.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// Decode decodes an Opus packet that was produced by [Encoder.Encode].
//
// This is the inverse of Encode and is provided for round-trip testing.
// It does not decode packets produced by standard Opus encoders.
//
// Returns [ErrTooShortForTableOfContentsHeader] if the packet is completely
// empty, [io.ErrUnexpectedEOF] if only the TOC byte is present without a
// payload, and [ErrPayloadTooLarge] if the decompressed payload exceeds
// maxDecompressedBytes.
func Decode(packet []byte) ([]int16, error) {
	if len(packet) < 1 {
		return nil, ErrTooShortForTableOfContentsHeader
	}
	if len(packet) == 1 {
		return nil, io.ErrUnexpectedEOF
	}

	// Decompress the payload (everything after the TOC byte).
	// Limit decompressed output to maxDecompressedBytes+1 so that zip-bomb
	// payloads are caught without exhausting memory.
	r := flate.NewReader(bytes.NewReader(packet[1:]))
	limited := io.LimitReader(r, int64(maxDecompressedBytes)+1)
	raw, readErr := io.ReadAll(limited)
	closeErr := r.Close()

	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(raw) > maxDecompressedBytes {
		return nil, ErrPayloadTooLarge
	}

	if len(raw)%2 != 0 {
		return nil, ErrInvalidFrameData
	}

	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return samples, nil
}
