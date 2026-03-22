package magnum

// frameDurationMs is the standard Opus frame duration used by this encoder, in
// milliseconds.
const frameDurationMs = 20

// frameBuffer accumulates PCM samples and returns complete audio frames.
// A frame is exactly sampleRate * frameDurationMs / 1000 samples.
type frameBuffer struct {
	samples   []int16
	frameSize int
}

// newFrameBuffer creates a new frameBuffer sized for one 20 ms frame at the
// given sample rate.
func newFrameBuffer(sampleRate int) *frameBuffer {
	frameSize := sampleRate * frameDurationMs / 1000
	return &frameBuffer{
		samples:   make([]int16, 0, frameSize),
		frameSize: frameSize,
	}
}

// write appends samples to the internal buffer and returns all complete frames
// that have been accumulated. Leftover samples are retained for the next call.
func (fb *frameBuffer) write(samples []int16) [][]int16 {
	fb.samples = append(fb.samples, samples...)

	var frames [][]int16
	for len(fb.samples) >= fb.frameSize {
		frame := make([]int16, fb.frameSize)
		copy(frame, fb.samples[:fb.frameSize])
		frames = append(frames, frame)
		// Remove the consumed samples without reallocating the backing array.
		fb.samples = fb.samples[fb.frameSize:]
	}
	return frames
}

// buffered returns the number of samples currently held in the buffer.
func (fb *frameBuffer) buffered() int {
	return len(fb.samples)
}

// flush returns the remaining samples as a zero-padded complete frame. Returns
// nil when the buffer is empty.
func (fb *frameBuffer) flush() []int16 {
	if len(fb.samples) == 0 {
		return nil
	}
	frame := make([]int16, fb.frameSize)
	copy(frame, fb.samples)
	fb.samples = fb.samples[:0]
	return frame
}
