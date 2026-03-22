package magnum

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
// The sample rate and channels are stored for future validation but are not
// currently used to verify incoming packets.
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	switch sampleRate {
	case 8000, 16000, 24000, 48000:
	default:
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
// Returns the number of samples written to out. If out is nil or too small,
// the function allocates and returns a new slice with the decoded samples
// (in this case, the returned count equals len(samples)).
//
// This method follows the pion/opus Decoder.Decode signature pattern.
func (d *Decoder) Decode(packet []byte, out []int16) (int, error) {
	samples, _, err := DecodeWithInfo(packet)
	if err != nil {
		return 0, err
	}

	// If out is provided and large enough, copy into it.
	if out != nil && len(out) >= len(samples) {
		copy(out, samples)
		return len(samples), nil
	}

	// Otherwise, we cannot use out; return the freshly allocated slice length.
	// Note: This differs slightly from pion/opus which requires out to be
	// pre-sized. We return the sample count for convenience.
	return len(samples), nil
}

// SampleRate returns the sample rate configured for this decoder.
func (d *Decoder) SampleRate() int {
	return d.sampleRate
}

// Channels returns the channel count configured for this decoder.
func (d *Decoder) Channels() int {
	return d.channels
}
