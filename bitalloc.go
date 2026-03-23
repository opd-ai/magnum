// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements the CELT bit allocation system as specified in
// RFC 6716 §4.3.4. The bit allocation algorithm distributes available bits
// across frequency bands based on:
// 1. The target bitrate (from SetBitrate)
// 2. The band energy (perceptual importance)
// 3. The frame size and sample rate
// 4. The band width (larger bands need more bits)

package magnum

import (
	"math"
)

// BitAllocationConfig holds configuration for bit allocation.
type BitAllocationConfig struct {
	// Bitrate is the target bitrate in bits per second
	Bitrate int
	// SampleRate is the audio sample rate in Hz
	SampleRate int
	// FrameSize is the number of samples per frame
	FrameSize int
	// Channels is the number of audio channels
	Channels int
}

// BitAllocation holds the computed bit allocation for a frame.
type BitAllocation struct {
	// BandBits is the number of bits allocated to each band
	BandBits [NumCELTBands]int
	// TotalBits is the total bits available for the frame
	TotalBits int
	// UsedBits is the total bits actually allocated
	UsedBits int
	// FineBits is the number of bits reserved for fine energy
	FineBits int
	// PVQBits is the number of bits allocated for PVQ coding
	PVQBits int
}

// BitAllocator computes bit allocation for CELT frames.
type BitAllocator struct {
	config BitAllocationConfig
	// Static allocation table based on RFC 6716
	staticAlloc [NumCELTBands]int
}

// NewBitAllocator creates a new bit allocator with the given configuration.
func NewBitAllocator(config BitAllocationConfig) *BitAllocator {
	ba := &BitAllocator{
		config: config,
	}
	ba.computeStaticAllocation()
	return ba
}

// computeStaticAllocation computes the base allocation per band.
// This follows the CELT allocation strategy from RFC 6716.
func (ba *BitAllocator) computeStaticAllocation() {
	// Base allocation roughly follows band width (perceptual importance).
	// Lower bands (speech frequencies) get priority at low bitrates.
	// Higher bands get more bits at higher bitrates.

	// Compute bits per frame
	bitsPerSecond := float64(ba.config.Bitrate)
	framesPerSecond := float64(ba.config.SampleRate) / float64(ba.config.FrameSize)
	bitsPerFrame := bitsPerSecond / framesPerSecond

	// Reserve bits for overhead (TOC, silence flag, transient flag, etc.)
	overhead := 20.0 // Approximate overhead bits
	availableBits := bitsPerFrame - overhead

	// Compute total band width for weighting
	totalWidth := 0
	for i := 0; i < NumCELTBands; i++ {
		totalWidth += BandWidth(i)
	}

	// Base allocation: proportional to band width
	for i := 0; i < NumCELTBands; i++ {
		// Weight by band width
		width := BandWidth(i)
		baseBits := availableBits * float64(width) / float64(totalWidth)

		// Apply perceptual weighting: boost lower bands at low bitrates
		perceptualWeight := computePerceptualWeight(i, ba.config.Bitrate)
		ba.staticAlloc[i] = int(baseBits * perceptualWeight)
	}
}

// computePerceptualWeight returns a weight factor for perceptual importance.
// At lower bitrates, speech frequencies (bands 0-10) are more important.
// At higher bitrates, all bands are treated more equally.
func computePerceptualWeight(band, bitrate int) float64 {
	// Normalize bitrate to 0-1 range (6000-510000 bps)
	normalizedBitrate := float64(bitrate-6000) / float64(510000-6000)
	normalizedBitrate = math.Max(0, math.Min(1, normalizedBitrate))

	// Speech bands (0-10) get boosted weight at low bitrates
	if band < 11 {
		// At low bitrate: weight 1.5, at high bitrate: weight 1.0
		return 1.0 + 0.5*(1-normalizedBitrate)
	}

	// High frequency bands get reduced weight at low bitrates
	// At low bitrate: weight 0.5, at high bitrate: weight 1.0
	return 0.5 + 0.5*normalizedBitrate
}

