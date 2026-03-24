// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements Pyramid Vector Quantization (PVQ) as specified in
// RFC 6716 §4.3.4. PVQ is used to encode the normalized spectral shape
// (unit-norm vectors) in each frequency band after the energy is quantized.
//
// PVQ represents unit-norm vectors as integer pulse distributions. A vector
// with K pulses in N dimensions has its coordinates constrained such that
// the sum of absolute values equals K (L1 norm). The codebook consists of
// all such vectors, providing efficient representation of spectral shapes.
//
// Reference: Fischer, T.R. "A Pyramid Vector Quantizer" IEEE Trans. Info.
// Theory, Vol IT-32, No. 4, July 1986, pp. 568-583

package magnum

import (
	"math"
)

// PVQCodeword represents a quantized unit-norm vector as pulse positions and signs.
type PVQCodeword struct {
	// N is the dimension (number of coefficients in the band)
	N int
	// K is the number of pulses (quantization resolution)
	K int
	// Pulses contains the pulse count at each position (absolute values)
	Pulses []int
	// Signs contains the sign of each non-zero pulse (+1 or -1)
	Signs []int
	// Index is the combinatoric index for this codeword (for range coding)
	Index uint64
}

// Constants for U(N,K) lookup table dimensions
const (
	pvqMaxN = 64  // Max band dimension (actual max ~53)
	pvqMaxK = 130 // Max pulses typically encountered (SelectK uses up to 128)
)

// PVQ implements Pyramid Vector Quantization for CELT spectral coding.
type PVQ struct {
	// Precomputed U(N,K) values in lookup table for fast access.
	// uTable[n][k] stores U(n,k) for 0 <= n,k < pvqMax{N,K}.
	uTable [pvqMaxN][pvqMaxK]uint64
	// Fallback cache for values outside lookup table range.
	uCache map[uint64]uint64
}

// NewPVQ creates a new PVQ encoder/decoder instance.
func NewPVQ() *PVQ {
	p := &PVQ{
		uCache: make(map[uint64]uint64),
	}
	p.precomputeU()
	return p
}

// precomputeU fills the lookup table with U(n,k) values using iteration.
// Uses bottom-up dynamic programming to avoid recursion overhead.
func (p *PVQ) precomputeU() {
	// Initialize base cases for the full table
	for i := 0; i < pvqMaxN; i++ {
		p.uTable[i][0] = 1 // U(N,0) = 1
	}
	// U(0,K) = 0 for K > 0 (already 0 by default from zero-initialization)

	for i := 1; i < pvqMaxN; i++ {
		p.uTable[i][1] = 1 // U(N,1) = 1
	}
	for j := 1; j < pvqMaxK; j++ {
		p.uTable[1][j] = 1 // U(1,K) = 1
	}

	// Fill table row by row. For each cell, compute using recurrence
	// U(N,K) = U(N-1,K) + U(N,K-1) + U(N-1,K-1)
	for n := 2; n < pvqMaxN; n++ {
		for k := 2; k < pvqMaxK; k++ {
			p.uTable[n][k] = p.uTable[n-1][k] + p.uTable[n][k-1] + p.uTable[n-1][k-1]
		}
	}
}

// V computes V(N,K) - the total number of PVQ codewords for N dimensions and K pulses.
// V(N,K) = number of N-dimensional unit pulse vectors with K pulses, including signs.
func (p *PVQ) V(n, k int) uint64 {
	if k == 0 {
		return 1 // Only the zero vector
	}
	if n == 0 {
		return 0 // No dimensions, no valid vectors (except k=0 case above)
	}
	// V(N,K) = U(N,K) + U(N,K+1)
	return p.U(n, k) + p.U(n, k+1)
}

// U computes U(N,K) - the number of codewords with first element >= 0.
// This is the key combinatoric function for PVQ encoding/decoding.
func (p *PVQ) U(n, k int) uint64 {
	// Base cases
	if k == 0 {
		return 1
	}
	if n == 0 {
		return 0
	}

	// Fast path: use precomputed lookup table for common values
	if n < pvqMaxN && k < pvqMaxK {
		return p.uTable[n][k]
	}

	// Slow path: use map cache for large values
	// Normalize to use symmetry for cache efficiency
	if k > n {
		n, k = k, n
	}
	cacheKey := uint64(n)<<32 | uint64(k)
	if val, ok := p.uCache[cacheKey]; ok {
		return val
	}

	// General recurrence: U(N,K) = U(N-1,K) + U(N,K-1) + U(N-1,K-1)
	result := p.U(n-1, k) + p.U(n, k-1) + p.U(n-1, k-1)
	p.uCache[cacheKey] = result
	return result
}

