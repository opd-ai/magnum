// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements the MDCT (Modified Discrete Cosine Transform) used by
// the CELT codec in Opus. The MDCT is a lapped transform that provides
// time-frequency decomposition with 50% overlap between frames.
//
// Reference: RFC 6716 §4.3.2

package magnum

import "math"

// MDCT implements the Modified Discrete Cosine Transform as specified in
// RFC 6716 §4.3.2 for the CELT codec.
//
// The MDCT transforms N time-domain samples into N/2 frequency-domain
// coefficients, using a 50% overlap between consecutive frames.
type MDCT struct {
	// n is the MDCT size (number of time-domain samples).
	n int
	// window holds the pre-computed window coefficients.
	window []float64
	// cosTable holds pre-computed cosine values for the transform.
	// Layout: cosTable[k*n + i] = cos(π/N * (i + 0.5 + N/4) * (k + 0.5))
	cosTable []float64
	// winCosTable holds pre-combined window*cosine values for ForwardInto.
	// Layout: winCosTable[k*n + i] = window[i] * cosTable[k*n + i]
	winCosTable []float64
}

// Supported MDCT sizes for Opus CELT codec.
// These correspond to frame durations at 48 kHz:
//   - 120 samples = 2.5 ms
//   - 240 samples = 5 ms
//   - 480 samples = 10 ms
//   - 960 samples = 20 ms
//   - 1920 samples = 40 ms
const (
	MDCTSize120  = 120
	MDCTSize240  = 240
	MDCTSize480  = 480
	MDCTSize960  = 960
	MDCTSize1920 = 1920
)

// NewMDCT creates a new MDCT instance for the given transform size.
// The size must be one of the supported MDCT sizes: 120, 240, 480, 960, or 1920.
func NewMDCT(n int) *MDCT {
	if !isValidMDCTSize(n) {
		return nil
	}

	n2 := n / 2
	m := &MDCT{
		n:           n,
		window:      make([]float64, n),
		cosTable:    make([]float64, n2*n),
		winCosTable: make([]float64, n2*n),
	}

	// Pre-compute window coefficients using RFC 6716 formula:
	// w[n] = sin(π/2 * sin²(π(n+0.5)/N))
	for i := 0; i < n; i++ {
		sinArg := math.Pi * (float64(i) + 0.5) / float64(n)
		sinSq := math.Sin(sinArg)
		sinSq *= sinSq
		m.window[i] = math.Sin(math.Pi / 2 * sinSq)
	}

	// Pre-compute cosine table for MDCT:
	// cos(π/N * (i + 0.5 + N/4) * (k + 0.5))
	// Also compute combined window*cosine table
	n4 := float64(n) / 4.0
	piOverN := math.Pi / float64(n)
	for k := 0; k < n2; k++ {
		kTerm := float64(k) + 0.5
		for i := 0; i < n; i++ {
			nTerm := float64(i) + 0.5 + n4
			cosVal := math.Cos(piOverN * nTerm * kTerm)
			m.cosTable[k*n+i] = cosVal
			m.winCosTable[k*n+i] = m.window[i] * cosVal
		}
	}

	return m
}

// isValidMDCTSize returns true if n is a valid MDCT size for Opus.
func isValidMDCTSize(n int) bool {
	switch n {
	case MDCTSize120, MDCTSize240, MDCTSize480, MDCTSize960, MDCTSize1920:
		return true
	}
	return false
}

// Size returns the MDCT transform size.
func (m *MDCT) Size() int {
	return m.n
}

// Forward performs the forward MDCT transform.
//
// Input: N time-domain samples (windowed internally)
// Output: N/2 frequency-domain coefficients
//
// The input slice must have exactly N samples. The output slice is allocated
// and returned.
func (m *MDCT) Forward(input []float64) []float64 {
	if len(input) != m.n {
		return nil
	}

	// Apply window to input
	windowed := make([]float64, m.n)
	for i := 0; i < m.n; i++ {
		windowed[i] = input[i] * m.window[i]
	}

	// Standard MDCT using pre-computed cosine table
	output := make([]float64, m.n/2)
	n2 := m.n / 2
	n := m.n

	for k := 0; k < n2; k++ {
		sum := 0.0
		cosRow := m.cosTable[k*n:]
		for i := 0; i < n; i++ {
			sum += windowed[i] * cosRow[i]
		}
		output[k] = sum
	}

	return output
}

