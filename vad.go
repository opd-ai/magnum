// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements Voice Activity Detection (VAD) as used in RFC 6716
// for the SILK codec. VAD detects whether a frame contains speech or silence,
// enabling Discontinuous Transmission (DTX) to reduce bitrate during silent
// periods.
//
// Key components:
// - Energy-based activity detection
// - Spectral analysis for speech-like content
// - Temporal smoothing to avoid false transitions
// - Adaptive threshold adjustment

package magnum

import (
	"math"
)

// VAD state constants.
const (
	// VADStateInactive indicates the frame is silence/background noise.
	VADStateInactive = iota
	// VADStateActive indicates the frame contains speech/voice.
	VADStateActive
)

// VAD configuration constants.
const (
	// VADEnergyThresholdDB is the default energy threshold in dB.
	VADEnergyThresholdDB = -35.0
	// VADHangoverFrames is the number of frames to remain active after speech ends.
	VADHangoverFrames = 5
	// VADAttackFrames is the number of frames for activation smoothing.
	VADAttackFrames = 2
	// VADSNRThreshold is the signal-to-noise ratio threshold.
	VADSNRThreshold = 3.0
	// VADMinNoiseEnergy is the minimum noise floor estimate.
	VADMinNoiseEnergy = 1e-10
	// VADNoiseUpdateAlpha is the smoothing factor for noise floor update.
	VADNoiseUpdateAlpha = 0.95
)

// VADResult holds the result of voice activity detection.
type VADResult struct {
	// Active indicates if the frame contains voice activity.
	Active bool
	// Confidence is the detection confidence (0.0 to 1.0).
	Confidence float64
	// EnergyDB is the frame energy in dB.
	EnergyDB float64
	// SNR is the estimated signal-to-noise ratio.
	SNR float64
	// SpectralFlatness is a measure of spectral content (0=tonal, 1=noise).
	SpectralFlatness float64
}

// VAD implements Voice Activity Detection.
type VAD struct {
	// state is the current VAD state.
	state int
	// activeCount is consecutive active frame count.
	activeCount int
	// inactiveCount is consecutive inactive frame count.
	inactiveCount int
	// hangoverCount is the remaining hangover frames.
	hangoverCount int

	// noiseFloor is the adaptive noise floor estimate.
	noiseFloor float64
	// signalEnergy is the smoothed signal energy.
	signalEnergy float64

	// Configuration
	energyThresholdDB float64
	hangoverFrames    int
	snrThreshold      float64

	// sampleRate is the audio sample rate.
	sampleRate int
}

// NewVAD creates a new Voice Activity Detector.
func NewVAD(sampleRate int) *VAD {
	if sampleRate <= 0 {
		sampleRate = SampleRate16k
	}

	return &VAD{
		state:             VADStateInactive,
		activeCount:       0,
		inactiveCount:     0,
		hangoverCount:     0,
		noiseFloor:        VADMinNoiseEnergy,
		signalEnergy:      0,
		energyThresholdDB: VADEnergyThresholdDB,
		hangoverFrames:    VADHangoverFrames,
		snrThreshold:      VADSNRThreshold,
		sampleRate:        sampleRate,
	}
}

// Detect performs voice activity detection on a frame of audio.
func (vad *VAD) Detect(samples []float64) *VADResult {
	if len(samples) == 0 {
		return &VADResult{Active: false, Confidence: 0}
	}

	// Step 1: Compute frame energy
	energy := vad.computeEnergy(samples)
	energyDB := vad.energyToDB(energy)

	// Step 2: Update noise floor estimate
	vad.updateNoiseFloor(energy)

	// Step 3: Compute SNR
	snr := vad.computeSNR(energy)

	// Step 4: Compute spectral flatness
	spectralFlatness := vad.computeSpectralFlatness(samples)

	// Step 5: Make VAD decision
	rawActive := vad.makeDecision(energyDB, snr, spectralFlatness)

	// Step 6: Apply temporal smoothing
	smoothedActive := vad.applySmoothing(rawActive)

	// Compute confidence
	confidence := vad.computeConfidence(energyDB, snr, spectralFlatness)

	// Update state
	vad.signalEnergy = 0.9*vad.signalEnergy + 0.1*energy

	return &VADResult{
		Active:           smoothedActive,
		Confidence:       confidence,
		EnergyDB:         energyDB,
		SNR:              snr,
		SpectralFlatness: spectralFlatness,
	}
}

