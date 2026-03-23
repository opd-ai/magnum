// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements Normalized Line Spectral Frequency (NLSF) conversion
// as specified in RFC 6716 §4.2.7 and the SILK codec specification.
//
// NLSFs are a normalized representation of Line Spectral Frequencies (LSFs),
// which are a re-parameterization of LPC coefficients. NLSFs have several
// advantages over direct LPC representation:
// - They are bounded in [0, 1], making them easy to quantize
// - They form a strictly increasing sequence, ensuring filter stability
// - They are robust against quantization errors
// - They interpolate smoothly for transitions between frames

package magnum

import (
	"math"
)

// NLSFOrder defines the NLSF orders used by SILK.
const (
	NLSFOrderNarrowband = 10 // 8 kHz
	NLSFOrderWideband   = 16 // 16 kHz
)

// Minimum and maximum NLSF separation to ensure stability.
const (
	nlsfMinSeparation = 0.005 // Minimum separation between consecutive NLSFs
	nlsfMinValue      = 0.001 // Minimum NLSF value
	nlsfMaxValue      = 0.999 // Maximum NLSF value
)

// LPCToNLSF converts LPC coefficients to Normalized Line Spectral Frequencies.
//
// The conversion uses polynomial root finding on the unit circle to find
// the LSFs, then normalizes them to [0, 1].
//
// Returns a slice of NLSFs of the same length as the input LPC order.
// The NLSFs are guaranteed to be in strictly increasing order in [0, 1].
func LPCToNLSF(lpc []float64) []float64 {
	order := len(lpc)
	if order == 0 {
		return nil
	}

	// Create the P(z) and Q(z) polynomials from LPC coefficients
	// P(z) = A(z) + z^(-order-1) * A(z^-1)  (symmetric)
	// Q(z) = A(z) - z^(-order-1) * A(z^-1)  (antisymmetric)
	p := make([]float64, order+2)
	q := make([]float64, order+2)

	// Build A(z) polynomial: 1 + a1*z^-1 + a2*z^-2 + ... + an*z^-n
	p[0] = 1.0
	q[0] = 1.0
	for i := 0; i < order; i++ {
		p[i+1] = lpc[i]
		q[i+1] = lpc[i]
	}

	// P(z) = A(z) + z^-(order+1) * A(z^-1)
	// Q(z) = A(z) - z^-(order+1) * A(z^-1)
	for i := 0; i <= order; i++ {
		p[i] += p[order-i]
		q[i] -= q[order-i]
	}
	p[order+1] = 0
	q[order+1] = 0

	// For antisymmetric Q(z), factor out (1 - z^-1)
	// For symmetric P(z), factor out (1 + z^-1)
	pRoots := chebyshevRootFind(p[:order+1], (order+1)/2)
	qRoots := chebyshevRootFind(q[:order+1], order/2)

	// Combine and sort roots
	nlsf := make([]float64, order)
	pIdx, qIdx := 0, 0
	for i := 0; i < order; i++ {
		if i%2 == 0 {
			if pIdx < len(pRoots) {
				nlsf[i] = pRoots[pIdx]
				pIdx++
			}
		} else {
			if qIdx < len(qRoots) {
				nlsf[i] = qRoots[qIdx]
				qIdx++
			}
		}
	}

	// Sort to ensure increasing order
	sortFloat64s(nlsf)

	// Stabilize: ensure minimum separation
	StabilizeNLSF(nlsf)

	return nlsf
}

// chebyshevRootFind finds roots of a polynomial on the unit circle
// using Chebyshev polynomial evaluation.
func chebyshevRootFind(poly []float64, numRoots int) []float64 {
	if numRoots <= 0 || len(poly) == 0 {
		return nil
	}

	roots := make([]float64, 0, numRoots)
	n := len(poly)

	// Search for roots by evaluating the polynomial at discrete points
	// and looking for sign changes (indicates a root crossing)
	const numSearchPoints = 512
	prevVal := evalPolyCosine(poly, n, 0)

	for i := 1; i <= numSearchPoints && len(roots) < numRoots; i++ {
		// Evaluate at cos(pi * i / numSearchPoints), which corresponds to
		// frequency w = pi * i / numSearchPoints
		w := float64(i) / float64(numSearchPoints)
		currVal := evalPolyCosine(poly, n, w)

		// Check for sign change (root crossing)
		if (prevVal >= 0 && currVal < 0) || (prevVal < 0 && currVal >= 0) {
			// Refine root using bisection
			root := bisectionRootFind(poly, n, float64(i-1)/float64(numSearchPoints), w)
			roots = append(roots, root)
		}
		prevVal = currVal
	}

	return roots
}