// Encode quantizes a unit-norm vector using PVQ with K pulses.
// The input vector should be approximately unit-norm (L2).
// Returns a PVQCodeword with the quantized representation.
func (p *PVQ) Encode(x []float64, k int) *PVQCodeword {
	n := len(x)
	if n == 0 || k == 0 {
		return &PVQCodeword{N: n, K: 0, Pulses: make([]int, n), Signs: make([]int, n)}
	}

	pulses := make([]int, n)
	signs := make([]int, n)
	return p.encodeWithBuffers(x, k, pulses, signs)
}

// EncodeInto encodes a unit-norm vector using pre-allocated buffers.
// The pulses and signs slices must have length >= len(x).
func (p *PVQ) EncodeInto(x []float64, k int, pulses, signs []int) *PVQCodeword {
	n := len(x)
	if n == 0 || k == 0 {
		return &PVQCodeword{N: n, K: 0, Pulses: pulses[:n], Signs: signs[:n]}
	}
	return p.encodeWithBuffers(x, k, pulses[:n], signs[:n])
}

// EncodeIndex encodes a vector and returns only the combinatoric index.
// This avoids allocating a PVQCodeword when only the index is needed.
func (p *PVQ) EncodeIndex(x []float64, k int, pulses, signs []int) uint64 {
	n := len(x)
	if n == 0 || k == 0 {
		return 0
	}

	// Normalize input to unit L2 norm
	norm := 0.0
	for i := 0; i < n; i++ {
		norm += x[i] * x[i]
	}
	if norm > 0 {
		norm = 1.0 / math.Sqrt(norm)
		for i := 0; i < n; i++ {
			x[i] *= norm
		}
	}

	// Clear and initialize buffers
	for i := 0; i < n; i++ {
		pulses[i] = 0
		if x[i] >= 0 {
			signs[i] = 1
		} else {
			signs[i] = -1
		}
	}

	// Allocate K pulses greedily with O(NK) complexity
	// Track currentNorm incrementally instead of recomputing
	currentNormSq := 0.0

	for pulse := 0; pulse < k; pulse++ {
		bestIdx := 0
		bestGain := -math.MaxFloat64

		for i := 0; i < n; i++ {
			newPulse := pulses[i] + 1
			// Gain formula: |x[i]| * newPulse / sqrt(currentNormSq + 2*pulses[i] + 1)
			// Optimization: avoid Abs by using pre-computed sign
			absX := x[i]
			if signs[i] < 0 {
				absX = -absX
			}
			// delta = (2*pulses[i] + 1) is the change in norm squared
			gain := absX * float64(newPulse) / math.Sqrt(currentNormSq+float64(2*pulses[i]+1))
			if gain > bestGain {
				bestGain = gain
				bestIdx = i
			}
		}
		// Update currentNormSq for next iteration
		// ||p||^2 increases by (2*p[i] + 1) when p[i] increments by 1
		currentNormSq += float64(2*pulses[bestIdx] + 1)
		pulses[bestIdx]++
	}

	return p.encodeIndex(pulses, signs, n, k)
}

// encodeWithBuffers performs PVQ encoding with provided buffers.
func (p *PVQ) encodeWithBuffers(x []float64, k int, pulses, signs []int) *PVQCodeword {
	n := len(x)

	// Normalize input to unit L2 norm
	norm := 0.0
	for i := 0; i < n; i++ {
		norm += x[i] * x[i]
	}
	if norm > 0 {
		norm = 1.0 / math.Sqrt(norm)
		for i := 0; i < n; i++ {
			x[i] *= norm
		}
	}

	// Clear and initialize buffers
	for i := 0; i < n; i++ {
		pulses[i] = 0
		if x[i] >= 0 {
			signs[i] = 1
		} else {
			signs[i] = -1
		}
	}

	// Allocate K pulses greedily with O(NK) complexity
	// Track currentNormSq incrementally instead of recomputing
	currentNormSq := 0.0

	for pulse := 0; pulse < k; pulse++ {
		bestIdx := 0
		bestGain := -math.MaxFloat64

		// Find position that maximizes correlation with target
		for i := 0; i < n; i++ {
			// Proposed pulse count at position i
			newPulse := pulses[i] + 1

			// Compute correlation gain using pre-computed sign
			absX := x[i]
			if signs[i] < 0 {
				absX = -absX
			}
			gain := absX * float64(newPulse) / math.Sqrt(currentNormSq+float64(2*pulses[i]+1))

			if gain > bestGain {
				bestGain = gain
				bestIdx = i
			}
		}

		// Update currentNormSq for next iteration
		currentNormSq += float64(2*pulses[bestIdx] + 1)
		pulses[bestIdx]++
	}

	// Compute combinatoric index
	index := p.encodeIndex(pulses, signs, n, k)

	return &PVQCodeword{
		N:      n,
		K:      k,
		Pulses: pulses,
		Signs:  signs,
		Index:  index,
	}
}

