package magnum

import "errors"

var (
	// ErrUnsupportedSampleRate is returned when the requested sample rate is not
	// supported. Callers may branch on this via errors.Is.
	ErrUnsupportedSampleRate = errors.New("unsupported sample rate: must be 8000, 16000, 24000, or 48000")

	// ErrUnsupportedChannelCount is returned when the channel count is not 1 or 2.
	// Callers may branch on this via errors.Is.
	ErrUnsupportedChannelCount = errors.New("unsupported channel count: must be 1 or 2")

	// ErrTooShortForTableOfContentsHeader is returned when a packet contains no
	// bytes at all (not even the TOC header byte).
	ErrTooShortForTableOfContentsHeader = errors.New("packet too short to contain table of contents header")

	// ErrInvalidFrameData is returned when the decompressed frame payload has an
	// unexpected length (e.g., an odd number of bytes for int16 samples).
	ErrInvalidFrameData = errors.New("invalid frame data: unexpected payload length")

	// ErrPayloadTooLarge is returned by Decode when the decompressed payload
	// exceeds the maximum allowed size, preventing memory exhaustion from
	// malformed or malicious packets.
	ErrPayloadTooLarge = errors.New("decompressed payload exceeds maximum allowed size")

	// ErrUnsupportedFrameCode is returned by Decode when the packet contains a
	// frame code other than frameCodeOneFrame (0). This encoder only produces
	// single-frame packets; multi-frame packets (codes 1, 2, 3) are not supported.
	ErrUnsupportedFrameCode = errors.New("unsupported frame code: only single-frame packets (code 0) are supported")

	// ErrChannelMismatch is returned by Decoder.Decode when the packet's stereo
	// flag does not match the decoder's configured channel count.
	ErrChannelMismatch = errors.New("packet channel configuration does not match decoder")

	// ErrSampleRateMismatch is returned by Decoder.Decode when the packet's TOC
	// configuration indicates a sample rate that does not match the decoder's
	// configured sample rate.
	ErrSampleRateMismatch = errors.New("packet sample rate configuration does not match decoder")
)
