// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements the CELT energy quantization as specified in RFC 6716 §4.3.
// Energy quantization encodes the spectral envelope in two stages:
//   1. Coarse quantization: ~6 dB resolution with prediction and entropy coding
//   2. Fine quantization: Additional bits for improved accuracy
//
// The quantizers use inter-frame and intra-band prediction to exploit
// correlations in audio signals, reducing the bitrate needed for the envelope.

package magnum

import (
	"math"
)

// Energy quantization constants from RFC 6716
const (
	// coarseQuantStep is the coarse quantization step size in dB
	coarseQuantStep = 6.0

	// fineQuantMaxBits is the maximum number of fine quantization bits per band
	fineQuantMaxBits = 8

	// energyFloorDB is the minimum energy floor in dB
	energyFloorDB = -28.0

	// maxDecayDB is the maximum energy decay per frame in dB
	maxDecayDB = 16.0
)

// Prediction coefficients from RFC 6716 §4.3.2
// These control inter-frame and intra-band prediction strength.
// Values are indexed by LM (log2 of frame size ratio: 0=2.5ms, 1=5ms, 2=10ms, 3=20ms)
var (
	// predCoef is the inter-frame prediction coefficient (alpha)
	// Higher values = stronger prediction from previous frame
	predCoef = [4]float64{
		29440.0 / 32768.0, // ~0.9 for 2.5ms
		26112.0 / 32768.0, // ~0.8 for 5ms
		21248.0 / 32768.0, // ~0.65 for 10ms
		16384.0 / 32768.0, // ~0.5 for 20ms
	}

	// betaCoef is the intra-band prediction coefficient (beta)
	// Controls how much the prediction adapts based on error
	betaCoef = [4]float64{
		30147.0 / 32768.0,
		22282.0 / 32768.0,
		12124.0 / 32768.0,
		6554.0 / 32768.0,
	}

	// betaIntra is the beta coefficient for intra frames (no inter-frame prediction)
	betaIntra = 4915.0 / 32768.0
)

// Mean energy values for each band (Q4 values converted to dB)
// From RFC 6716 Table 61 (simplified for 21 bands)
var eMeans = [NumCELTBands]float64{
	6.4375, 6.25, 5.75, 5.3125, 5.0625,
	4.8125, 4.5, 4.375, 4.875, 4.6875,
	4.5625, 4.4375, 4.875, 4.625, 4.3125,
	4.5, 4.375, 4.625, 4.75, 4.4375,
	3.75,
}

// EnergyQuantizer handles the quantization and dequantization of band energies.
// It maintains state for inter-frame prediction.
type EnergyQuantizer struct {
	// oldEnergies stores the previous frame's quantized energies for prediction
	oldEnergies [NumCELTBands]float64

	// lm is the log2 of frame size ratio (0=2.5ms, 1=5ms, 2=10ms, 3=20ms)
	lm int

	// hasHistory indicates if we have valid previous frame data
	hasHistory bool
}

// NewEnergyQuantizer creates a new energy quantizer for the given frame size.
// lm is the log2 of the frame size ratio:
//   - 0: 2.5ms (120 samples at 48kHz)
//   - 1: 5ms (240 samples)
//   - 2: 10ms (480 samples)
//   - 3: 20ms (960 samples)
func NewEnergyQuantizer(lm int) *EnergyQuantizer {
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	eq := &EnergyQuantizer{
		lm:         lm,
		hasHistory: false,
	}

	// Initialize old energies to mean values
	for i := 0; i < NumCELTBands; i++ {
		eq.oldEnergies[i] = eMeans[i]
	}

	return eq
}

// Reset clears the quantizer state, forcing intra-frame coding for the next frame.
func (eq *EnergyQuantizer) Reset() {
	eq.hasHistory = false
	for i := 0; i < NumCELTBands; i++ {
		eq.oldEnergies[i] = eMeans[i]
	}
}