// Decode reconstructs a unit-norm vector from a PVQ codeword.
func (p *PVQ) Decode(cw *PVQCodeword) []float64 {
	n := cw.N
	if n == 0 {
		return nil
	}

	result := make([]float64, n)

	// Reconstruct from pulses and signs
	normSq := 0.0
	for i := 0; i < n; i++ {
		val := float64(cw.Pulses[i])
		if cw.Signs[i] < 0 {
			val = -val
		}
		result[i] = val
		normSq += val * val
	}

	// Normalize to unit L2 norm
	if normSq > 0 {
		norm := math.Sqrt(normSq)
		for i := 0; i < n; i++ {
			result[i] /= norm
		}
	}

	return result
}

// DecodeFromIndex decodes a PVQ codeword from its combinatoric index.
func (p *PVQ) DecodeFromIndex(index uint64, n, k int) *PVQCodeword {
	cw := &PVQCodeword{
		N:      n,
		K:      k,
		Pulses: make([]int, n),
		Signs:  make([]int, n),
		Index:  index,
	}

	if k == 0 || n == 0 {
		return cw
	}

	// Decode using the recursive structure of U(N,K)
	p.decodeIndex(cw, index, n, k)

	return cw
}

// encodeIndex computes the combinatoric index for a pulse configuration.
// The index uniquely identifies the codeword within V(N,K) possibilities.
func (pvq *PVQ) encodeIndex(pulses, signs []int, n, k int) uint64 {
	if k == 0 {
		return 0
	}

	var index uint64

	// Process each dimension
	remainingK := k
	for i := 0; i < n && remainingK > 0; i++ {
		// For each pulse count p at position i, we add the codewords skipped

		if pulses[i] > 0 {
			// Non-zero pulse at this position
			// Add codewords for all smaller absolute pulse values
			for absP := 0; absP < pulses[i]; absP++ {
				if absP > 0 {
					// Both positive and negative for absP pulses
					index += 2 * pvq.V(n-i-1, remainingK-absP)
				} else {
					// Zero pulses at this position
					index += pvq.V(n-i-1, remainingK)
				}
			}

			// Now account for sign at current position
			if signs[i] < 0 {
				// Positive comes before negative in our ordering
				index += pvq.V(n-i-1, remainingK-pulses[i])
			}

			remainingK -= pulses[i]
		} else {
			// Zero pulses, no contribution to index at this position
			// but we consume the "zero" codewords from V(n-i-1, remainingK)
		}
	}

	return index
}

// decodeIndex reconstructs pulses and signs from a combinatoric index.
func (pvq *PVQ) decodeIndex(cw *PVQCodeword, index uint64, n, k int) {
	if k == 0 {
		return
	}

	remainingK := k
	remainingIndex := index

	for i := 0; i < n && remainingK > 0; i++ {
		// Try each possible pulse count at this position
		for pulseCount := 0; pulseCount <= remainingK; pulseCount++ {
			var countForPulse uint64

			if pulseCount == 0 {
				// Zero pulses at this position
				countForPulse = pvq.V(n-i-1, remainingK)
			} else {
				// Non-zero pulses: both signs possible
				countForPulse = 2 * pvq.V(n-i-1, remainingK-pulseCount)
			}

			if remainingIndex < countForPulse {
				cw.Pulses[i] = pulseCount

				if pulseCount > 0 {
					// Determine sign
					half := pvq.V(n-i-1, remainingK-pulseCount)
					if remainingIndex < half {
						cw.Signs[i] = 1 // Positive
					} else {
						cw.Signs[i] = -1 // Negative
						remainingIndex -= half
					}
				} else {
					cw.Signs[i] = 1 // Default positive for zero
				}

				remainingK -= pulseCount
				break
			}

			remainingIndex -= countForPulse
		}
	}
}

// BitsRequired returns the number of bits needed to encode a PVQ codeword.
// This is ceil(log2(V(N,K))).
func (p *PVQ) BitsRequired(n, k int) int {
	v := p.V(n, k)
	if v <= 1 {
		return 0
	}
	return int(math.Ceil(math.Log2(float64(v))))
}

// ComputeDistortion calculates the squared error between original and quantized vectors.
func ComputeDistortion(original, quantized []float64) float64 {
	if len(original) != len(quantized) {
		return math.MaxFloat64
	}

	distortion := 0.0
	for i := range original {
		diff := original[i] - quantized[i]
		distortion += diff * diff
	}
	return distortion
}

// SelectK determines the optimal number of pulses K for a given bit budget.
// The bit budget should account for the bits available for this band.
func (p *PVQ) SelectK(n, bitBudget int) int {
	if bitBudget <= 0 || n <= 0 {
		return 0
	}

	// Binary search for the largest K that fits in the bit budget
	low, high := 0, bitBudget*2 // Upper bound heuristic
	if high > 128 {
		high = 128 // Reasonable maximum
	}

	for low < high {
		mid := (low + high + 1) / 2
		bits := p.BitsRequired(n, mid)
		if bits <= bitBudget {
			low = mid
		} else {
			high = mid - 1
		}
	}

	return low
}
