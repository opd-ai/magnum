// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements the CELT frequency band energy computation as specified
// in RFC 6716 §4.3.2 (now §5.3.1). CELT divides the spectrum into 21 frequency
// bands that approximate the Bark scale (critical bands of human hearing).
//
// The band energy is computed in the log domain for efficient quantization
// and perceptual modeling.

package magnum

import "math"

// celtBands defines the 21 CELT frequency bands as MDCT bin boundaries.
// These values are from RFC 6716 §5.3.1 Table 5.6.1 (for fullband 48 kHz, 960 samples).
// Each pair of consecutive values [i, i+1] defines a band from bin start (inclusive)
// to bin end (exclusive).
//
// The bands approximate the Bark scale (critical bands of human hearing) to
// enable perceptually-weighted quantization.
var celtBands = [22]int{
	0, 2, 4, 6, 8, 10, 12, 15, 19, 23, 29, 37, 47, 59, 74, 93, 118, 149, 188, 237, 299, 377,
}

// NumCELTBands is the number of frequency bands in the CELT codec.
const NumCELTBands = 21

// BandEnergy holds the computed band energies for a frame.
// Energies are stored in the log domain (dB-like scale) for quantization.
type BandEnergy struct {
	// Linear holds the linear (power) energy for each band.
	// Energy[i] = sum of squared MDCT coefficients in band i.
	Linear [NumCELTBands]float64

	// LogEnergy holds the log-domain energy for each band.
	// LogEnergy[i] = 10 * log10(Linear[i] / bandWidth[i])
	// This is normalized by band width for consistent perceptual scaling.
	LogEnergy [NumCELTBands]float64

	// Valid indicates whether each band has valid energy (non-zero coefficients).
	Valid [NumCELTBands]bool

	// numBins is the total number of MDCT bins used (depends on frame size).
	numBins int
}

// ComputeBandEnergy computes the per-band energy from MDCT coefficients.
//
// The input spectrum should have N/2 coefficients from an N-point MDCT.
// For 48 kHz fullband Opus, this is 480 coefficients from a 960-point MDCT.
//
// The band boundaries are scaled proportionally for different frame sizes.
// For example, a 480-point MDCT (10 ms frame) uses half the bin indices.
//
// Returns nil if the spectrum is empty.
func ComputeBandEnergy(spectrum []float64) *BandEnergy {
	if len(spectrum) == 0 {
		return nil
	}

	be := &BandEnergy{
		numBins: len(spectrum),
	}

	// Scale factor for band boundaries based on actual spectrum size vs reference (480 bins for 960-point MDCT)
	refBins := 480 // 960-point MDCT produces 480 bins (fullband reference)
	scale := float64(len(spectrum)) / float64(refBins)

	for band := 0; band < NumCELTBands; band++ {
		// Scale band boundaries to match actual spectrum size
		startBin := int(float64(celtBands[band]) * scale)
		endBin := int(float64(celtBands[band+1]) * scale)

		// Clamp to spectrum bounds
		if startBin >= len(spectrum) {
			startBin = len(spectrum)
		}
		if endBin > len(spectrum) {
			endBin = len(spectrum)
		}

		// Skip empty bands (can happen for lower sample rates)
		if startBin >= endBin {
			be.Linear[band] = 0
			be.LogEnergy[band] = -100 // Very low energy floor in dB
			be.Valid[band] = false
			continue
		}

		// Compute sum of squared coefficients (power)
		energy := 0.0
		for bin := startBin; bin < endBin; bin++ {
			energy += spectrum[bin] * spectrum[bin]
		}

		bandWidth := float64(endBin - startBin)
		be.Linear[band] = energy
		be.Valid[band] = true

		// Compute log-domain energy normalized by band width
		// Add small epsilon to avoid log(0)
		const epsilon = 1e-30
		normalizedEnergy := energy / bandWidth
		if normalizedEnergy < epsilon {
			normalizedEnergy = epsilon
		}
		be.LogEnergy[band] = 10 * math.Log10(normalizedEnergy)
	}

	return be
}

// BandWidth returns the width in MDCT bins for the specified band.
// Returns 0 for invalid band indices.
func BandWidth(band int) int {
	if band < 0 || band >= NumCELTBands {
		return 0
	}
	return celtBands[band+1] - celtBands[band]
}

// BandStart returns the starting MDCT bin for the specified band.
// This is for the reference fullband 48 kHz (960-point MDCT, 480 bins).
// Returns -1 for invalid band indices.
func BandStart(band int) int {
	if band < 0 || band >= NumCELTBands {
		return -1
	}
	return celtBands[band]
}

// BandEnd returns the ending MDCT bin (exclusive) for the specified band.
// This is for the reference fullband 48 kHz (960-point MDCT, 480 bins).
// Returns -1 for invalid band indices.
func BandEnd(band int) int {
	if band < 0 || band > NumCELTBands {
		return -1
	}
	return celtBands[band+1]
}

// ScaledBandBoundaries returns the band start and end bins scaled for a given
// spectrum size. This is useful when working with different frame sizes.
//
// For 48 kHz fullband (480 bins), scale is 1.0.
// For 24 kHz superwideband (240 bins), scale is 0.5.
func ScaledBandBoundaries(spectrumSize int) (starts, ends [NumCELTBands]int) {
	refBins := 480 // Reference: 960-point MDCT produces 480 bins
	scale := float64(spectrumSize) / float64(refBins)

	for band := 0; band < NumCELTBands; band++ {
		starts[band] = int(float64(celtBands[band]) * scale)
		ends[band] = int(float64(celtBands[band+1]) * scale)

		// Clamp to spectrum bounds
		if starts[band] > spectrumSize {
			starts[band] = spectrumSize
		}
		if ends[band] > spectrumSize {
			ends[band] = spectrumSize
		}
	}

	return starts, ends
}

// TotalEnergy returns the sum of all linear band energies.
func (be *BandEnergy) TotalEnergy() float64 {
	total := 0.0
	for band := 0; band < NumCELTBands; band++ {
		total += be.Linear[band]
	}
	return total
}

// NumValidBands returns the count of bands with valid (non-zero) energy.
func (be *BandEnergy) NumValidBands() int {
	count := 0
	for band := 0; band < NumCELTBands; band++ {
		if be.Valid[band] {
			count++
		}
	}
	return count
}

// AverageLogEnergy returns the average log-domain energy across all valid bands.
func (be *BandEnergy) AverageLogEnergy() float64 {
	sum := 0.0
	count := 0
	for band := 0; band < NumCELTBands; band++ {
		if be.Valid[band] {
			sum += be.LogEnergy[band]
			count++
		}
	}
	if count == 0 {
		return -100 // Silence floor
	}
	return sum / float64(count)
}
