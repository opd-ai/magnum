package magnum

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"io"
)

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
		return nil, errUnsupportedSampleRate
	}
	if channels < 1 || channels > 2 {
		return nil, errUnsupportedChannelCount
	}
	return &Encoder{
		sampleRate: sampleRate,
		channels:   channels,
		bitrate:    64000, // default: 64 kbps
		buffer:     newFrameBuffer(sampleRate),
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

// Encode encodes a slice of signed 16-bit PCM samples into an Opus packet.
//
// Samples are buffered internally until a full 20 ms frame is available.
// Returns nil (with no error) when the buffer does not yet hold a complete
// frame. When more than one frame's worth of samples is supplied only the
// first complete frame is encoded; the remainder stays in the internal buffer
// and will be returned on a subsequent call.
func (e *Encoder) Encode(pcm []int16) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, nil
	}
	frames := e.buffer.write(pcm)
	if len(frames) == 0 {
		return nil, nil
	}
	return e.encodeFrame(frames[0])
}

// encodeFrame encodes a single audio frame into an Opus-structured packet.
//
// Packet layout:
//
//	byte 0   : TOC header (configuration | stereo flag | frame code)
//	bytes 1… : flate-compressed little-endian int16 PCM samples
func (e *Encoder) encodeFrame(frame []int16) ([]byte, error) {
	isStereo := e.channels == 2
	toc := newTOCHeader(ConfigurationCELTFB20ms, isStereo, frameCodeOneFrame)

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
func Decode(packet []byte) ([]int16, error) {
	if len(packet) < 2 {
		return nil, errTooShortForTableOfContentsHeader
	}

	// Decompress the payload (everything after the TOC byte).
	r := flate.NewReader(bytes.NewReader(packet[1:]))

	raw, err := io.ReadAll(r)
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	if err = r.Close(); err != nil {
		return nil, err
	}

	if len(raw)%2 != 0 {
		return nil, errInvalidFrameData
	}

	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return samples, nil
}
