// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements Packet Loss Concealment (PLC) as specified in RFC 6716
// §4.4 for the SILK codec. When packets are lost, the decoder uses information
// from previous frames to extrapolate plausible audio.
//
// Key components:
// - PLC state machine tracking lost/received packets
// - LPC-based audio extrapolation
// - Pitch-periodic repetition for voiced frames
// - Attenuation to fade out during extended loss
// - Smooth transition back when packets resume

package magnum

import (
	"math"
)

// PLC state constants.
const (
	// PLCStateNormal indicates no packet loss.
	PLCStateNormal = iota
	// PLCStateLost indicates one or more packets have been lost.
	PLCStateLost
	// PLCStateRecovery indicates transitioning back from loss.
	PLCStateRecovery
)

// PLC attenuation constants.
const (
	// PLCMaxLostFrames is the maximum frames to conceal before muting.
	PLCMaxLostFrames = 10
	// PLCAttenuationDB is the attenuation per lost frame in dB.
	PLCAttenuationDB = 2.0
	// PLCRecoveryFrames is the number of frames for smooth transition.
	PLCRecoveryFrames = 2
	// PLCMinPitch is the minimum pitch period for repetition.
	PLCMinPitch = 16
	// PLCMaxPitch is the maximum pitch period for repetition.
	PLCMaxPitch = 1024
)

// PLCState holds the state for packet loss concealment.
type PLCState struct {
	// state is the current PLC state (Normal, Lost, Recovery).
	state int
	// lostCount is the consecutive lost packet count.
	lostCount int
	// recoveryCount is the frames since returning from loss.
	recoveryCount int

	// lastGoodFrame holds data from the last successfully decoded frame.
	lastGoodFrame *PLCFrameData
	// prevSamples holds previous output samples for overlap-add.
	prevSamples []float64

	// sampleRate is the decoder sample rate.
	sampleRate int
	// frameSize is samples per frame.
	frameSize int
	// channels is the number of audio channels.
	channels int

	// Random state for noise generation during PLC
	randSeed uint32
}

// PLCFrameData stores data needed for PLC from a successfully decoded frame.
type PLCFrameData struct {
	// LPCCoeffs are the LPC coefficients from the last frame.
	LPCCoeffs []float64
	// PitchLag is the pitch period from the last frame.
	PitchLag int
	// PitchGain is the pitch correlation/gain from the last frame.
	PitchGain float64
	// Voiced indicates if the last frame was voiced.
	Voiced bool
	// Gain is the frame gain (energy level).
	Gain float64
	// Samples holds the decoded samples (for pitch repetition).
	Samples []float64
	// Excitation holds the excitation signal.
	Excitation []float64
}

// NewPLCState creates a new PLC state.
func NewPLCState(sampleRate, frameSize, channels int) *PLCState {
	if sampleRate <= 0 {
		sampleRate = SampleRate16k
	}
	if frameSize <= 0 {
		frameSize = sampleRate / 50 // 20ms default
	}
	if channels <= 0 {
		channels = 1
	}

	return &PLCState{
		state:         PLCStateNormal,
		lostCount:     0,
		recoveryCount: 0,
		sampleRate:    sampleRate,
		frameSize:     frameSize,
		channels:      channels,
		prevSamples:   make([]float64, frameSize),
		randSeed:      12345,
	}
}

// PacketReceived should be called when a good packet is received.
// It updates PLC state and stores frame data for future concealment.
func (plc *PLCState) PacketReceived(frameData *PLCFrameData) {
	// Transition state
	switch plc.state {
	case PLCStateLost:
		plc.state = PLCStateRecovery
		plc.recoveryCount = 0
	case PLCStateRecovery:
		plc.recoveryCount++
		if plc.recoveryCount >= PLCRecoveryFrames {
			plc.state = PLCStateNormal
		}
	default:
		plc.state = PLCStateNormal
	}

	plc.lostCount = 0

	// Store frame data for future PLC
	if frameData != nil {
		plc.lastGoodFrame = frameData
	}
}

// PacketLost should be called when a packet is detected as lost.
// Returns synthesized audio to fill the gap.
func (plc *PLCState) PacketLost() []float64 {
	plc.state = PLCStateLost
	plc.lostCount++

	// Generate concealment audio
	return plc.generateConcealment()
}