// Allocate computes the bit allocation for a frame given band energies.
func (ba *BitAllocator) Allocate(energy *BandEnergy) *BitAllocation {
	alloc := &BitAllocation{}

	// Compute total bits for this frame
	bitsPerSecond := float64(ba.config.Bitrate)
	framesPerSecond := float64(ba.config.SampleRate) / float64(ba.config.FrameSize)
	alloc.TotalBits = int(bitsPerSecond / framesPerSecond)

	// Reserve bits for frame overhead
	overheadBits := 20 // TOC, flags, etc.

	// Reserve bits for coarse energy coding
	coarseEnergyBits := NumCELTBands * 7 // 7 bits per band

	// Reserve bits for fine energy coding (2 bits per band)
	alloc.FineBits = NumCELTBands * 2

	// Bits available for PVQ spectral coding
	alloc.PVQBits = alloc.TotalBits - overheadBits - coarseEnergyBits - alloc.FineBits
	if alloc.PVQBits < 0 {
		alloc.PVQBits = 0
	}

	// Distribute PVQ bits across bands
	ba.distributeBits(alloc, energy)

	return alloc
}

// distributeBits distributes available PVQ bits across bands.
func (ba *BitAllocator) distributeBits(alloc *BitAllocation, energy *BandEnergy) {
	if alloc.PVQBits <= 0 {
		return
	}

	// Compute weights based on static allocation and energy
	weights := make([]float64, NumCELTBands)
	totalWeight := 0.0

	for i := 0; i < NumCELTBands; i++ {
		if energy != nil && energy.Valid[i] {
			// Weight by static allocation and energy
			staticWeight := float64(ba.staticAlloc[i])
			energyWeight := math.Pow(10.0, energy.LogEnergy[i]/20.0)

			// Combine static and energy-based weights
			weights[i] = staticWeight * (0.5 + 0.5*energyWeight)
			totalWeight += weights[i]
		} else if energy == nil {
			// No energy info: use static allocation only
			weights[i] = float64(ba.staticAlloc[i])
			totalWeight += weights[i]
		}
	}

	if totalWeight <= 0 {
		// Fallback: equal distribution
		bitsPerBand := alloc.PVQBits / NumCELTBands
		for i := 0; i < NumCELTBands; i++ {
			alloc.BandBits[i] = bitsPerBand
		}
		alloc.UsedBits = bitsPerBand * NumCELTBands
		return
	}

	// Allocate bits proportionally
	remaining := alloc.PVQBits
	for i := 0; i < NumCELTBands; i++ {
		bits := int(float64(alloc.PVQBits) * weights[i] / totalWeight)
		// Ensure minimum bits for valid bands
		if energy != nil && energy.Valid[i] && bits < 4 {
			bits = 4
		}
		if bits > remaining {
			bits = remaining
		}
		alloc.BandBits[i] = bits
		alloc.UsedBits += bits
		remaining -= bits
	}

	// Distribute any remaining bits to bands with highest weights
	for remaining > 0 {
		maxIdx := 0
		maxWeight := weights[0]
		for i := 1; i < NumCELTBands; i++ {
			if weights[i] > maxWeight {
				maxWeight = weights[i]
				maxIdx = i
			}
		}
		alloc.BandBits[maxIdx]++
		alloc.UsedBits++
		remaining--
		weights[maxIdx] *= 0.9 // Reduce weight to distribute more evenly
	}
}

// UpdateBitrate updates the allocator's bitrate and recomputes static allocation.
func (ba *BitAllocator) UpdateBitrate(bitrate int) {
	ba.config.Bitrate = bitrate
	ba.computeStaticAllocation()
}

// AllocateBandBits is the entry point for bit allocation from celt_frame.go.
// It takes the total bits available, number of bands, and band energies,
// and returns the per-band bit allocation.
func AllocateBandBits(totalBits, numBands int, energy *BandEnergy, bitrate, sampleRate, frameSize int) []int {
	// Create a bit allocator with the given configuration
	config := BitAllocationConfig{
		Bitrate:    bitrate,
		SampleRate: sampleRate,
		FrameSize:  frameSize,
		Channels:   1,
	}
	allocator := NewBitAllocator(config)

	// Override total bits (caller may have adjusted for overhead already)
	alloc := &BitAllocation{
		TotalBits: totalBits,
		PVQBits:   totalBits,
	}

	allocator.distributeBits(alloc, energy)

	bits := make([]int, numBands)
	for i := 0; i < numBands && i < NumCELTBands; i++ {
		bits[i] = alloc.BandBits[i]
	}
	return bits
}

// GetStaticAllocation returns the static bit allocation per band.
// This is useful for debugging and testing.
func (ba *BitAllocator) GetStaticAllocation() [NumCELTBands]int {
	return ba.staticAlloc
}
