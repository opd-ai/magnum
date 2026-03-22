// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
// It follows the pion/opus API patterns and produces Opus-structured packets.
//
// Note: This is a minimal implementation. Packets are encoded using flate
// compression of raw PCM samples rather than the SILK/CELT codecs defined in
// RFC 6716. Use the [Decode] function (or a matching magnum Decoder) to
// round-trip packets; they are not compatible with standard Opus decoders.
package magnum

// Configuration represents an Opus configuration number stored in bits 7–3 of
// the Table of Contents (TOC) header (RFC 6716 §3.1).
type Configuration byte

// frameCode represents the frame-count encoding stored in bits 1–0 of the TOC
// header.
type frameCode byte

const (
	// frameCodeOneFrame indicates a single-frame Opus packet.
	frameCodeOneFrame frameCode = 0
	// frameCodeTwoEqualFrames indicates two frames of equal compressed size.
	frameCodeTwoEqualFrames frameCode = 1
	// frameCodeTwoDifferentFrames indicates two frames with different compressed sizes.
	frameCodeTwoDifferentFrames frameCode = 2
	// frameCodeArbitraryFrames indicates an arbitrary number of frames (CBR or VBR).
	frameCodeArbitraryFrames frameCode = 3
)

// Standard Opus configurations (RFC 6716 Table 2, 20 ms frame durations).
const (
	// ConfigurationSILKNB20ms is SILK-only, narrowband (8 kHz), 20 ms frame.
	// This is configuration 1 in RFC 6716 Table 2 (group 0–3, index 1).
	ConfigurationSILKNB20ms Configuration = 1
	// ConfigurationSILKWB20ms is SILK-only, wideband (16 kHz), 20 ms frame.
	// This is configuration 9 in RFC 6716 Table 2 (group 8–11, index 1).
	ConfigurationSILKWB20ms Configuration = 9
	// ConfigurationCELTSWB20ms is CELT-only, superwideband (24 kHz), 20 ms frame.
	// This is configuration 27 in RFC 6716 Table 2 (group 24–27, index 3).
	ConfigurationCELTSWB20ms Configuration = 27
	// ConfigurationCELTFB20ms is CELT-only, fullband (48 kHz), 20 ms frame.
	// This is configuration 31 in RFC 6716 Table 2 (group 28–31, index 3).
	ConfigurationCELTFB20ms Configuration = 31
)

// configForSampleRate returns the Opus TOC configuration that best describes
// a 20 ms frame at the given sample rate. Values follow RFC 6716 Table 2.
func configForSampleRate(sampleRate int) Configuration {
	switch sampleRate {
	case 8000:
		return ConfigurationSILKNB20ms // narrowband
	case 16000:
		return ConfigurationSILKWB20ms // wideband
	case 24000:
		return ConfigurationCELTSWB20ms // superwideband
	default: // 48000
		return ConfigurationCELTFB20ms // fullband
	}
}

// tocHeader is the Table of Contents header byte defined in RFC 6716 §3.1.
// It encodes the configuration number (bits 7–3), stereo flag (bit 2), and
// frame-count code (bits 1–0).
type tocHeader byte

// newTOCHeader assembles a TOC header byte from its constituent fields.
func newTOCHeader(config Configuration, stereo bool, fc frameCode) tocHeader {
	result := byte(config) << 3
	if stereo {
		result |= 0b00000100
	}
	result |= byte(fc)
	return tocHeader(result)
}

// configuration returns the configuration number from the TOC header.
func (t tocHeader) configuration() Configuration {
	return Configuration(t >> 3)
}

// isStereo returns whether the packet carries a stereo signal.
func (t tocHeader) isStereo() bool {
	return (t & 0b00000100) != 0
}

// frameCode returns the frame-count code from the TOC header.
func (t tocHeader) frameCode() frameCode {
	return frameCode(t & 0b00000011)
}