// computeEnergy computes the average energy of the samples.
func (vad *VAD) computeEnergy(samples []float64) float64 {
	energy := 0.0
	for _, s := range samples {
		energy += s * s
	}
	return energy / float64(len(samples))
}

// energyToDB converts linear energy to dB scale.
func (vad *VAD) energyToDB(energy float64) float64 {
	if energy < VADMinNoiseEnergy {
		return -100.0
	}
	return 10.0 * math.Log10(energy)
}

// updateNoiseFloor updates the adaptive noise floor estimate.
func (vad *VAD) updateNoiseFloor(energy float64) {
	// Only update noise floor during inactive periods
	if vad.state == VADStateInactive {
		if energy < vad.noiseFloor {
			// Fast attack for decreasing noise
			vad.noiseFloor = energy
		} else {
			// Slow adaptation for increasing noise
			vad.noiseFloor = VADNoiseUpdateAlpha*vad.noiseFloor +
				(1-VADNoiseUpdateAlpha)*energy
		}
	}

	// Ensure minimum noise floor
	if vad.noiseFloor < VADMinNoiseEnergy {
		vad.noiseFloor = VADMinNoiseEnergy
	}
}

// computeSNR computes the signal-to-noise ratio.
func (vad *VAD) computeSNR(energy float64) float64 {
	if vad.noiseFloor < VADMinNoiseEnergy {
		return 0
	}
	snrLinear := energy / vad.noiseFloor
	if snrLinear < 1 {
		return 0
	}
	return 10.0 * math.Log10(snrLinear)
}

// computeSpectralFlatness estimates the spectral flatness measure.
// Returns 0 for tonal signals, 1 for noise-like signals.
func (vad *VAD) computeSpectralFlatness(samples []float64) float64 {
	n := len(samples)
	if n < 8 {
		return 0.5
	}

	// Use a simple approximation: ratio of geometric to arithmetic mean
	// of absolute sample values (as proxy for spectral shape)

	// Compute absolute values
	sumAbs := 0.0
	sumLogAbs := 0.0
	validCount := 0

	for _, s := range samples {
		absVal := math.Abs(s)
		if absVal > 1e-10 {
			sumAbs += absVal
			sumLogAbs += math.Log(absVal)
			validCount++
		}
	}

	if validCount < n/4 {
		return 0.5 // Not enough data
	}

	arithmeticMean := sumAbs / float64(validCount)
	geometricMean := math.Exp(sumLogAbs / float64(validCount))

	if arithmeticMean < 1e-10 {
		return 0.5
	}

	flatness := geometricMean / arithmeticMean
	// Clamp to [0, 1]
	if flatness < 0 {
		flatness = 0
	} else if flatness > 1 {
		flatness = 1
	}

	return flatness
}

// makeDecision makes the raw VAD decision based on features.
func (vad *VAD) makeDecision(energyDB, snr, spectralFlatness float64) bool {
	// Energy threshold check
	if energyDB < vad.energyThresholdDB {
		return false
	}

	// SNR threshold check
	if snr < vad.snrThreshold {
		return false
	}

	// Spectral content check: speech typically has lower flatness than noise
	// But this is a soft criterion
	if spectralFlatness > 0.95 {
		// Very noise-like, might be silence/background
		return snr > vad.snrThreshold*2 // Require higher SNR
	}

	return true
}

