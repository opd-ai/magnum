// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements Linear Predictive Coding (LPC) analysis as specified
// in RFC 6716 §4.2 for the SILK codec. LPC is used to model the vocal tract
// and compress speech by predicting samples from their linear combination
// with previous samples.
//
// Key components:
// - Autocorrelation computation for LPC analysis
// - Levinson-Durbin recursion for efficient coefficient estimation
// - Reflection coefficient calculation for stability analysis

package magnum

import (
	"math"
)

// LPCOrder is the default LPC order for SILK narrowband/wideband.
// RFC 6716 specifies order 10 for narrowband and 16 for wideband.
const (
	LPCOrderNarrowband = 10
	LPCOrderWideband   = 16
)

// LPCAnalyzer performs Linear Predictive Coding analysis on audio frames.
type LPCAnalyzer struct {
	order int
	// Pre-allocated buffers to reduce allocations
	autocorr []float64
	coeffs   []float64
	refl     []float64
}

// NewLPCAnalyzer creates a new LPC analyzer for the given order.
// Order must be positive and typically ranges from 10-16 for speech.
func NewLPCAnalyzer(order int) *LPCAnalyzer {
	if order <= 0 {
		order = LPCOrderNarrowband
	}
	return &LPCAnalyzer{
		order:    order,
		autocorr: make([]float64, order+1),
		coeffs:   make([]float64, order+1),
		refl:     make([]float64, order),
	}
}

// LPCResult holds the results of LPC analysis.
type LPCResult struct {
	// Coefficients are the LPC coefficients a[1..order].
	// a[0] is always 1.0 and is not stored.
	Coefficients []float64

	// ReflectionCoeffs are the reflection coefficients (PARCOR).
	// These are bounded in [-1, 1] for stable filters.
	ReflectionCoeffs []float64

	// PredictionError is the residual energy after prediction.
	PredictionError float64

	// Gain is the prediction gain (input energy / prediction error).
	Gain float64
}

// Analyze performs LPC analysis on the input samples using the
// autocorrelation method with Levinson-Durbin recursion.
//
// The input should be a windowed frame of audio samples.
// Returns LPC coefficients, reflection coefficients, and prediction error.
func (a *LPCAnalyzer) Analyze(samples []float64) *LPCResult {
	if len(samples) == 0 {
		return &LPCResult{
			Coefficients:     make([]float64, a.order),
			ReflectionCoeffs: make([]float64, a.order),
			PredictionError:  0,
			Gain:             1,
		}
	}

	// Step 1: Compute autocorrelation
	a.computeAutocorrelation(samples)

	// Step 2: Apply Levinson-Durbin recursion
	predError := a.levinsonDurbin()

	// Compute gain
	gain := 1.0
	if predError > 0 && a.autocorr[0] > 0 {
		gain = a.autocorr[0] / predError
	}

	// Copy results to output
	result := &LPCResult{
		Coefficients:     make([]float64, a.order),
		ReflectionCoeffs: make([]float64, a.order),
		PredictionError:  predError,
		Gain:             gain,
	}
	copy(result.Coefficients, a.coeffs[1:a.order+1])
	copy(result.ReflectionCoeffs, a.refl)

	return result
}

// computeAutocorrelation computes the autocorrelation sequence r[0..order].
// r[k] = sum(samples[i] * samples[i+k]) for i = 0 to N-1-k
func (a *LPCAnalyzer) computeAutocorrelation(samples []float64) {
	n := len(samples)

	for k := 0; k <= a.order; k++ {
		sum := 0.0
		for i := 0; i < n-k; i++ {
			sum += samples[i] * samples[i+k]
		}
		a.autocorr[k] = sum
	}

	// Add small regularization to avoid division by zero
	if a.autocorr[0] < 1e-10 {
		a.autocorr[0] = 1e-10
	}
}