// generateConcealment synthesizes audio for a lost packet.
func (plc *PLCState) generateConcealment() []float64 {
	output := make([]float64, plc.frameSize*plc.channels)

	// If we have no previous data, output silence
	if plc.lastGoodFrame == nil {
		return output
	}

	// Compute attenuation based on loss count
	attenuation := plc.computeAttenuation()
	if attenuation < 0.001 {
		return output // Effectively muted
	}

	// Choose concealment method based on voicing
	if plc.lastGoodFrame.Voiced && plc.lastGoodFrame.PitchLag > PLCMinPitch {
		// Voiced: repeat pitch periods with attenuation
		output = plc.generateVoicedPLC(attenuation)
	} else {
		// Unvoiced: generate noise-like signal with LPC shaping
		output = plc.generateUnvoicedPLC(attenuation)
	}

	// Store output for next iteration
	copy(plc.prevSamples, output)

	return output
}

// computeAttenuation returns the attenuation factor (0-1) based on loss count.
func (plc *PLCState) computeAttenuation() float64 {
	if plc.lostCount >= PLCMaxLostFrames {
		return 0 // Muted after too many losses
	}

	// Linear attenuation in dB domain
	attenuationDB := float64(plc.lostCount) * PLCAttenuationDB
	return math.Pow(10, -attenuationDB/20.0)
}

// generateVoicedPLC generates concealment for voiced frames using pitch repetition.
func (plc *PLCState) generateVoicedPLC(attenuation float64) []float64 {
	output := make([]float64, plc.frameSize)

	lastFrame := plc.lastGoodFrame
	pitchLag := lastFrame.PitchLag
	if pitchLag < PLCMinPitch {
		pitchLag = PLCMinPitch
	} else if pitchLag > PLCMaxPitch {
		pitchLag = PLCMaxPitch
	}

	// Get source samples (from previous frame)
	var srcSamples []float64
	if len(lastFrame.Samples) > 0 {
		srcSamples = lastFrame.Samples
	} else {
		srcSamples = plc.prevSamples
	}

	if len(srcSamples) == 0 {
		return output
	}

	// Pitch-synchronous repetition
	srcLen := len(srcSamples)
	for i := 0; i < plc.frameSize; i++ {
		// Compute source position: wrap around at pitch period
		srcIdx := i % srcLen

		// Apply pitch-periodic continuation
		if srcIdx < srcLen-pitchLag && pitchLag < srcLen {
			// Blend current with pitch-lagged sample
			blend := 0.7 // Blend factor
			output[i] = blend*srcSamples[srcIdx] + (1-blend)*srcSamples[srcIdx+pitchLag]
		} else {
			output[i] = srcSamples[srcIdx]
		}

		// Apply gain from last frame
		output[i] *= lastFrame.Gain

		// Apply attenuation
		output[i] *= attenuation
	}

	// Apply slight random jitter to reduce "robotic" artifacts
	for i := 0; i < plc.frameSize; i++ {
		jitter := plc.nextRandom() * 0.02 // ±1% jitter
		output[i] *= (1.0 + jitter)
	}

	return output
}

// generateUnvoicedPLC generates concealment for unvoiced frames using shaped noise.
func (plc *PLCState) generateUnvoicedPLC(attenuation float64) []float64 {
	output := make([]float64, plc.frameSize)

	lastFrame := plc.lastGoodFrame

	// Generate white noise
	noise := make([]float64, plc.frameSize)
	for i := range noise {
		noise[i] = plc.nextRandom() * 2.0 // Range [-1, 1]
	}

	// If we have LPC coefficients, shape the noise
	if len(lastFrame.LPCCoeffs) > 0 {
		output = SynthesizeLPC(noise, lastFrame.LPCCoeffs)
	} else {
		copy(output, noise)
	}

	// Apply gain and attenuation
	gain := lastFrame.Gain * attenuation
	for i := range output {
		output[i] *= gain
	}

	return output
}

// nextRandom generates a pseudo-random value in [-1, 1].
func (plc *PLCState) nextRandom() float64 {
	// Simple LCG for pseudo-random generation
	plc.randSeed = plc.randSeed*1103515245 + 12345
	// Map to [-1, 1]
	return float64(int32(plc.randSeed>>16))/32768.0 - 1.0
}