// ForwardInto performs the forward MDCT transform into a pre-allocated output slice.
// This avoids allocations when the output buffer can be reused.
//
// Input: N time-domain samples
// Output: N/2 frequency-domain coefficients written to out
//
// Returns the number of coefficients written, or 0 if input/output sizes are wrong.
func (m *MDCT) ForwardInto(input, out []float64) int {
	if len(input) != m.n || len(out) < m.n/2 {
		return 0
	}

	n2 := m.n / 2
	n := m.n

	// Apply window and compute MDCT using pre-combined window*cosine table
	// Loop unrolled for better performance
	for k := 0; k < n2; k++ {
		sum := 0.0
		wcRow := m.winCosTable[k*n:]

		// Unroll by 8 for common sizes
		i := 0
		for ; i+7 < n; i += 8 {
			sum += input[i]*wcRow[i] + input[i+1]*wcRow[i+1] +
				input[i+2]*wcRow[i+2] + input[i+3]*wcRow[i+3] +
				input[i+4]*wcRow[i+4] + input[i+5]*wcRow[i+5] +
				input[i+6]*wcRow[i+6] + input[i+7]*wcRow[i+7]
		}
		// Handle remaining elements
		for ; i < n; i++ {
			sum += input[i] * wcRow[i]
		}
		out[k] = sum
	}

	return n2
}

// Inverse performs the inverse MDCT transform.
//
// Input: N/2 frequency-domain coefficients
// Output: N time-domain samples (windowed)
//
// The input slice must have exactly N/2 coefficients. The output slice is
// allocated and returned.
//
// Note: The IMDCT output must be combined with the previous frame's output
// using overlap-add to reconstruct the original signal.
func (m *MDCT) Inverse(input []float64) []float64 {
	n2 := m.n / 2
	if len(input) != n2 {
		return nil
	}

	// Perform IMDCT using pre-computed cosine table
	output := make([]float64, m.n)
	n := m.n
	scale := 2.0 / float64(n)

	for i := 0; i < n; i++ {
		sum := 0.0
		// Access cosine values: cosTable[k*n + i] for each k
		for k := 0; k < n2; k++ {
			sum += input[k] * m.cosTable[k*n+i]
		}
		// Apply window and scale
		output[i] = sum * scale * m.window[i]
	}

	return output
}

// InverseInto performs the inverse MDCT transform into a pre-allocated output slice.
// This avoids allocations when the output buffer can be reused.
//
// Returns the number of samples written, or 0 if input/output sizes are wrong.
func (m *MDCT) InverseInto(input, out []float64) int {
	n2 := m.n / 2
	if len(input) != n2 || len(out) < m.n {
		return 0
	}

	n := m.n
	scale := 2.0 / float64(n)

	for i := 0; i < n; i++ {
		sum := 0.0
		// Access cosine values: cosTable[k*n + i] for each k
		for k := 0; k < n2; k++ {
			sum += input[k] * m.cosTable[k*n+i]
		}
		out[i] = sum * scale * m.window[i]
	}

	return n
}

// Window returns a copy of the pre-computed window coefficients.
// This is useful for external analysis or debugging.
func (m *MDCT) Window() []float64 {
	w := make([]float64, m.n)
	copy(w, m.window)
	return w
}

// OverlapAdd combines two consecutive IMDCT outputs to reconstruct the original signal.
//
// prev: The IMDCT output from the previous frame (N samples)
// curr: The IMDCT output from the current frame (N samples)
// out: Output buffer for N/2 reconstructed samples
//
// The overlap region is N/2 samples. For the first frame, prev should be zeros.
// Returns the number of samples written, or 0 if sizes are wrong.
func (m *MDCT) OverlapAdd(prev, curr, out []float64) int {
	if len(prev) != m.n || len(curr) != m.n || len(out) < m.n/2 {
		return 0
	}

	n2 := m.n / 2

	// The second half of prev overlaps with the first half of curr
	for i := 0; i < n2; i++ {
		out[i] = prev[n2+i] + curr[i]
	}

	return n2
}