// levinsonDurbin implements the Levinson-Durbin recursion to solve
// the Yule-Walker equations efficiently in O(order²) time.
//
// Returns the final prediction error energy.
func (a *LPCAnalyzer) levinsonDurbin() float64 {
	order := a.order
	r := a.autocorr

	// Initialize
	for i := range a.coeffs {
		a.coeffs[i] = 0
	}
	a.coeffs[0] = 1.0

	e := r[0] // Prediction error energy

	// Temporary buffer for coefficient update
	tmp := make([]float64, order+1)

	for i := 1; i <= order; i++ {
		// Compute reflection coefficient (PARCOR)
		lambda := r[i]
		for j := 1; j < i; j++ {
			lambda += a.coeffs[j] * r[i-j]
		}

		if e <= 0 {
			// Signal is not invertible; stop iteration
			break
		}

		lambda = -lambda / e
		a.refl[i-1] = lambda

		// Check stability: |lambda| must be < 1
		if math.Abs(lambda) >= 1.0 {
			// Clamp to ensure stability
			if lambda > 0 {
				lambda = 0.9999
			} else {
				lambda = -0.9999
			}
			a.refl[i-1] = lambda
		}

		// Update coefficients using the recurrence relation:
		// a_new[j] = a[j] + lambda * a[i-j]
		tmp[i] = lambda
		for j := 1; j < i; j++ {
			tmp[j] = a.coeffs[j] + lambda*a.coeffs[i-j]
		}
		for j := 1; j <= i; j++ {
			a.coeffs[j] = tmp[j]
		}

		// Update prediction error
		e *= 1.0 - lambda*lambda
	}

	return e
}

// Autocorrelation computes the autocorrelation of input samples up to
// the specified lag. This is a standalone function for general use.
func Autocorrelation(samples []float64, maxLag int) []float64 {
	if len(samples) == 0 || maxLag < 0 {
		return nil
	}

	n := len(samples)
	if maxLag >= n {
		maxLag = n - 1
	}

	result := make([]float64, maxLag+1)
	for k := 0; k <= maxLag; k++ {
		sum := 0.0
		for i := 0; i < n-k; i++ {
			sum += samples[i] * samples[i+k]
		}
		result[k] = sum
	}

	return result
}

// ApplyLPCFilter applies the LPC filter to compute the prediction residual.
// residual[n] = input[n] - sum(coeffs[k] * input[n-k]) for k = 1..order
func ApplyLPCFilter(input, coeffs []float64) []float64 {
	if len(input) == 0 {
		return nil
	}

	order := len(coeffs)
	output := make([]float64, len(input))

	for n := range input {
		prediction := 0.0
		for k := 1; k <= order && k <= n; k++ {
			prediction += coeffs[k-1] * input[n-k]
		}
		output[n] = input[n] - prediction
	}

	return output
}

// SynthesizeLPC reconstructs the signal from residual and LPC coefficients.
// output[n] = residual[n] + sum(coeffs[k] * output[n-k]) for k = 1..order
func SynthesizeLPC(residual, coeffs []float64) []float64 {
	if len(residual) == 0 {
		return nil
	}

	order := len(coeffs)
	output := make([]float64, len(residual))

	for n := range residual {
		prediction := 0.0
		for k := 1; k <= order && k <= n; k++ {
			prediction += coeffs[k-1] * output[n-k]
		}
		output[n] = residual[n] + prediction
	}

	return output
}

// ApplyHammingWindow applies a Hamming window to the input samples.
// This is commonly used before LPC analysis to reduce spectral leakage.
func ApplyHammingWindow(samples []float64) []float64 {
	n := len(samples)
	if n == 0 {
		return nil
	}

	output := make([]float64, n)
	for i := range samples {
		// Hamming window: w[n] = 0.54 - 0.46 * cos(2*pi*n/(N-1))
		w := 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		output[i] = samples[i] * w
	}

	return output
}

// ComputeLPCGain computes the gain factor for the LPC filter.
// This is the square root of the prediction error energy.
func ComputeLPCGain(predictionError float64) float64 {
	if predictionError <= 0 {
		return 0
	}
	return math.Sqrt(predictionError)
}
