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
	sampleRate    int
	channels      int
	bitrate       int
	application   Application
	complexity    int           // 0-10, default 10
	bandwidth     Bandwidth     // default BandwidthAuto
	frameDuration FrameDuration // default FrameDuration20ms
	buffer        *frameBuffer

	// useCELT controls whether to use the CELT codec for 24/48 kHz.
	// When false (default), uses flate compression for backward compatibility.
	// When true, uses RFC 6716-style CELT encoding.
	useCELT bool

	// CELT encoder for 24 kHz and 48 kHz sample rates when useCELT is true.
	// nil for 8 kHz and 16 kHz (SILK paths), or when useCELT is false.
	celtEncoder *CELTFrameEncoder
	// celtEncoderR is a second CELT encoder for the right channel in stereo
	// dual mono mode. nil for mono or when useCELT is false.
	celtEncoderR *CELTFrameEncoder

	// useSILK controls whether to use the SILK codec for 8/16 kHz.
	// When false (default), uses flate compression for backward compatibility.
	// When true, uses RFC 6716-style SILK encoding.
	useSILK bool

	// SILK encoder for 8 kHz and 16 kHz sample rates when useSILK is true.
	// nil for 24 kHz and 48 kHz (CELT paths), or when useSILK is false.
	silkEncoder *SILKFrameEncoder
	// silkEncoderR is a second SILK encoder for the right channel in stereo
	// dual mono mode. nil for mono or when useSILK is false.
	silkEncoderR *SILKFrameEncoder

	// dtx implements Discontinuous Transmission for bandwidth reduction
	// during silence periods. Initialized lazily when DTX is enabled.
	dtx *DTX

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
// By default, the encoder uses flate compression for backward compatibility.
// To enable RFC 6716–style CELT encoding for 24/48 kHz, call [Encoder.EnableCELT].
func NewEncoderWithApplication(sampleRate, channels int, app Application) (*Encoder, error) {
	if !isValidSampleRate(sampleRate) {
		return nil, ErrUnsupportedSampleRate
	}
	if channels < 1 || channels > 2 {
		return nil, ErrUnsupportedChannelCount
	}

	fb := newFrameBufferWithDuration(sampleRate, channels, FrameDuration20ms)

	// Pre-allocate rawPCM buffer for one frame (frameSize * 2 bytes per sample).
	rawPCM := make([]byte, fb.frameSize*2)

	// Initialize the flate writer with a dummy buffer; we'll reset it per frame.
	var outputBuf bytes.Buffer
	flateW, err := flate.NewWriter(&outputBuf, flate.DefaultCompression)
	if err != nil {
		return nil, fmt.Errorf("magnum: new encoder: %w", err)
	}

	return &Encoder{
		sampleRate:    sampleRate,
		channels:      channels,
		bitrate:       64000, // default: 64 kbps
		application:   app,
		complexity:    10,                // default: highest quality
		bandwidth:     BandwidthAuto,     // default: automatic
		frameDuration: FrameDuration20ms, // default: 20 ms
		buffer:        fb,
		useCELT:       false, // default: use flate for backward compatibility
		rawPCM:        rawPCM,
		outputBuf:     outputBuf,
		flateW:        flateW,
	}, nil
}

