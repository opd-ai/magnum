package magnum

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
)

// Application represents the encoder application mode.
// It hints the encoder about the intended use case for optimization.
// This follows the pion/opus API pattern.
type Application int

const (
	// ApplicationVoIP optimizes for voice over IP, prioritizing low latency
	// and speech intelligibility. This mode enables features like DTX
	// (discontinuous transmission) and is optimized for speech.
	ApplicationVoIP Application = 2048

	// ApplicationAudio optimizes for general audio, providing the best quality
	// for music and other non-speech audio. This is the default mode.
	ApplicationAudio Application = 2049

	// ApplicationLowDelay provides the lowest possible latency at the expense
	// of some quality. Useful for real-time applications like live instruments.
	ApplicationLowDelay Application = 2051
)

// Bandwidth represents the encoder bandwidth setting.
// It controls the maximum audio bandwidth that will be encoded.
// This follows the pion/opus API pattern.
type Bandwidth int

const (
	// BandwidthNarrowband limits audio to 4 kHz bandwidth (8 kHz sample rate).
	BandwidthNarrowband Bandwidth = 1101

	// BandwidthMediumband limits audio to 6 kHz bandwidth (12 kHz sample rate).
	BandwidthMediumband Bandwidth = 1102

	// BandwidthWideband limits audio to 8 kHz bandwidth (16 kHz sample rate).
	BandwidthWideband Bandwidth = 1103

	// BandwidthSuperwideband limits audio to 12 kHz bandwidth (24 kHz sample rate).
	BandwidthSuperwideband Bandwidth = 1104

	// BandwidthFullband allows full 20 kHz bandwidth (48 kHz sample rate).
	BandwidthFullband Bandwidth = 1105

	// BandwidthAuto lets the encoder automatically select bandwidth based on
	// the input signal and bitrate. This is the default.
	BandwidthAuto Bandwidth = -1000
)

// Encoder is a simplified pure-Go Opus-compatible audio encoder.
//
// It follows the pion/opus API patterns and wraps PCM audio frames in
// Opus-structured packets. The frame payload is compressed with the standard
// library flate codec rather than SILK or CELT, so packets are not
// interoperable with standard Opus decoders. Use the [Decode] function
// (or a matching magnum decoder) to recover the original PCM samples.
type Encoder struct {
	sampleRate  int
	channels    int
	bitrate     int
	application Application
	complexity  int       // 0-10, default 10
	bandwidth   Bandwidth // default BandwidthAuto
	buffer      *frameBuffer

	// Reusable buffers to reduce allocations.
	rawPCM    []byte        // pre-allocated buffer for PCM serialization
	outputBuf bytes.Buffer  // reusable output buffer
	flateW    *flate.Writer // reusable flate compressor
}

// NewEncoder creates a new Encoder for the given sample rate and channel count.
//
// Supported sample rates: 8000, 16000, 24000, 48000 Hz.
// Supported channel counts: 1 (mono) or 2 (stereo).
//
// This is a convenience constructor that uses [ApplicationAudio] as the default
// application mode. For explicit control over the application mode, use
// [NewEncoderWithApplication].
func NewEncoder(sampleRate, channels int) (*Encoder, error) {
	return NewEncoderWithApplication(sampleRate, channels, ApplicationAudio)
}

// NewEncoderWithApplication creates a new Encoder with explicit application mode.
//
// Supported sample rates: 8000, 16000, 24000, 48000 Hz.
// Supported channel counts: 1 (mono) or 2 (stereo).
// Supported applications: [ApplicationVoIP], [ApplicationAudio], [ApplicationLowDelay].
//
// The application mode is stored for future codec integration (ROADMAP Milestones 2-3).
// The current simplified implementation does not yet use it to modify encoding behavior,
// but it will affect SILK/CELT mode selection when those codecs are implemented.
func NewEncoderWithApplication(sampleRate, channels int, app Application) (*Encoder, error) {
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
		sampleRate:  sampleRate,
		channels:    channels,
		bitrate:     64000, // default: 64 kbps
		application: app,
		complexity:  10,             // default: highest quality
		bandwidth:   BandwidthAuto,  // default: automatic
		buffer:      fb,
		rawPCM:      rawPCM,
		outputBuf:   outputBuf,
		flateW:      flateW,
	}, nil
}

// Application returns the application mode configured for this encoder.
func (e *Encoder) Application() Application {
	return e.application
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

// SetComplexity sets the encoder complexity level (0-10).
// Higher values provide better quality at the expense of CPU usage.
// Values outside the range are clamped to 0-10.
//
// NOTE: complexity is stored for future codec integration (ROADMAP Milestones 2-3);
// the current flate-based compression does not use it.
func (e *Encoder) SetComplexity(complexity int) {
	switch {
	case complexity < 0:
		e.complexity = 0
	case complexity > 10:
		e.complexity = 10
	default:
		e.complexity = complexity
	}
}

// Complexity returns the complexity level configured for this encoder.
func (e *Encoder) Complexity() int {
	return e.complexity
}

// SetBandwidth sets the maximum audio bandwidth for encoding.
// Use [BandwidthAuto] to let the encoder automatically select.
//
// NOTE: bandwidth is stored for future codec integration (ROADMAP Milestones 2-3);
// the current flate-based compression does not use it.
func (e *Encoder) SetBandwidth(bandwidth Bandwidth) {
	e.bandwidth = bandwidth
}

// Bandwidth returns the bandwidth setting configured for this encoder.
func (e *Encoder) Bandwidth() Bandwidth {
	return e.bandwidth
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