// QuantizedEnergy represents the quantized energy values for a frame.
type QuantizedEnergy struct {
	// CoarseQuant holds the coarse quantization indices for each band
	CoarseQuant [NumCELTBands]int

	// FineQuant holds the fine quantization values for each band
	FineQuant [NumCELTBands]int

	// FineBits holds the number of fine quantization bits used per band
	FineBits [NumCELTBands]int

	// Intra indicates if intra-frame coding was used (no inter-frame prediction)
	Intra bool

	// ReconstructedEnergy holds the reconstructed dB energy after quantization
	ReconstructedEnergy [NumCELTBands]float64

	// QuantizationError holds the residual error after fine quantization
	QuantizationError [NumCELTBands]float64
}

// QuantizeCoarse performs coarse energy quantization.
//
// logEnergies contains the log-domain (dB) energy for each band.
// intra forces intra-frame coding (no inter-frame prediction).
//
// Returns the quantized energy structure with coarse quantization indices.
func (eq *EnergyQuantizer) QuantizeCoarse(logEnergies [NumCELTBands]float64, intra bool) *QuantizedEnergy {
	result := &QuantizedEnergy{
		Intra: intra || !eq.hasHistory,
	}

	// Select prediction coefficients
	var alpha, beta float64
	if result.Intra {
		alpha = 0
		beta = betaIntra
	} else {
		alpha = predCoef[eq.lm]
		beta = betaCoef[eq.lm]
	}

	// Prediction accumulator (across bands within this frame)
	prev := 0.0

	for band := 0; band < NumCELTBands; band++ {
		// Get current energy (clamp to floor)
		x := logEnergies[band]
		if x < energyFloorDB {
			x = energyFloorDB
		}

		// Get previous frame's energy for this band
		oldE := eq.oldEnergies[band]
		if oldE < -9.0 {
			oldE = -9.0
		}

		// Compute predicted energy using inter-frame and intra-band prediction
		predicted := alpha*oldE + prev

		// Compute prediction residual
		residual := x - predicted

		// Quantize residual to coarse step (6 dB)
		qi := int(math.Round(residual / coarseQuantStep))

		// Apply decay bound to prevent energy from dropping too fast
		decayBound := oldE - maxDecayDB
		if qi < 0 && x < decayBound {
			adjustment := int((decayBound - x) / coarseQuantStep)
			qi += adjustment
			if qi > 0 {
				qi = 0
			}
		}

		result.CoarseQuant[band] = qi

		// Compute reconstructed energy
		quantizedResidual := float64(qi) * coarseQuantStep
		reconstructed := predicted + quantizedResidual

		// Clamp reconstructed energy
		if reconstructed < energyFloorDB {
			reconstructed = energyFloorDB
		}

		result.ReconstructedEnergy[band] = reconstructed
		result.QuantizationError[band] = x - reconstructed

		// Update prediction state
		prev = prev + quantizedResidual - beta*quantizedResidual

		// Update old energies for next frame
		eq.oldEnergies[band] = reconstructed
	}

	eq.hasHistory = true
	return result
}

// QuantizeFine performs fine energy quantization to refine the coarse values.
//
// qe is the result from QuantizeCoarse.
// totalFineBits is the total number of fine bits to allocate across all bands.
//
// The function allocates bits proportionally to bands with larger errors.
func (eq *EnergyQuantizer) QuantizeFine(qe *QuantizedEnergy, totalFineBits int) {
	if totalFineBits <= 0 {
		return
	}

	// Simple bit allocation: distribute bits to bands with largest errors
	// In a full implementation, this would use the RFC 6716 bit allocation tables

	// Calculate error magnitude for each band
	type bandError struct {
		band int
		err  float64
	}
	errors := make([]bandError, NumCELTBands)
	for i := 0; i < NumCELTBands; i++ {
		errors[i] = bandError{band: i, err: math.Abs(qe.QuantizationError[i])}
	}

	// Allocate bits greedily to bands with largest errors
	remainingBits := totalFineBits
	for remainingBits > 0 {
		// Find band with largest error that hasn't reached max bits
		maxErr := -1.0
		maxBand := -1
		for i := 0; i < NumCELTBands; i++ {
			if qe.FineBits[i] < fineQuantMaxBits && errors[i].err > maxErr {
				maxErr = errors[i].err
				maxBand = i
			}
		}

		if maxBand < 0 {
			break // All bands at max bits
		}

		// Add one bit to this band
		qe.FineBits[maxBand]++
		remainingBits--

		// Update error estimate (halved with each additional bit)
		errors[maxBand].err /= 2
	}

	// Quantize fine values for bands that got bits
	for band := 0; band < NumCELTBands; band++ {
		bits := qe.FineBits[band]
		if bits <= 0 {
			continue
		}

		// Fine quantization step size
		levels := 1 << bits
		stepSize := coarseQuantStep / float64(levels)

		// Quantize the error
		err := qe.QuantizationError[band]
		qi := int(math.Round(err/stepSize + float64(levels)/2))

		// Clamp to valid range
		if qi < 0 {
			qi = 0
		}
		if qi >= levels {
			qi = levels - 1
		}

		qe.FineQuant[band] = qi

		// Update reconstructed energy
		offset := (float64(qi)+0.5)/float64(levels)*coarseQuantStep - coarseQuantStep/2
		qe.ReconstructedEnergy[band] += offset
		qe.QuantizationError[band] -= offset

		// Also update the quantizer state
		eq.oldEnergies[band] += offset
	}
}