// EnableCELT enables RFC 6716–style CELT encoding for 24 kHz and 48 kHz
// sample rates. Returns an error if the encoder's sample rate is not
// suitable for CELT (must be 24000 or 48000 Hz).
//
// When CELT is enabled, packets are encoded using the CELT codec as specified
// in RFC 6716 §4.3. Note that CELT is a lossy codec, so exact round-trip
// reconstruction of PCM samples is not possible. Use the CELT-enabled
// [Decoder] for decoding these packets.
//
// For stereo input, dual mono encoding is used: each channel is encoded
// independently and concatenated in the output packet.
//
// This method is part of ROADMAP Milestone 2f (CELT integration).
func (e *Encoder) EnableCELT() error {
	if e.sampleRate != 24000 && e.sampleRate != 48000 {
		return fmt.Errorf("magnum: CELT requires 24000 or 48000 Hz sample rate, got %d", e.sampleRate)
	}

	if e.celtEncoder == nil {
		frameSize := e.sampleRate * frameDurationMs / 1000 // Samples per channel
		// Note: CELT internally processes mono; for stereo, we use dual mono
		celtConfig := CELTFrameConfig{
			SampleRate: e.sampleRate,
			Channels:   1, // Each encoder handles one channel
			FrameSize:  frameSize,
			Bitrate:    e.bitrate / e.channels, // Split bitrate for dual mono
		}
		celtEnc, err := NewCELTFrameEncoder(celtConfig)
		if err != nil {
			return fmt.Errorf("magnum: enable CELT: %w", err)
		}
		e.celtEncoder = celtEnc

		// Create second encoder for right channel in stereo mode
		if e.channels == 2 {
			celtEncR, err := NewCELTFrameEncoder(celtConfig)
			if err != nil {
				return fmt.Errorf("magnum: enable CELT (right channel): %w", err)
			}
			e.celtEncoderR = celtEncR
		}
	}

	e.useCELT = true
	return nil
}

// IsCELTEnabled returns true if CELT encoding is enabled for this encoder.
func (e *Encoder) IsCELTEnabled() bool {
	return e.useCELT && e.celtEncoder != nil
}

// EnableSILK enables RFC 6716–style SILK encoding for 8 kHz and 16 kHz
// sample rates. Returns an error if the encoder's sample rate is not
// suitable for SILK (must be 8000 or 16000 Hz).
//
// When SILK is enabled, packets are encoded using the SILK codec as specified
// in RFC 6716 §4.2. Note that SILK is a lossy codec, so exact round-trip
// reconstruction of PCM samples is not possible.
//
// For stereo input, dual mono encoding is used: each channel is encoded
// independently and concatenated in the output packet.
//
// This method is part of ROADMAP Milestone 3f (SILK integration).
func (e *Encoder) EnableSILK() error {
	if e.sampleRate != 8000 && e.sampleRate != 16000 {
		return fmt.Errorf("magnum: SILK requires 8000 or 16000 Hz sample rate, got %d", e.sampleRate)
	}

	if e.silkEncoder == nil {
		frameSize := e.sampleRate * frameDurationMs / 1000 // Samples per channel
		// Note: SILK internally processes mono; for stereo, we use dual mono
		silkConfig := SILKFrameConfig{
			SampleRate: e.sampleRate,
			Channels:   1, // Each encoder handles one channel
			FrameSize:  frameSize,
			Bitrate:    e.bitrate / e.channels, // Split bitrate for dual mono
		}
		silkEnc, err := NewSILKFrameEncoder(silkConfig)
		if err != nil {
			return fmt.Errorf("magnum: enable SILK: %w", err)
		}
		e.silkEncoder = silkEnc

		// Create second encoder for right channel in stereo mode
		if e.channels == 2 {
			silkEncR, err := NewSILKFrameEncoder(silkConfig)
			if err != nil {
				return fmt.Errorf("magnum: enable SILK (right channel): %w", err)
			}
			e.silkEncoderR = silkEncR
		}
	}

	e.useSILK = true
	return nil
}