// evalPolyCosine evaluates a polynomial at cos(pi * w) using Chebyshev recursion.
func evalPolyCosine(poly []float64, n int, w float64) float64 {
	// Use Horner's method with cos(pi * w)
	x := math.Cos(math.Pi * w)
	result := poly[n-1]
	for i := n - 2; i >= 0; i-- {
		result = result*x + poly[i]
	}
	return result
}

// bisectionRootFind refines a root using bisection method.
func bisectionRootFind(poly []float64, n int, lo, hi float64) float64 {
	const maxIter = 32
	const tolerance = 1e-10

	for iter := 0; iter < maxIter; iter++ {
		mid := (lo + hi) / 2
		if hi-lo < tolerance {
			return mid
		}

		loVal := evalPolyCosine(poly, n, lo)
		midVal := evalPolyCosine(poly, n, mid)

		if (loVal >= 0 && midVal < 0) || (loVal < 0 && midVal >= 0) {
			hi = mid
		} else {
			lo = mid
		}
	}

	return (lo + hi) / 2
}

// NLSFToLPC converts Normalized Line Spectral Frequencies back to LPC coefficients.
//
// This is the inverse of LPCToNLSF. The NLSFs must be in strictly increasing
// order in [0, 1].
func NLSFToLPC(nlsf []float64) []float64 {
	order := len(nlsf)
	if order == 0 {
		return nil
	}

	// Convert NLSF to LSF (multiply by pi)
	lsf := make([]float64, order)
	for i := range nlsf {
		lsf[i] = nlsf[i] * math.Pi
	}

	// Reconstruct P(z) and Q(z) from their roots
	// P(z) has roots at even indices, Q(z) has roots at odd indices
	pOrder := (order + 1) / 2
	qOrder := order / 2

	pRoots := make([]float64, pOrder)
	qRoots := make([]float64, qOrder)

	pIdx, qIdx := 0, 0
	for i := 0; i < order; i++ {
		if i%2 == 0 && pIdx < pOrder {
			pRoots[pIdx] = lsf[i]
			pIdx++
		} else if qIdx < qOrder {
			qRoots[qIdx] = lsf[i]
			qIdx++
		}
	}

	// Build P(z) and Q(z) polynomials from roots
	p := buildPolyFromRoots(pRoots)
	q := buildPolyFromRoots(qRoots)

	// Reconstruct LPC: a[k] = (p[k] + q[k]) / 2
	lpc := make([]float64, order)
	pLen := len(p)
	qLen := len(q)

	for k := 0; k < order; k++ {
		var pVal, qVal float64
		if k < pLen {
			pVal = p[k]
		}
		if k < qLen {
			qVal = q[k]
		}
		lpc[k] = (pVal + qVal) / 2
	}

	return lpc
}

// buildPolyFromRoots builds a polynomial from its roots on the unit circle.
// Each root w contributes a factor (1 - 2*cos(w)*z^-1 + z^-2).
func buildPolyFromRoots(roots []float64) []float64 {
	if len(roots) == 0 {
		return []float64{1.0}
	}

	// Start with polynomial 1
	poly := []float64{1.0}

	for _, w := range roots {
		// Multiply by (1 - 2*cos(w)*z^-1 + z^-2)
		c := -2 * math.Cos(w)
		newPoly := make([]float64, len(poly)+2)

		for i, coef := range poly {
			newPoly[i] += coef
			newPoly[i+1] += coef * c
			newPoly[i+2] += coef
		}

		poly = newPoly
	}

	return poly
}

