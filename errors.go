package magnum

import "errors"

var (
	// errUnsupportedSampleRate is returned when the requested sample rate is not supported.
	errUnsupportedSampleRate = errors.New("unsupported sample rate: must be 8000, 16000, 24000, or 48000")

	// errUnsupportedChannelCount is returned when the channel count is not 1 or 2.
	errUnsupportedChannelCount = errors.New("unsupported channel count: must be 1 or 2")

	// errTooShortForTableOfContentsHeader is returned when a packet is too short to contain a TOC header.
	errTooShortForTableOfContentsHeader = errors.New("packet too short to contain table of contents header")

	// errInvalidFrameData is returned when the frame data has an unexpected format.
	errInvalidFrameData = errors.New("invalid frame data: unexpected payload length")
)