// IsSILKEnabled returns true if SILK encoding is enabled for this encoder.
func (e *Encoder) IsSILKEnabled() bool {
	return e.useSILK && e.silkEncoder != nil
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

// SetFrameDuration sets the frame duration for encoding.
// Supported durations depend on the sample rate:
//   - 8/16 kHz (SILK):  10, 20, 40, 60 ms
//   - 24/48 kHz (CELT): 2.5, 5, 10, 20 ms
//
// Returns an error if the duration is not supported for the encoder's sample rate.
// Changing the frame duration recreates the internal buffer, discarding any
// buffered samples.
func (e *Encoder) SetFrameDuration(duration FrameDuration) error {
	// Validate duration for sample rate
	if err := validateFrameDuration(e.sampleRate, duration); err != nil {
		return err
	}

	e.frameDuration = duration

	// Recreate frame buffer with new duration
	e.buffer = newFrameBufferWithDuration(e.sampleRate, e.channels, duration)

	// Reallocate rawPCM buffer for new frame size
	e.rawPCM = make([]byte, e.buffer.frameSize*2)

	// Update CELT encoder if active
	if e.celtEncoder != nil {
		frameSize := duration.Samples(e.sampleRate)
		e.celtEncoder.config.FrameSize = frameSize
	}

	// Update SILK encoder if active
	if e.silkEncoder != nil {
		frameSize := duration.Samples(e.sampleRate)
		e.silkEncoder.config.FrameSize = frameSize
	}

	return nil
}

// FrameDuration returns the frame duration configured for this encoder.
func (e *Encoder) FrameDuration() FrameDuration {
	return e.frameDuration
}

// validateFrameDuration checks if the given duration is valid for the sample rate.
func validateFrameDuration(sampleRate int, duration FrameDuration) error {
	switch sampleRate {
	case SampleRate8k, SampleRate16k:
		// SILK mode: 10, 20, 40, 60 ms
		switch duration {
		case FrameDuration10ms, FrameDuration20ms, FrameDuration40ms, FrameDuration60ms:
			return nil
		default:
			return fmt.Errorf("magnum: frame duration %.1fms not supported for %d Hz (SILK supports 10, 20, 40, 60 ms)", duration.Milliseconds(), sampleRate)
		}
	case SampleRate24k, SampleRate48k:
		// CELT mode: 2.5, 5, 10, 20 ms
		switch duration {
		case FrameDuration2_5ms, FrameDuration5ms, FrameDuration10ms, FrameDuration20ms:
			return nil
		default:
			return fmt.Errorf("magnum: frame duration %.1fms not supported for %d Hz (CELT supports 2.5, 5, 10, 20 ms)", duration.Milliseconds(), sampleRate)
		}
	default:
		return fmt.Errorf("magnum: unsupported sample rate %d", sampleRate)
	}
}

// EnableDTX enables Discontinuous Transmission (DTX) mode.
//
// When DTX is enabled, the encoder uses Voice Activity Detection (VAD) to
// detect silence frames. During silence, the encoder returns nil instead of
// encoding a packet, reducing bandwidth. The decoder uses Packet Loss
// Concealment (PLC) to synthesize comfort noise during these periods.
//
// DTX is most effective for VoIP applications with significant pause time.
// It is automatically enabled when using ApplicationVoIP mode.
func (e *Encoder) EnableDTX() {
	if e.dtx == nil {
		config := DefaultDTXConfig()
		config.Enabled = true
		e.dtx = NewDTX(e.sampleRate, config)
	} else {
		e.dtx.SetEnabled(true)
	}
}

// DisableDTX disables Discontinuous Transmission (DTX) mode.
func (e *Encoder) DisableDTX() {
	if e.dtx != nil {
		e.dtx.SetEnabled(false)
	}
}

// IsDTXEnabled returns true if DTX mode is enabled.
func (e *Encoder) IsDTXEnabled() bool {
	return e.dtx != nil && e.dtx.IsEnabled()
}

// DTXStats returns DTX statistics: transmitted frames and suppressed frames.
// Returns (0, 0) if DTX is not enabled.
func (e *Encoder) DTXStats() (transmitted, suppressed int) {
	if e.dtx == nil {
		return 0, 0
	}
	return e.dtx.Stats()
}

// SetDTXConfig sets the DTX configuration.
// Creates DTX if not already initialized.
func (e *Encoder) SetDTXConfig(config DTXConfig) {
	if e.dtx == nil {
		e.dtx = NewDTX(e.sampleRate, config)
	} else {
		e.dtx.SetConfig(config)
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
		if err := e.buffer.write(pcm); err != nil {
			return nil, fmt.Errorf("magnum: encode: %w", err)
		}
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
//	bytes 1… : payload (CELT bitstream for 24/48 kHz, flate for 8/16 kHz)
//
// For 24 kHz and 48 kHz sample rates, this uses the CELT codec to produce
// RFC 6716–compliant packets. For 8 kHz and 16 kHz, flate compression is
// used as a placeholder until SILK is implemented.
//
// When DTX is enabled and the frame is detected as silence, returns nil
// to suppress transmission and reduce bandwidth.
func (e *Encoder) encodeFrame(frame []int16) ([]byte, error) {
	// Check DTX - if enabled and frame is silence, suppress transmission
	if e.dtx != nil && e.dtx.IsEnabled() {
		decision := e.dtx.ProcessInt16(frame)
		if !decision.Transmit {
			// DTX: suppress this frame (silence detected)
			return nil, nil
		}
	}

	isStereo := e.channels == 2
	config := configForSampleRateAndDuration(e.sampleRate, e.frameDuration)
	toc := newTOCHeader(config, isStereo, frameCodeOneFrame)

	// Use SILK when enabled and available (8/16 kHz)
	if e.useSILK && e.silkEncoder != nil {
		return e.encodeFrameSILK(frame, toc)
	}

	// Use CELT when enabled and available (24/48 kHz)
	if e.useCELT && e.celtEncoder != nil {
		return e.encodeFrameCELT(frame, toc)
	}

	// Default: flate compression (backward compatible)
	return e.encodeFrameFlate(frame, toc)
}

// encodeFrameSILK encodes a frame using the SILK codec for RFC 6716 compliance.
// For stereo input, dual mono encoding is used where each channel is encoded
// independently and the encoded data is concatenated.
func (e *Encoder) encodeFrameSILK(frame []int16, toc tocHeader) ([]byte, error) {
	samplesPerChannel := len(frame) / e.channels

	if e.channels == 1 {
		// Mono: convert and encode directly
		floatSamples := make([]float64, samplesPerChannel)
		for i, s := range frame {
			floatSamples[i] = float64(s) / 32768.0
		}

		// Encode with SILK
		silkFrame, err := e.silkEncoder.EncodeFrame(floatSamples)
		if err != nil {
			return nil, fmt.Errorf("magnum: encode frame: SILK: %w", err)
		}

		// Build packet: TOC header + SILK payload
		result := make([]byte, 1+len(silkFrame.Data))
		result[0] = byte(toc)
		copy(result[1:], silkFrame.Data)
		return result, nil
	}

	// Stereo: dual mono encoding - encode each channel independently
	leftSamples := make([]float64, samplesPerChannel)
	rightSamples := make([]float64, samplesPerChannel)

	for i := 0; i < samplesPerChannel; i++ {
		leftSamples[i] = float64(frame[i*2]) / 32768.0
		rightSamples[i] = float64(frame[i*2+1]) / 32768.0
	}

	// Encode left channel
	leftFrame, err := e.silkEncoder.EncodeFrame(leftSamples)
	if err != nil {
		return nil, fmt.Errorf("magnum: encode frame: SILK left: %w", err)
	}

	// Encode right channel
	rightFrame, err := e.silkEncoderR.EncodeFrame(rightSamples)
	if err != nil {
		return nil, fmt.Errorf("magnum: encode frame: SILK right: %w", err)
	}

	// Build packet: TOC header + left SILK payload + right SILK payload
	// In RFC 6716 dual mono, the two channel frames are simply concatenated
	result := make([]byte, 1+len(leftFrame.Data)+len(rightFrame.Data))
	result[0] = byte(toc)
	copy(result[1:], leftFrame.Data)
	copy(result[1+len(leftFrame.Data):], rightFrame.Data)

	return result, nil
}

// encodeFrameCELT encodes a frame using the CELT codec for RFC 6716 compliance.
// For stereo input, dual mono encoding is used where each channel is encoded
// independently and the encoded data is concatenated.
func (e *Encoder) encodeFrameCELT(frame []int16, toc tocHeader) ([]byte, error) {
	samplesPerChannel := len(frame) / e.channels

	if e.channels == 1 {
		// Mono: convert and encode directly
		floatSamples := make([]float64, samplesPerChannel)
		for i, s := range frame {
			floatSamples[i] = float64(s) / 32768.0
		}

		// Update CELT encoder bitrate if it changed
		e.celtEncoder.config.Bitrate = e.bitrate

		// Encode with CELT
		celtFrame, err := e.celtEncoder.EncodeFrame(floatSamples)
		if err != nil {
			return nil, fmt.Errorf("magnum: encode frame: CELT: %w", err)
		}

		// Build packet: TOC header + CELT payload
		result := make([]byte, 1+len(celtFrame.Data))
		result[0] = byte(toc)
		copy(result[1:], celtFrame.Data)
		return result, nil
	}

	// Stereo: dual mono encoding - encode each channel independently
	leftSamples := make([]float64, samplesPerChannel)
	rightSamples := make([]float64, samplesPerChannel)

	for i := 0; i < samplesPerChannel; i++ {
		leftSamples[i] = float64(frame[i*2]) / 32768.0
		rightSamples[i] = float64(frame[i*2+1]) / 32768.0
	}

	// Update bitrate for both encoders (split evenly)
	bitratePerChannel := e.bitrate / 2
	e.celtEncoder.config.Bitrate = bitratePerChannel
	e.celtEncoderR.config.Bitrate = bitratePerChannel

	// Encode left channel
	leftFrame, err := e.celtEncoder.EncodeFrame(leftSamples)
	if err != nil {
		return nil, fmt.Errorf("magnum: encode frame: CELT left: %w", err)
	}

	// Encode right channel
	rightFrame, err := e.celtEncoderR.EncodeFrame(rightSamples)
	if err != nil {
		return nil, fmt.Errorf("magnum: encode frame: CELT right: %w", err)
	}

	// Build packet: TOC header + left CELT payload + right CELT payload
	// In RFC 6716 dual mono, the two channel frames are simply concatenated
	result := make([]byte, 1+len(leftFrame.Data)+len(rightFrame.Data))
	result[0] = byte(toc)
	copy(result[1:], leftFrame.Data)
	copy(result[1+len(leftFrame.Data):], rightFrame.Data)

	return result, nil
}

// encodeFrameFlate encodes a frame using flate compression (fallback for SILK paths).
func (e *Encoder) encodeFrameFlate(frame []int16, toc tocHeader) ([]byte, error) {
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

// EncodeTwoFrames encodes two audio frames into a single Opus packet.
// This produces a frame-code 1 packet (two equal-size frames) if the
// compressed sizes are equal, or a frame-code 2 packet (two different-size
// frames) if they differ.
//
// Each frame must be exactly the encoder's frame size (FrameDuration worth
// of samples × channels). Returns an error if the frames are the wrong size.
//
// This is useful for reducing packet overhead when encoding at lower latencies
// (e.g., two 10ms frames = 20ms effective duration with better compression).
func (e *Encoder) EncodeTwoFrames(frame1, frame2 []int16) ([]byte, error) {
	expectedSize := e.buffer.frameSize
	if len(frame1) != expectedSize {
		return nil, fmt.Errorf("magnum: frame1 has %d samples, want %d", len(frame1), expectedSize)
	}
	if len(frame2) != expectedSize {
		return nil, fmt.Errorf("magnum: frame2 has %d samples, want %d", len(frame2), expectedSize)
	}

	// Encode each frame individually to get the payloads
	// We need to temporarily store the payloads without TOC headers
	payload1, err := e.encodeFramePayload(frame1)
	if err != nil {
		return nil, fmt.Errorf("magnum: encode frame1: %w", err)
	}
	payload2, err := e.encodeFramePayload(frame2)
	if err != nil {
		return nil, fmt.Errorf("magnum: encode frame2: %w", err)
	}

	// DTX check - if both frames are silence, return nil
	if payload1 == nil && payload2 == nil {
		return nil, nil
	}
	// If only one is DTX-suppressed, encode it as silence
	if payload1 == nil {
		payload1 = e.encodeSilencePayload()
	}
	if payload2 == nil {
		payload2 = e.encodeSilencePayload()
	}

	isStereo := e.channels == 2
	config := configForSampleRateAndDuration(e.sampleRate, e.frameDuration)

	if len(payload1) == len(payload2) {
		// Frame code 1: two equal-size frames
		// Packet: [TOC][Frame1][Frame2]
		toc := newTOCHeader(config, isStereo, frameCodeTwoEqualFrames)
		result := make([]byte, 1+len(payload1)+len(payload2))
		result[0] = byte(toc)
		copy(result[1:], payload1)
		copy(result[1+len(payload1):], payload2)
		return result, nil
	}

	// Frame code 2: two different-size frames
	// Packet: [TOC][length1][Frame1][Frame2]
	toc := newTOCHeader(config, isStereo, frameCodeTwoDifferentFrames)
	lenBytes := encodeFrameLength(len(payload1))
	result := make([]byte, 1+len(lenBytes)+len(payload1)+len(payload2))
	result[0] = byte(toc)
	copy(result[1:], lenBytes)
	copy(result[1+len(lenBytes):], payload1)
	copy(result[1+len(lenBytes)+len(payload1):], payload2)
	return result, nil
}

// EncodeMultipleFrames encodes multiple audio frames into a single Opus packet
// using frame code 3 (arbitrary number of frames, VBR mode).
//
// Each frame must be exactly the encoder's frame size. Returns an error if
// any frame has the wrong size or if more than 48 frames are provided.
//
// This is useful for bundling multiple short frames into a single packet,
// reducing per-packet overhead for low-latency streaming.
func (e *Encoder) EncodeMultipleFrames(frames [][]int16) ([]byte, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("magnum: no frames provided")
	}
	if len(frames) > 48 {
		// Max 120ms per packet with 2.5ms frames = 48 frames
		return nil, fmt.Errorf("magnum: too many frames (%d), max 48", len(frames))
	}

	expectedSize := e.buffer.frameSize
	for i, frame := range frames {
		if len(frame) != expectedSize {
			return nil, fmt.Errorf("magnum: frame %d has %d samples, want %d", i, len(frame), expectedSize)
		}
	}

	// Encode all frames
	payloads := make([][]byte, len(frames))
	allSilence := true
	for i, frame := range frames {
		payload, err := e.encodeFramePayload(frame)
		if err != nil {
			return nil, fmt.Errorf("magnum: encode frame %d: %w", i, err)
		}
		if payload == nil {
			// DTX suppressed - encode as silence
			payload = e.encodeSilencePayload()
		} else {
			allSilence = false
		}
		payloads[i] = payload
	}

	// If all frames are silence, suppress the entire packet
	if allSilence && e.dtx != nil && e.dtx.IsEnabled() {
		return nil, nil
	}

	// Build frame code 3 packet
	// Format: [TOC][M byte][len1][frame1][len2][frame2]...[lenM-1][frameM-1][frameM]
	// M byte: bits 2-7 = frame count, bit 1 = padding (0), bit 0 = VBR (1)
	isStereo := e.channels == 2
	config := configForSampleRateAndDuration(e.sampleRate, e.frameDuration)
	toc := newTOCHeader(config, isStereo, frameCodeArbitraryFrames)

	// M byte: frame count (shifted left 2) | padding=0 | VBR=1
	mByte := byte(len(frames)<<2) | 0x01 // VBR mode, no padding

	// Calculate total size
	totalSize := 2 // TOC + M byte
	for i := 0; i < len(payloads)-1; i++ {
		totalSize += len(encodeFrameLength(len(payloads[i])))
		totalSize += len(payloads[i])
	}
	totalSize += len(payloads[len(payloads)-1]) // Last frame (no length prefix)

	result := make([]byte, totalSize)
	result[0] = byte(toc)
	result[1] = mByte

	offset := 2
	for i := 0; i < len(payloads)-1; i++ {
		lenBytes := encodeFrameLength(len(payloads[i]))
		copy(result[offset:], lenBytes)
		offset += len(lenBytes)
		copy(result[offset:], payloads[i])
		offset += len(payloads[i])
	}
	// Last frame (no length prefix)
	copy(result[offset:], payloads[len(payloads)-1])

	return result, nil
}

// encodeFramePayload encodes a frame and returns just the payload (no TOC).
// Returns nil if DTX suppresses the frame.
func (e *Encoder) encodeFramePayload(frame []int16) ([]byte, error) {
	// Check DTX
	if e.dtx != nil && e.dtx.IsEnabled() {
		decision := e.dtx.ProcessInt16(frame)
		if !decision.Transmit {
			return nil, nil // DTX-suppressed
		}
	}

	// Use SILK when enabled
	if e.useSILK && e.silkEncoder != nil {
		return e.encodeSILKPayload(frame)
	}

	// Use CELT when enabled
	if e.useCELT && e.celtEncoder != nil {
		return e.encodeCELTPayload(frame)
	}

	// Default: flate compression
	return e.encodeFlatePayload(frame)
}

// encodeSILKPayload encodes a frame using SILK and returns just the payload.
func (e *Encoder) encodeSILKPayload(frame []int16) ([]byte, error) {
	samplesPerChannel := len(frame) / e.channels
	floatSamples := make([]float64, samplesPerChannel)

	if e.channels == 1 {
		for i, s := range frame {
			floatSamples[i] = float64(s) / 32768.0
		}
	} else {
		for i := 0; i < samplesPerChannel; i++ {
			left := float64(frame[i*2]) / 32768.0
			right := float64(frame[i*2+1]) / 32768.0
			floatSamples[i] = (left + right) / 2.0
		}
	}

	silkFrame, err := e.silkEncoder.EncodeFrame(floatSamples)
	if err != nil {
		return nil, err
	}
	return silkFrame.Data, nil
}

// encodeCELTPayload encodes a frame using CELT and returns just the payload.
func (e *Encoder) encodeCELTPayload(frame []int16) ([]byte, error) {
	samplesPerChannel := len(frame) / e.channels
	floatSamples := make([]float64, samplesPerChannel)

	if e.channels == 1 {
		for i, s := range frame {
			floatSamples[i] = float64(s) / 32768.0
		}
	} else {
		for i := 0; i < samplesPerChannel; i++ {
			left := float64(frame[i*2]) / 32768.0
			right := float64(frame[i*2+1]) / 32768.0
			floatSamples[i] = (left + right) / 2.0
		}
	}

	e.celtEncoder.config.Bitrate = e.bitrate
	celtFrame, err := e.celtEncoder.EncodeFrame(floatSamples)
	if err != nil {
		return nil, err
	}
	return celtFrame.Data, nil
}

// encodeFlatePayload encodes a frame using flate and returns just the payload.
func (e *Encoder) encodeFlatePayload(frame []int16) ([]byte, error) {
	for i, sample := range frame {
		binary.LittleEndian.PutUint16(e.rawPCM[i*2:], uint16(sample))
	}

	e.outputBuf.Reset()
	e.flateW.Reset(&e.outputBuf)
	if _, err := e.flateW.Write(e.rawPCM[:len(frame)*2]); err != nil {
		return nil, err
	}
	if err := e.flateW.Close(); err != nil {
		return nil, err
	}

	result := make([]byte, e.outputBuf.Len())
	copy(result, e.outputBuf.Bytes())
	return result, nil
}

// encodeSilencePayload returns a minimal encoded silence payload.
func (e *Encoder) encodeSilencePayload() []byte {
	// Generate silence samples and encode
	silence := make([]int16, e.buffer.frameSize)
	payload, _ := e.encodeFlatePayload(silence)
	return payload
}

// encodeFrameLength encodes a frame length using the Opus variable-length
// encoding (RFC 6716 §3.2.1):
//   - Length <= 251: single byte
//   - Length >= 252: two bytes where length = first_byte + second_byte*4
//     with first_byte in [252, 255]
func encodeFrameLength(length int) []byte {
	if length <= 251 {
		return []byte{byte(length)}
	}
	// Two-byte encoding: length = first_byte + second_byte*4
	// first_byte must be in [252, 255], so we use first_byte = 252 + (length % 4)
	// But wait - we need length >= 252 and first_byte >= 252
	// So: length = first_byte + second_byte*4
	// Rearranging: second_byte = (length - first_byte) / 4
	// We want first_byte to be 252 + (length-252)%4 so that division is exact
	firstByte := 252 + (length-252)%4
	secondByte := (length - firstByte) / 4
	return []byte{byte(firstByte), byte(secondByte)}
}

// decodeFrameLength decodes a frame length from the Opus variable-length format.
// Returns the length and the number of bytes consumed.
func decodeFrameLength(data []byte) (length, consumed int) {
	if len(data) == 0 {
		return 0, 0
	}
	if data[0] <= 251 {
		return int(data[0]), 1
	}
	if len(data) < 2 {
		return 0, 0
	}
	// Two-byte encoding: length = first_byte + second_byte*4
	length = int(data[0]) + int(data[1])*4
	return length, 2
}
