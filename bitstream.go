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

// FrameDuration represents an Opus frame duration in milliseconds.
// Supported values depend on the codec mode (RFC 6716 §3.1):
//   - SILK:   10, 20, 40, 60 ms
//   - CELT:   2.5, 5, 10, 20 ms
//   - Hybrid: 10, 20 ms
type FrameDuration float64

const (
	// FrameDuration2p5ms is 2.5 ms frame duration (CELT only).
	// The "2p5" naming follows Go conventions for fractional values.
	FrameDuration2p5ms FrameDuration = 2.5
	// FrameDuration5ms is 5 ms frame duration (CELT only).
	FrameDuration5ms FrameDuration = 5
	// FrameDuration10ms is 10 ms frame duration (SILK, CELT, Hybrid).
	FrameDuration10ms FrameDuration = 10
	// FrameDuration20ms is 20 ms frame duration (SILK, CELT, Hybrid). This is the default.
	FrameDuration20ms FrameDuration = 20
	// FrameDuration40ms is 40 ms frame duration (SILK only).
	FrameDuration40ms FrameDuration = 40
	// FrameDuration60ms is 60 ms frame duration (SILK only).
	FrameDuration60ms FrameDuration = 60
)

// Milliseconds returns the frame duration in milliseconds as a float64.
func (fd FrameDuration) Milliseconds() float64 {
	return float64(fd)
}

// Samples returns the number of samples per channel for this frame duration
// at the given sample rate.
func (fd FrameDuration) Samples(sampleRate int) int {
	return int(float64(sampleRate) * float64(fd) / 1000.0)
}

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
	return configForSampleRateAndDuration(sampleRate, FrameDuration20ms)
}

// configForSampleRateAndDuration returns the Opus TOC configuration for
// the given sample rate and frame duration. Values follow RFC 6716 Table 2.
//
// RFC 6716 Table 2 layout (configurations 0-31):
//
//	SILK NB (8 kHz):    0=10ms, 1=20ms, 2=40ms, 3=60ms
//	SILK MB (12 kHz):   4=10ms, 5=20ms, 6=40ms, 7=60ms
//	SILK WB (16 kHz):   8=10ms, 9=20ms, 10=40ms, 11=60ms
//	Hybrid SWB (24 kHz): 12=10ms, 13=20ms
//	Hybrid FB (48 kHz):  16=10ms, 17=20ms
//	CELT NB (8 kHz):    20=2.5ms, 21=5ms, 22=10ms, 23=20ms
//	CELT WB (16 kHz):   (not standardized, use SILK)
//	CELT SWB (24 kHz):  24=2.5ms, 25=5ms, 26=10ms, 27=20ms
//	CELT FB (48 kHz):   28=2.5ms, 29=5ms, 30=10ms, 31=20ms
func configForSampleRateAndDuration(sampleRate int, duration FrameDuration) Configuration {
	// Map frame duration to index within each group
	// SILK group: 10ms=0, 20ms=1, 40ms=2, 60ms=3
	// CELT group: 2.5ms=0, 5ms=1, 10ms=2, 20ms=3
	// Hybrid group: 10ms=0, 20ms=1

	switch sampleRate {
	case SampleRate8k:
		// SILK NB (configurations 0-3)
		switch duration {
		case FrameDuration10ms:
			return 0
		case FrameDuration20ms:
			return 1
		case FrameDuration40ms:
			return 2
		case FrameDuration60ms:
			return 3
		default:
			return 1 // Default to 20ms
		}
	case SampleRate16k:
		// SILK WB (configurations 8-11)
		switch duration {
		case FrameDuration10ms:
			return 8
		case FrameDuration20ms:
			return 9
		case FrameDuration40ms:
			return 10
		case FrameDuration60ms:
			return 11
		default:
			return 9 // Default to 20ms
		}
	case SampleRate24k:
		// CELT SWB (configurations 24-27) for short durations
		// Hybrid SWB (configurations 12-13) for 10/20ms - but we use CELT
		switch duration {
		case FrameDuration2p5ms:
			return 24
		case FrameDuration5ms:
			return 25
		case FrameDuration10ms:
			return 26
		case FrameDuration20ms:
			return 27
		default:
			return 27 // Default to 20ms
		}
	case SampleRate48k:
		// CELT FB (configurations 28-31)
		switch duration {
		case FrameDuration2p5ms:
			return 28
		case FrameDuration5ms:
			return 29
		case FrameDuration10ms:
			return 30
		case FrameDuration20ms:
			return 31
		default:
			return 31 // Default to 20ms
		}
	default:
		return 31 // Default to 48kHz 20ms
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

// frameDurationForConfig returns the frame duration for the given configuration.
// RFC 6716 Table 2 defines the frame duration index within each group:
//
//	SILK groups (0-11): index 0=10ms, 1=20ms, 2=40ms, 3=60ms
//	Hybrid groups (12-19): index 0=10ms, 1=20ms (2,3 reserved)
//	CELT groups (20-31): index 0=2.5ms, 1=5ms, 2=10ms, 3=20ms
func frameDurationForConfig(config Configuration) FrameDuration {
	// Get the index within the group (0-3)
	var groupBase Configuration
	var isCELT, isHybrid bool

	switch {
	case config <= configSILKNBMax:
		groupBase = 0
	case config <= configSILKMBMax:
		groupBase = 4
	case config <= configSILKWBMax:
		groupBase = 8
	case config <= configHybridSWBMax:
		groupBase = 12
		isHybrid = true
	case config <= configHybridFBMax:
		groupBase = 16
		isHybrid = true
	case config <= configCELTNBMax:
		groupBase = 20
		isCELT = true
	case config <= configCELTSWBMax:
		groupBase = 24
		isCELT = true
	case config <= configCELTFBMax:
		groupBase = 28
		isCELT = true
	default:
		return FrameDuration20ms
	}

	index := config - groupBase

	if isCELT {
		// CELT: 2.5, 5, 10, 20 ms
		switch index {
		case 0:
			return FrameDuration2p5ms
		case 1:
			return FrameDuration5ms
		case 2:
			return FrameDuration10ms
		default:
			return FrameDuration20ms
		}
	}

	if isHybrid {
		// Hybrid: 10, 20 ms
		switch index {
		case 0:
			return FrameDuration10ms
		default:
			return FrameDuration20ms
		}
	}

	// SILK: 10, 20, 40, 60 ms
	switch index {
	case 0:
		return FrameDuration10ms
	case 1:
		return FrameDuration20ms
	case 2:
		return FrameDuration40ms
	default:
		return FrameDuration60ms
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
