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

// PVQ implements Pyramid Vector Quantization for CELT spectral coding.
type PVQ struct {
	// Precomputed U(N,K) values for encoding/decoding efficiency
	// U(N,K) = number of codewords with first element non-negative
	uCache map[uint64]uint64
}

// NewPVQ creates a new PVQ encoder/decoder instance.
func NewPVQ() *PVQ {
	return &PVQ{
		uCache: make(map[uint64]uint64),
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
// U(N,K) is symmetric: U(N,K) = U(K,N)
func (p *PVQ) U(n, k int) uint64 {
	// Normalize to use symmetry (access smaller index first)
	if k > n {
		n, k = k, n
	}

	// Base cases
	if k == 0 {
		return 1
	}
	if n == 0 {
		return 0
	}
	if k == 1 {
		return 1 // U(N,1) = 1 for all N >= 1
	}
	if n == 1 {
		return 1 // U(1,K) = 1 for all K >= 1
	}

	// Check cache
	cacheKey := uint64(n)<<32 | uint64(k)
	if val, ok := p.uCache[cacheKey]; ok {
		return val
	}

	// For small n, use polynomial formulas (more efficient)
	if n == 2 {
		// U(2,K) = 2*K - 1
		result := uint64(2*k - 1)
		p.uCache[cacheKey] = result
		return result
	}
	if n == 3 {
		// U(3,K) = (2*K-2)*K + 1 = 2*K^2 - 2*K + 1
		result := uint64(2*k*k - 2*k + 1)
		p.uCache[cacheKey] = result
		return result
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

	// Normalize input to unit L2 norm
	norm := 0.0
	for i := 0; i < n; i++ {
		norm += x[i] * x[i]
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := 0; i < n; i++ {
			x[i] /= norm
		}
	}

	// Greedy pulse allocation algorithm
	// Allocate pulses one at a time to minimize distortion
	pulses := make([]int, n)
	signs := make([]int, n)

	// Determine signs from input
	for i := 0; i < n; i++ {
		if x[i] >= 0 {
			signs[i] = 1
		} else {
			signs[i] = -1
		}
	}

	// Allocate K pulses greedily
	for pulse := 0; pulse < k; pulse++ {
		bestIdx := 0
		bestGain := -math.MaxFloat64

		// Find position that maximizes correlation with target
		for i := 0; i < n; i++ {
			// Compute gain from adding one pulse at position i
			currentNorm := 0.0
			for j := 0; j < n; j++ {
				val := float64(pulses[j])
				currentNorm += val * val
			}

			// Proposed pulse count at position i
			newPulse := pulses[i] + 1

			// Compute correlation gain
			absX := math.Abs(x[i])
			gain := absX * float64(newPulse) / math.Sqrt(currentNorm+float64(2*pulses[i]+1))

			if gain > bestGain {
				bestGain = gain
				bestIdx = i
			}
		}

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
func (p *PVQ) encodeIndex(pulses, signs []int, n, k int) uint64 {
	if k == 0 {
		return 0
	}

	var index uint64

	// Process each dimension
	remainingK := k
	for i := 0; i < n && remainingK > 0; i++ {
		// U(N-i, K') gives the count of codewords where dimension i is zero or positive
		// For each pulse count p at position i, we add the codewords skipped

		// First, handle the sign
		if pulses[i] > 0 {
			// Non-zero pulse at this position
			// Add U(N-i, K) for all codewords with smaller values at position i
			for p := 0; p < pulses[i]; p++ {
				// Count codewords with exactly p pulses at position i, negative sign first
				if p > 0 {
					// Negative p pulses
					index += p.V(n-i-1, remainingK-p)
				}
				// Positive p pulses (for p > 0)
				if p > 0 {
					index += p.V(n-i-1, remainingK-p)
				}
			}

			// Now account for sign at current position
			if signs[i] < 0 {
				// Add codewords with positive sign for this pulse count
				index += p.V(n-i-1, remainingK-pulses[i])
			}

			remainingK -= pulses[i]
		}
	}

	return index
}

// decodeIndex reconstructs pulses and signs from a combinatoric index.
func (p *PVQ) decodeIndex(cw *PVQCodeword, index uint64, n, k int) {
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
				countForPulse = p.V(n-i-1, remainingK)
			} else {
				// Non-zero pulses: both signs possible
				countForPulse = 2 * p.V(n-i-1, remainingK-pulseCount)
			}

			if remainingIndex < countForPulse {
				cw.Pulses[i] = pulseCount

				if pulseCount > 0 {
					// Determine sign
					half := p.V(n-i-1, remainingK-pulseCount)
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
