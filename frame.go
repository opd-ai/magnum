package magnum

// frameDurationMs is the standard Opus frame duration used by this encoder, in
// milliseconds.
const frameDurationMs = 20

// frameBuffer accumulates interleaved PCM samples and emits complete audio
// frames one at a time via next.
//
// A single frame contains sampleRate * frameDurationMs / 1000 * channels
// int16 samples (all channels interleaved).
type frameBuffer struct {
	// samples holds the partial frame currently being filled.
	// Its capacity is always exactly frameSize so the backing array never
	// grows beyond one frame.
	samples []int16
	// ready holds complete frames waiting to be consumed by next.
	ready [][]int16
	// frameSize is the total number of int16 samples per frame (all channels).
	frameSize int
}

// newFrameBuffer creates a new frameBuffer sized for one 20 ms frame at the
// given sample rate and channel count. frameSize equals
// sampleRate * frameDurationMs / 1000 * channels.
func newFrameBuffer(sampleRate, channels int) *frameBuffer {
	frameSize := sampleRate * frameDurationMs / 1000 * channels
	return &frameBuffer{
		samples:   make([]int16, 0, frameSize),
		ready:     make([][]int16, 0, 4), // pre-allocate for typical streaming use
		frameSize: frameSize,
	}
}

// write appends samples to the internal buffer. It processes input in
// frame-sized chunks so that the partial-frame backing array never exceeds
// frameSize samples. Completed frames are placed in the ready queue and
// retrieved via next.
func (fb *frameBuffer) write(samples []int16) {
	for len(samples) > 0 {
		// How many samples are needed to complete the current partial frame.
		space := fb.frameSize - len(fb.samples)
		if space > len(samples) {
			// Not enough to complete a frame; buffer the remainder.
			fb.samples = append(fb.samples, samples...)
			return
		}

		// Fill exactly one frame.
		fb.samples = append(fb.samples, samples[:space]...)
		samples = samples[space:]

		// Move the completed frame to the ready queue.
		frame := make([]int16, fb.frameSize)
		copy(frame, fb.samples)
		fb.ready = append(fb.ready, frame)

		// Reset the partial buffer without reallocating.
		fb.samples = fb.samples[:0]
	}
}

// next removes and returns the oldest complete frame from the ready queue.
// Returns nil when no complete frame is available.
func (fb *frameBuffer) next() []int16 {
	if len(fb.ready) == 0 {
		return nil
	}
	frame := fb.ready[0]
	fb.ready = fb.ready[1:]
	return frame
}

// buffered returns the number of samples in the partial frame currently being
// filled (not counting samples already queued in the ready list).
func (fb *frameBuffer) buffered() int {
	return len(fb.samples)
}

// flush returns the partial frame as a zero-padded complete frame and clears
// the partial buffer. Returns nil when the partial buffer is empty.
// Complete frames already in the ready queue are not affected; drain those
// first with next.
func (fb *frameBuffer) flush() []int16 {
	if len(fb.samples) == 0 {
		return nil
	}
	frame := make([]int16, fb.frameSize)
	copy(frame, fb.samples)
	fb.samples = fb.samples[:0]
	return frame
}
