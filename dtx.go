// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements Discontinuous Transmission (DTX) as specified in
// RFC 6716. DTX works with Voice Activity Detection (VAD) to suppress
// encoding of silence frames, reducing bandwidth during silent periods.
//
// When DTX is enabled and a frame is detected as silence by VAD, the encoder
// returns nil instead of an encoded packet. The decoder uses Packet Loss
// Concealment (PLC) to synthesize comfort noise during these periods.

package magnum

// DTXState represents the state of DTX processing.
type DTXState int

const (
	// DTXStateActive indicates active speech is being transmitted.
	DTXStateActive DTXState = iota
	// DTXStateInactive indicates DTX is suppressing transmission.
	DTXStateInactive
	// DTXStateHangover indicates the transition period after speech ends.
	DTXStateHangover
)

// DTXConfig holds configuration for Discontinuous Transmission.
type DTXConfig struct {
	// Enabled indicates whether DTX is active.
	Enabled bool
	// HangoverFrames is the number of frames to continue transmitting
	// after VAD detects end of speech. This prevents abrupt cutoffs.
	HangoverFrames int
	// MinActiveFrames is the minimum number of consecutive active frames
	// required before entering DTX inactive state. This prevents false
	// triggering on brief pauses in speech.
	MinActiveFrames int
}

// DefaultDTXConfig returns the default DTX configuration.
func DefaultDTXConfig() DTXConfig {
	return DTXConfig{
		Enabled:         false, // DTX disabled by default for backward compatibility
		HangoverFrames:  5,     // 100ms at 20ms frame duration
		MinActiveFrames: 2,     // Require 2 frames of activity before suppressing
	}
}

// DTX implements Discontinuous Transmission for bandwidth reduction
// during silence periods.
type DTX struct {
	config DTXConfig
	state  DTXState
	vad    *VAD

	// hangoverRemaining is the number of hangover frames left.
	hangoverRemaining int
	// activeFrameCount tracks consecutive active frames.
	activeFrameCount int
	// inactiveFrameCount tracks consecutive inactive frames.
	inactiveFrameCount int
	// totalSuppressedFrames counts frames suppressed by DTX.
	totalSuppressedFrames int
	// totalTransmittedFrames counts frames transmitted.
	totalTransmittedFrames int
}

// NewDTX creates a new DTX processor with the given configuration.
func NewDTX(sampleRate int, config DTXConfig) *DTX {
	return &DTX{
		config:                 config,
		state:                  DTXStateActive,
		vad:                    NewVAD(sampleRate),
		hangoverRemaining:      0,
		activeFrameCount:       0,
		inactiveFrameCount:     0,
		totalSuppressedFrames:  0,
		totalTransmittedFrames: 0,
	}
}

// DTXDecision represents the decision made by DTX for a frame.
type DTXDecision struct {
	// Transmit indicates whether the frame should be encoded and transmitted.
	Transmit bool
	// State is the current DTX state.
	State DTXState
	// VADResult contains the underlying VAD analysis.
	VADResult *VADResult
}

// Process analyzes a frame and determines whether it should be transmitted.
// The samples parameter should be float64 PCM samples in the range [-1, 1].
func (d *DTX) Process(samples []float64) *DTXDecision {
	if !d.config.Enabled {
		// DTX disabled - always transmit but still track stats
		d.totalTransmittedFrames++
		return &DTXDecision{
			Transmit:  true,
			State:     DTXStateActive,
			VADResult: nil,
		}
	}

	// Run VAD on the frame
	vadResult := d.vad.Detect(samples)

	// Update frame counters
	if vadResult.Active {
		d.activeFrameCount++
		d.inactiveFrameCount = 0
	} else {
		d.inactiveFrameCount++
		d.activeFrameCount = 0
	}

	// State machine for DTX
	transmit := true
	switch d.state {
	case DTXStateActive:
		if !vadResult.Active {
			// Speech ended - enter hangover
			d.state = DTXStateHangover
			d.hangoverRemaining = d.config.HangoverFrames
			transmit = true // Continue transmitting during hangover
		}

	case DTXStateHangover:
		if vadResult.Active {
			// Speech resumed - back to active
			d.state = DTXStateActive
			transmit = true
		} else if d.hangoverRemaining > 0 {
			// Still in hangover period
			d.hangoverRemaining--
			transmit = true
		} else {
			// Hangover expired - enter inactive
			d.state = DTXStateInactive
			transmit = false
		}

	case DTXStateInactive:
		if vadResult.Active && d.activeFrameCount >= d.config.MinActiveFrames {
			// Sufficient speech activity detected - return to active
			d.state = DTXStateActive
			transmit = true
		} else if vadResult.Active {
			// Activity detected but not enough consecutive frames yet
			// Transmit to avoid losing speech onset
			transmit = true
		} else if vadResult.EnergyDB > d.vad.NoiseFloorDB()+10 {
			// Not yet detected by VAD smoothing, but energy is significantly
			// above noise floor - this might be speech onset. Transmit to
			// avoid clipping the beginning of speech.
			transmit = true
		} else {
			// Still inactive - suppress transmission
			transmit = false
		}
	}

	// Update statistics
	if transmit {
		d.totalTransmittedFrames++
	} else {
		d.totalSuppressedFrames++
	}

	return &DTXDecision{
		Transmit:  transmit,
		State:     d.state,
		VADResult: vadResult,
	}
}

// ProcessInt16 is a convenience method that accepts int16 PCM samples.
// Samples are converted to float64 internally.
func (d *DTX) ProcessInt16(samples []int16) *DTXDecision {
	floatSamples := make([]float64, len(samples))
	for i, s := range samples {
		floatSamples[i] = float64(s) / 32768.0
	}
	return d.Process(floatSamples)
}

// State returns the current DTX state.
func (d *DTX) State() DTXState {
	return d.state
}

// IsTransmitting returns true if DTX is currently transmitting frames.
func (d *DTX) IsTransmitting() bool {
	return d.state != DTXStateInactive
}

// Stats returns DTX statistics.
func (d *DTX) Stats() (transmitted, suppressed int) {
	return d.totalTransmittedFrames, d.totalSuppressedFrames
}

// SuppressionRatio returns the ratio of suppressed frames to total frames.
// Returns 0 if no frames have been processed.
func (d *DTX) SuppressionRatio() float64 {
	total := d.totalTransmittedFrames + d.totalSuppressedFrames
	if total == 0 {
		return 0
	}
	return float64(d.totalSuppressedFrames) / float64(total)
}

// Reset resets the DTX state.
func (d *DTX) Reset() {
	d.state = DTXStateActive
	d.hangoverRemaining = 0
	d.activeFrameCount = 0
	d.inactiveFrameCount = 0
	d.totalSuppressedFrames = 0
	d.totalTransmittedFrames = 0
	d.vad.Reset()
}

// SetConfig updates the DTX configuration.
func (d *DTX) SetConfig(config DTXConfig) {
	d.config = config
}

// Config returns the current DTX configuration.
func (d *DTX) Config() DTXConfig {
	return d.config
}

// SetEnabled enables or disables DTX.
func (d *DTX) SetEnabled(enabled bool) {
	d.config.Enabled = enabled
}

// IsEnabled returns true if DTX is enabled.
func (d *DTX) IsEnabled() bool {
	return d.config.Enabled
}

// SetHangoverFrames sets the hangover period in frames.
func (d *DTX) SetHangoverFrames(frames int) {
	if frames >= 0 {
		d.config.HangoverFrames = frames
	}
}

// VAD returns the underlying VAD instance for direct configuration.
func (d *DTX) VAD() *VAD {
	return d.vad
}
