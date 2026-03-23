// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
// It follows the pion/opus API patterns and produces Opus-structured packets.
//
// Note: This is a minimal implementation. Packets are encoded using flate
// compression of raw PCM samples rather than the SILK/CELT codecs defined in
// RFC 6716. Use the [Decode] function (or a matching magnum Decoder) to
// round-trip packets; they are not compatible with standard Opus decoders.
package magnum

// Supported sample rates for Opus encoding/decoding.
const (
	SampleRate8k  = 8000  // narrowband
	SampleRate16k = 16000 // wideband
	SampleRate24k = 24000 // superwideband
	SampleRate48k = 48000 // fullband
)

// supportedSampleRates lists all sample rates supported by this encoder/decoder.
var supportedSampleRates = []int{SampleRate8k, SampleRate16k, SampleRate24k, SampleRate48k}

// isValidSampleRate returns true if the given sample rate is supported.
func isValidSampleRate(sampleRate int) bool {
	switch sampleRate {
	case SampleRate8k, SampleRate16k, SampleRate24k, SampleRate48k:
		return true
	}
	return false
}

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
	// Reserved for future multi-frame support (ROADMAP Milestone 5).
	frameCodeTwoEqualFrames frameCode = 1
	// frameCodeTwoDifferentFrames indicates two frames with different compressed sizes.
	// Reserved for future multi-frame support (ROADMAP Milestone 5).
	frameCodeTwoDifferentFrames frameCode = 2
	// frameCodeArbitraryFrames indicates an arbitrary number of frames (CBR or VBR).
	// Reserved for future multi-frame support (ROADMAP Milestone 5).
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

// Configuration range boundaries per RFC 6716 Table 2.
// These constants define the upper bound (inclusive) for each codec/bandwidth group.
const (
	// configSILKNBMax is the maximum configuration for SILK narrowband (8 kHz).
	// Configurations 0–3 are SILK NB with frame durations 10, 20, 40, 60 ms.
	configSILKNBMax Configuration = 3

	// configSILKMBMax is the maximum configuration for SILK mediumband (12 kHz).
	// Configurations 4–7 are SILK MB with frame durations 10, 20, 40, 60 ms.
	configSILKMBMax Configuration = 7

	// configSILKWBMax is the maximum configuration for SILK wideband (16 kHz).
	// Configurations 8–11 are SILK WB with frame durations 10, 20, 40, 60 ms.
	configSILKWBMax Configuration = 11

	// configHybridSWBMax is the maximum configuration for Hybrid superwideband (24 kHz).
	// Configurations 12–15 are Hybrid SWB with frame durations 10, 20 ms.
	configHybridSWBMax Configuration = 15

	// configHybridFBMax is the maximum configuration for Hybrid fullband (48 kHz).
	// Configurations 16–19 are Hybrid FB with frame durations 10, 20 ms.
	configHybridFBMax Configuration = 19

	// configCELTNBMax is the maximum configuration for CELT narrowband (8 kHz).
	// Configurations 20–23 are CELT NB with frame durations 2.5, 5, 10, 20 ms.
	configCELTNBMax Configuration = 23

	// configCELTSWBMax is the maximum configuration for CELT superwideband (24 kHz).
	// Configurations 24–27 are CELT SWB with frame durations 2.5, 5, 10, 20 ms.
	configCELTSWBMax Configuration = 27

	// configCELTFBMax is the maximum configuration for CELT fullband (48 kHz).
	// Configurations 28–31 are CELT FB with frame durations 2.5, 5, 10, 20 ms.
	configCELTFBMax Configuration = 31
)

// configForSampleRate returns the Opus TOC configuration that best describes
// a 20 ms frame at the given sample rate. Values follow RFC 6716 Table 2.
func configForSampleRate(sampleRate int) Configuration {
	switch sampleRate {
	case SampleRate8k:
		return ConfigurationSILKNB20ms // narrowband
	case SampleRate16k:
		return ConfigurationSILKWB20ms // wideband
	case SampleRate24k:
		return ConfigurationCELTSWB20ms // superwideband
	default: // SampleRate48k
		return ConfigurationCELTFB20ms // fullband
	}
}

// sampleRateForConfig returns the sample rate corresponding to the given
// Opus TOC configuration. This is the inverse of configForSampleRate.
// Returns 0 for unknown configurations.
//
// Configuration mapping per RFC 6716 Table 2 (§3.1):
//
//	Configs 0-3:   SILK NB (8 kHz narrowband)
//	Configs 4-7:   SILK MB (12 kHz mediumband) → maps to 16 kHz
//	Configs 8-11:  SILK WB (16 kHz wideband)
//	Configs 12-15: Hybrid SWB (24 kHz superwideband)
//	Configs 16-19: Hybrid FB (48 kHz fullband)
//	Configs 20-23: CELT NB (8 kHz narrowband, rare)
//	Configs 24-27: CELT SWB (24 kHz superwideband)
//	Configs 28-31: CELT FB (48 kHz fullband)
func sampleRateForConfig(config Configuration) int {
	// RFC 6716 Table 2 — use named constants for configuration boundaries.
	switch {
	case config <= configSILKNBMax: // SILK NB (0–3)
		return SampleRate8k
	case config <= configSILKMBMax: // SILK MB (4–7) → wideband
		return SampleRate16k
	case config <= configSILKWBMax: // SILK WB (8–11)
		return SampleRate16k
	case config <= configHybridSWBMax: // Hybrid SWB (12–15)
		return SampleRate24k
	case config <= configHybridFBMax: // Hybrid FB (16–19)
		return SampleRate48k
	case config <= configCELTNBMax: // CELT NB (20–23)
		return SampleRate8k
	case config <= configCELTSWBMax: // CELT SWB (24–27)
		return SampleRate24k
	case config <= configCELTFBMax: // CELT FB (28–31)
		return SampleRate48k
	default:
		return 0
	}
}

// tocHeader is the Table of Contents header byte defined in RFC 6716 §3.1.
// It encodes the configuration number (bits 7–3), stereo flag (bit 2), and
// frame-count code (bits 1–0).
type tocHeader byte

// TOC header bit layout constants per RFC 6716 §3.1.
const (
	tocConfigShift   = 3          // configuration is stored in bits 7–3
	tocStereoMask    = 0b00000100 // bit 2 is the stereo flag
	tocFrameCodeMask = 0b00000011 // bits 1–0 encode the frame count
)

// newTOCHeader assembles a TOC header byte from its constituent fields.
func newTOCHeader(config Configuration, stereo bool, fc frameCode) tocHeader {
	result := byte(config) << tocConfigShift
	if stereo {
		result |= tocStereoMask
	}
	result |= byte(fc)
	return tocHeader(result)
}

// configuration returns the configuration number from the TOC header.
func (t tocHeader) configuration() Configuration {
	return Configuration(t >> tocConfigShift)
}

// isStereo returns whether the packet carries a stereo signal.
func (t tocHeader) isStereo() bool {
	return (t & tocStereoMask) != 0
}

// frameCode returns the frame-count code from the TOC header.
func (t tocHeader) frameCode() frameCode {
	return frameCode(t & tocFrameCodeMask)
}