// ApplyTransition applies smooth transition when recovering from loss.
// Call this on the first good frame after packet loss.
func (plc *PLCState) ApplyTransition(decoded []float64) []float64 {
	if plc.state != PLCStateRecovery || len(plc.prevSamples) == 0 {
		return decoded
	}

	n := len(decoded)
	if n == 0 {
		return decoded
	}

	// Cross-fade between PLC output and decoded frame
	output := make([]float64, n)

	// Transition length: 5ms or quarter frame, whichever is smaller
	transitionLen := plc.sampleRate / 200 // 5ms
	if transitionLen > n/4 {
		transitionLen = n / 4
	}
	if transitionLen < 1 {
		transitionLen = 1
	}

	for i := 0; i < n; i++ {
		if i < transitionLen {
			// Cross-fade region
			alpha := float64(i) / float64(transitionLen) // 0 -> 1
			prevIdx := i
			if prevIdx >= len(plc.prevSamples) {
				prevIdx = len(plc.prevSamples) - 1
			}
			if prevIdx < 0 {
				prevIdx = 0
			}
			output[i] = (1-alpha)*plc.prevSamples[prevIdx] + alpha*decoded[i]
		} else {
			// Past transition: use decoded directly
			output[i] = decoded[i]
		}
	}

	return output
}

// State returns the current PLC state.
func (plc *PLCState) State() int {
	return plc.state
}

// LostCount returns the consecutive lost packet count.
func (plc *PLCState) LostCount() int {
	return plc.lostCount
}

// IsRecovering returns true if in recovery from packet loss.
func (plc *PLCState) IsRecovering() bool {
	return plc.state == PLCStateRecovery
}

// Reset resets the PLC state machine.
func (plc *PLCState) Reset() {
	plc.state = PLCStateNormal
	plc.lostCount = 0
	plc.recoveryCount = 0
	plc.lastGoodFrame = nil
	plc.randSeed = 12345

	for i := range plc.prevSamples {
		plc.prevSamples[i] = 0
	}
}

// UpdateFromDecoder updates PLC state from decoder results.
// This should be called after each successful decode.
func (plc *PLCState) UpdateFromDecoder(samples, lpcCoeffs []float64,
	pitchLag int, pitchGain float64, voiced bool, gain float64,
) {
	frameData := &PLCFrameData{
		Voiced:    voiced,
		PitchLag:  pitchLag,
		PitchGain: pitchGain,
		Gain:      gain,
	}

	// Copy LPC coefficients
	if len(lpcCoeffs) > 0 {
		frameData.LPCCoeffs = make([]float64, len(lpcCoeffs))
		copy(frameData.LPCCoeffs, lpcCoeffs)
	}

	// Store samples for pitch repetition
	if len(samples) > 0 {
		frameData.Samples = make([]float64, len(samples))
		copy(frameData.Samples, samples)
	}

	plc.PacketReceived(frameData)
}

// ComputeFrameGain computes the gain (RMS energy) of a frame.
func ComputeFrameGain(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}

	energy := 0.0
	for _, s := range samples {
		energy += s * s
	}

	return math.Sqrt(energy / float64(len(samples)))
}

// DetectVoicing detects if a frame is voiced based on autocorrelation.
func DetectVoicing(samples []float64, minLag, maxLag int) (bool, int, float64) {
	if len(samples) < maxLag*2 {
		return false, 0, 0
	}

	// Compute normalized autocorrelation
	bestLag := minLag
	bestCorr := 0.0

	// Compute energy
	energy := 0.0
	for _, s := range samples {
		energy += s * s
	}
	if energy < 1e-10 {
		return false, 0, 0
	}

	for lag := minLag; lag <= maxLag; lag++ {
		corr := 0.0
		for i := lag; i < len(samples); i++ {
			corr += samples[i] * samples[i-lag]
		}
		normCorr := corr / energy

		if normCorr > bestCorr {
			bestCorr = normCorr
			bestLag = lag
		}
	}

	// Voicing threshold
	voiced := bestCorr > 0.3

	return voiced, bestLag, bestCorr
}
