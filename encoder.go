package magnum

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
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

	// Reusable buffers to reduce allocations.
	rawPCM    []byte        // pre-allocated buffer for PCM serialization
	outputBuf bytes.Buffer  // reusable output buffer
	flateW    *flate.Writer // reusable flate compressor
}

// NewEncoder creates a new Encoder for the given sample rate and channel count.
//
// Supported sample rates: 8000, 16000, 24000, 48000 Hz.
// Supported channel counts: 1 (mono) or 2 (stereo).
func NewEncoder(sampleRate, channels int) (*Encoder, error) {
	if !isValidSampleRate(sampleRate) {
		return nil, ErrUnsupportedSampleRate
	}
	if channels < 1 || channels > 2 {
		return nil, ErrUnsupportedChannelCount
	}

	fb := newFrameBuffer(sampleRate, channels)

	// Pre-allocate rawPCM buffer for one frame (frameSize * 2 bytes per sample).
	rawPCM := make([]byte, fb.frameSize*2)

	// Initialize the flate writer with a dummy buffer; we'll reset it per frame.
	var outputBuf bytes.Buffer
	flateW, err := flate.NewWriter(&outputBuf, flate.DefaultCompression)
	if err != nil {
		return nil, fmt.Errorf("magnum: new encoder: %w", err)
	}

	return &Encoder{
		sampleRate: sampleRate,
		channels:   channels,
		bitrate:    64000, // default: 64 kbps
		buffer:     fb,
		rawPCM:     rawPCM,
		outputBuf:  outputBuf,
		flateW:     flateW,
	}, nil
}

// SetBitrate sets the target encoding bitrate in bits per second.
// Values below 6000 are clamped to 6000; values above 510000 are clamped to
// 510000. The bitrate is stored for future use when a full codec backend is
// integrated; the current simplified implementation does not use it.
//
// NOTE: bitrate is stored for future codec integration (ROADMAP Milestones 2-3);
// the current flate-based compression does not use it to control output size.
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

// Flush encodes any remaining buffered samples as a zero-padded final frame.
//
// Call Flush at end-of-stream to ensure partial frames are not lost. The
// returned packet contains a full-length frame with the partial samples at
// the beginning and zeros filling the remainder.
//
// Returns nil (with no error) when no partial frame is buffered.
func (e *Encoder) Flush() ([]byte, error) {
	frame := e.buffer.flush()
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

	// Serialise the frame as little-endian int16 bytes using pre-allocated buffer.
	for i, sample := range frame {
		binary.LittleEndian.PutUint16(e.rawPCM[i*2:], uint16(sample))
	}

	// Reset and reuse the output buffer.
	e.outputBuf.Reset()
	e.outputBuf.WriteByte(byte(toc))

	// Reset the flate writer to write to our output buffer.
	e.flateW.Reset(&e.outputBuf)
	if _, err := e.flateW.Write(e.rawPCM[:len(frame)*2]); err != nil {
		return nil, fmt.Errorf("magnum: encode frame: %w", err)
	}
	if err := e.flateW.Close(); err != nil {
		return nil, fmt.Errorf("magnum: encode frame: %w", err)
	}

	// Return a copy of the output to avoid data races if caller holds the slice.
	result := make([]byte, e.outputBuf.Len())
	copy(result, e.outputBuf.Bytes())
	return result, nil
}