// StabilizeNLSF ensures NLSFs are in valid range and have minimum separation.
// This implements the NLSF stabilization procedure from SILK.
func StabilizeNLSF(nlsf []float64) {
	if len(nlsf) == 0 {
		return
	}

	order := len(nlsf)

	// Clamp to valid range
	for i := range nlsf {
		if nlsf[i] < nlsfMinValue {
			nlsf[i] = nlsfMinValue
		}
		if nlsf[i] > nlsfMaxValue {
			nlsf[i] = nlsfMaxValue
		}
	}

	// Ensure strictly increasing order with minimum separation
	// Forward pass: push values up if too close
	for i := 1; i < order; i++ {
		minVal := nlsf[i-1] + nlsfMinSeparation
		if nlsf[i] < minVal {
			nlsf[i] = minVal
		}
	}

	// Backward pass: push values down if we exceeded max
	if nlsf[order-1] > nlsfMaxValue {
		nlsf[order-1] = nlsfMaxValue
		for i := order - 2; i >= 0; i-- {
			maxVal := nlsf[i+1] - nlsfMinSeparation
			if nlsf[i] > maxVal {
				nlsf[i] = maxVal
			}
		}
	}

	// Final check: ensure all values are in range
	for i := range nlsf {
		if nlsf[i] < nlsfMinValue {
			nlsf[i] = nlsfMinValue
		}
		if nlsf[i] > nlsfMaxValue {
			nlsf[i] = nlsfMaxValue
		}
	}
}

// InterpolateNLSF performs linear interpolation between two NLSF vectors.
// This is used for smooth transitions between frames.
//
// alpha is the interpolation factor: 0.0 returns nlsf1, 1.0 returns nlsf2.
func InterpolateNLSF(nlsf1, nlsf2 []float64, alpha float64) []float64 {
	if len(nlsf1) != len(nlsf2) || len(nlsf1) == 0 {
		return nil
	}

	// Clamp alpha to [0, 1]
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}

	result := make([]float64, len(nlsf1))
	for i := range result {
		result[i] = nlsf1[i]*(1-alpha) + nlsf2[i]*alpha
	}

	// Ensure result is stable
	StabilizeNLSF(result)

	return result
}

// sortFloat64s sorts a slice of float64 in ascending order (simple insertion sort).
func sortFloat64s(a []float64) {
	for i := 1; i < len(a); i++ {
		key := a[i]
		j := i - 1
		for j >= 0 && a[j] > key {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = key
	}
}

// NLSFQuantizer quantizes NLSF vectors for encoding.
type NLSFQuantizer struct {
	order int
	// Quantization tables would be added here for full SILK implementation
}

// NewNLSFQuantizer creates a new NLSF quantizer for the given order.
func NewNLSFQuantizer(order int) *NLSFQuantizer {
	return &NLSFQuantizer{order: order}
}

// Quantize quantizes an NLSF vector to integer indices.
// Returns the quantized indices and the quantized NLSF values.
func (q *NLSFQuantizer) Quantize(nlsf []float64, bits int) ([]int, []float64) {
	if len(nlsf) != q.order {
		return nil, nil
	}

	// Simple uniform quantization (full SILK uses trained codebooks)
	numLevels := 1 << bits
	indices := make([]int, q.order)
	quantized := make([]float64, q.order)

	for i, val := range nlsf {
		// Map [0, 1] to [0, numLevels-1]
		idx := int(val * float64(numLevels-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= numLevels {
			idx = numLevels - 1
		}
		indices[i] = idx
		quantized[i] = float64(idx) / float64(numLevels-1)
	}

	// Ensure quantized values are stable
	StabilizeNLSF(quantized)

	return indices, quantized
}

// Dequantize reconstructs NLSF values from quantized indices.
func (q *NLSFQuantizer) Dequantize(indices []int, bits int) []float64 {
	if len(indices) != q.order {
		return nil
	}

	numLevels := 1 << bits
	nlsf := make([]float64, q.order)

	for i, idx := range indices {
		nlsf[i] = float64(idx) / float64(numLevels-1)
	}

	// Ensure result is stable
	StabilizeNLSF(nlsf)

	return nlsf
}