// applySmoothing applies temporal smoothing to the VAD decision.
func (vad *VAD) applySmoothing(rawActive bool) bool {
	if rawActive {
		vad.activeCount++
		vad.inactiveCount = 0
		vad.hangoverCount = vad.hangoverFrames

		// Require multiple active frames to transition from inactive
		if vad.state == VADStateInactive {
			if vad.activeCount >= VADAttackFrames {
				vad.state = VADStateActive
			}
		} else {
			vad.state = VADStateActive
		}
	} else {
		vad.inactiveCount++

		// Apply hangover
		if vad.hangoverCount > 0 {
			vad.hangoverCount--
			vad.state = VADStateActive
		} else {
			vad.activeCount = 0
			vad.state = VADStateInactive
		}
	}

	return vad.state == VADStateActive
}

// computeConfidence computes the detection confidence.
func (vad *VAD) computeConfidence(energyDB, snr, spectralFlatness float64) float64 {
	// Confidence based on multiple factors

	// Energy contribution (0 to 0.4)
	energyConf := 0.0
	if energyDB > vad.energyThresholdDB {
		energyConf = math.Min((energyDB-vad.energyThresholdDB)/20.0, 1.0) * 0.4
	}

	// SNR contribution (0 to 0.4)
	snrConf := 0.0
	if snr > vad.snrThreshold {
		snrConf = math.Min((snr-vad.snrThreshold)/10.0, 1.0) * 0.4
	}

	// Spectral contribution (0 to 0.2)
	// Lower flatness = more tonal = higher confidence
	spectralConf := (1.0 - spectralFlatness) * 0.2

	return energyConf + snrConf + spectralConf
}

// IsActive returns true if the current state is active.
func (vad *VAD) IsActive() bool {
	return vad.state == VADStateActive
}

// State returns the current VAD state.
func (vad *VAD) State() int {
	return vad.state
}

// NoiseFloor returns the current noise floor estimate in linear scale.
func (vad *VAD) NoiseFloor() float64 {
	return vad.noiseFloor
}

// NoiseFloorDB returns the current noise floor estimate in dB.
func (vad *VAD) NoiseFloorDB() float64 {
	return vad.energyToDB(vad.noiseFloor)
}

// SetEnergyThreshold sets the energy threshold in dB.
func (vad *VAD) SetEnergyThreshold(thresholdDB float64) {
	vad.energyThresholdDB = thresholdDB
}

// SetHangoverFrames sets the hangover period in frames.
func (vad *VAD) SetHangoverFrames(frames int) {
	if frames >= 0 {
		vad.hangoverFrames = frames
	}
}

// SetSNRThreshold sets the SNR threshold in dB.
func (vad *VAD) SetSNRThreshold(thresholdDB float64) {
	vad.snrThreshold = thresholdDB
}

// Reset resets the VAD state.
func (vad *VAD) Reset() {
	vad.state = VADStateInactive
	vad.activeCount = 0
	vad.inactiveCount = 0
	vad.hangoverCount = 0
	vad.noiseFloor = VADMinNoiseEnergy
	vad.signalEnergy = 0
}

// SimpleVAD performs simple energy-based VAD without state.
// Returns true if the frame energy exceeds the threshold.
func SimpleVAD(samples []float64, thresholdDB float64) bool {
	if len(samples) == 0 {
		return false
	}

	energy := 0.0
	for _, s := range samples {
		energy += s * s
	}
	energy /= float64(len(samples))

	energyDB := -100.0
	if energy > VADMinNoiseEnergy {
		energyDB = 10.0 * math.Log10(energy)
	}

	return energyDB > thresholdDB
}

// ComputeFrameActivity returns an activity score for the frame (0.0 to 1.0).
func ComputeFrameActivity(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}

	// Compute RMS
	energy := 0.0
	for _, s := range samples {
		energy += s * s
	}
	rms := math.Sqrt(energy / float64(len(samples)))

	// Map RMS to activity score
	// Assuming typical speech RMS around 0.1-0.3
	activity := rms / 0.2
	if activity > 1.0 {
		activity = 1.0
	}

	return activity
}