// Dequantize reconstructs the energy values from quantized indices.
//
// This is the decoder-side operation that mirrors QuantizeCoarse and QuantizeFine.
func (eq *EnergyQuantizer) Dequantize(coarseQuant, fineQuant, fineBits [NumCELTBands]int, intra bool) [NumCELTBands]float64 {
	var result [NumCELTBands]float64

	// Select prediction coefficients
	var alpha, beta float64
	if intra || !eq.hasHistory {
		alpha = 0
		beta = betaIntra
	} else {
		alpha = predCoef[eq.lm]
		beta = betaCoef[eq.lm]
	}

	prev := 0.0

	for band := 0; band < NumCELTBands; band++ {
		// Get previous frame's energy for this band
		oldE := eq.oldEnergies[band]
		if oldE < -9.0 {
			oldE = -9.0
		}

		// Compute predicted energy
		predicted := alpha*oldE + prev

		// Dequantize coarse value
		quantizedResidual := float64(coarseQuant[band]) * coarseQuantStep
		reconstructed := predicted + quantizedResidual

		// Add fine quantization if present
		if fineBits[band] > 0 {
			levels := 1 << fineBits[band]
			offset := (float64(fineQuant[band])+0.5)/float64(levels)*coarseQuantStep - coarseQuantStep/2
			reconstructed += offset
		}

		// Clamp to floor
		if reconstructed < energyFloorDB {
			reconstructed = energyFloorDB
		}

		result[band] = reconstructed

		// Update prediction state
		prev = prev + quantizedResidual - beta*quantizedResidual

		// Update old energies for next frame
		eq.oldEnergies[band] = reconstructed
	}

	eq.hasHistory = true
	return result
}

// EstimateCoarseBits estimates the number of bits needed for coarse quantization.
// This is useful for rate control and bit allocation.
func EstimateCoarseBits(qe *QuantizedEnergy) int {
	bits := 0

	// Intra flag
	bits += 1

	// Estimate bits per band (simplified)
	// In a full implementation, this would use the Laplace entropy model
	for band := 0; band < NumCELTBands; band++ {
		qi := qe.CoarseQuant[band]
		absQi := qi
		if absQi < 0 {
			absQi = -absQi
		}

		// Rough estimate: 2 bits for sign + magnitude coding
		if absQi == 0 {
			bits += 1
		} else {
			bits += 2 + int(math.Log2(float64(absQi)+1))
		}
	}

	return bits
}

// ComputeTotalFineBits calculates the number of fine bits for a given bit budget.
func ComputeTotalFineBits(budget, coarseBits int) int {
	remaining := budget - coarseBits
	if remaining < 0 {
		return 0
	}
	// Reserve some bits for spectral shape coding
	fineBits := remaining / 2
	if fineBits > NumCELTBands*fineQuantMaxBits {
		fineBits = NumCELTBands * fineQuantMaxBits
	}
	return fineBits
}
