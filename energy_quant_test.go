package magnum

import (
	"math"
	"testing"
)

func TestNewEnergyQuantizer(t *testing.T) {
	tests := []struct {
		name string
		lm   int
		want int
	}{
		{"LM=0 (2.5ms)", 0, 0},
		{"LM=1 (5ms)", 1, 1},
		{"LM=2 (10ms)", 2, 2},
		{"LM=3 (20ms)", 3, 3},
		{"LM=-1 (clamp to 0)", -1, 0},
		{"LM=5 (clamp to 3)", 5, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eq := NewEnergyQuantizer(tt.lm)
			if eq == nil {
				t.Fatal("NewEnergyQuantizer returned nil")
			}
			if eq.lm != tt.want {
				t.Errorf("lm = %d, want %d", eq.lm, tt.want)
			}
			if eq.hasHistory {
				t.Error("New quantizer should not have history")
			}
		})
	}
}

func TestQuantizeCoarseBasic(t *testing.T) {
	eq := NewEnergyQuantizer(3) // 20ms frames

	// Create test energies (mean values should quantize to small residuals)
	var logEnergies [NumCELTBands]float64
	for i := 0; i < NumCELTBands; i++ {
		logEnergies[i] = eMeans[i] // Use mean values
	}

	result := eq.QuantizeCoarse(logEnergies, false)
	if result == nil {
		t.Fatal("QuantizeCoarse returned nil")
	}

	// First frame without history should use intra coding
	if !result.Intra {
		t.Error("First frame should use intra coding")
	}

	// Check that reconstructed energies are close to input
	for band := 0; band < NumCELTBands; band++ {
		diff := math.Abs(result.ReconstructedEnergy[band] - logEnergies[band])
		// Coarse quantization error should be at most ~half the step size (3 dB)
		if diff > 4.0 {
			t.Errorf("Band %d: reconstruction error %.2f dB too large", band, diff)
		}
	}
}

func TestQuantizeCoarseInterFrame(t *testing.T) {
	eq := NewEnergyQuantizer(3) // 20ms frames

	// First frame (establishes history)
	var logEnergies1 [NumCELTBands]float64
	for i := 0; i < NumCELTBands; i++ {
		logEnergies1[i] = eMeans[i]
	}
	result1 := eq.QuantizeCoarse(logEnergies1, false)
	if !result1.Intra {
		t.Error("First frame should use intra coding")
	}

	// Second frame (should use inter-frame prediction)
	var logEnergies2 [NumCELTBands]float64
	for i := 0; i < NumCELTBands; i++ {
		logEnergies2[i] = eMeans[i] + 1.0 // Slight increase
	}
	result2 := eq.QuantizeCoarse(logEnergies2, false)
	if result2.Intra {
		t.Error("Second frame should use inter-frame prediction")
	}

	// With prediction, coarse indices should be small
	for band := 0; band < NumCELTBands; band++ {
		qi := result2.CoarseQuant[band]
		if qi < -3 || qi > 3 {
			t.Errorf("Band %d: coarse quant %d unexpectedly large with prediction", band, qi)
		}
	}
}

func TestQuantizeFine(t *testing.T) {
	eq := NewEnergyQuantizer(3)

	// Create energies with known error
	var logEnergies [NumCELTBands]float64
	for i := 0; i < NumCELTBands; i++ {
		logEnergies[i] = eMeans[i] + 2.5 // Deliberately offset from quantization grid
	}

	result := eq.QuantizeCoarse(logEnergies, true)

	// Record coarse errors
	coarseErrors := make([]float64, NumCELTBands)
	for i := 0; i < NumCELTBands; i++ {
		coarseErrors[i] = math.Abs(result.QuantizationError[i])
	}

	// Apply fine quantization with 42 bits (2 bits per band average)
	eq.QuantizeFine(result, 42)

	// Check that fine quantization reduced errors
	for band := 0; band < NumCELTBands; band++ {
		if result.FineBits[band] > 0 {
			fineError := math.Abs(result.QuantizationError[band])
			if fineError > coarseErrors[band] {
				t.Errorf("Band %d: fine error %.3f > coarse error %.3f",
					band, fineError, coarseErrors[band])
			}
		}
	}

	// Check that some bands got fine bits
	totalFineBits := 0
	for band := 0; band < NumCELTBands; band++ {
		totalFineBits += result.FineBits[band]
	}
	if totalFineBits == 0 {
		t.Error("No fine bits were allocated")
	}
}

func TestDequantize(t *testing.T) {
	// Test that dequantize produces the same result as quantize
	eq1 := NewEnergyQuantizer(3)
	eq2 := NewEnergyQuantizer(3)

	// Create test energies
	var logEnergies [NumCELTBands]float64
	for i := 0; i < NumCELTBands; i++ {
		logEnergies[i] = eMeans[i] + float64(i%5)
	}

	// Quantize
	result := eq1.QuantizeCoarse(logEnergies, true)
	eq1.QuantizeFine(result, 42)

	// Dequantize
	dequantized := eq2.Dequantize(
		result.CoarseQuant,
		result.FineQuant,
		result.FineBits,
		result.Intra,
	)

	// Compare results
	for band := 0; band < NumCELTBands; band++ {
		diff := math.Abs(dequantized[band] - result.ReconstructedEnergy[band])
		if diff > 0.01 {
			t.Errorf("Band %d: dequantized %.4f != reconstructed %.4f (diff %.4f)",
				band, dequantized[band], result.ReconstructedEnergy[band], diff)
		}
	}
}

func TestQuantizerReset(t *testing.T) {
	eq := NewEnergyQuantizer(3)

	// Establish history
	var logEnergies [NumCELTBands]float64
	for i := 0; i < NumCELTBands; i++ {
		logEnergies[i] = eMeans[i]
	}
	eq.QuantizeCoarse(logEnergies, false)

	if !eq.hasHistory {
		t.Error("Should have history after first frame")
	}

	// Reset
	eq.Reset()

	if eq.hasHistory {
		t.Error("Should not have history after reset")
	}

	// Next frame should use intra coding
	result := eq.QuantizeCoarse(logEnergies, false)
	if !result.Intra {
		t.Error("Frame after reset should use intra coding")
	}
}

func TestEstimateCoarseBits(t *testing.T) {
	eq := NewEnergyQuantizer(3)

	var logEnergies [NumCELTBands]float64
	for i := 0; i < NumCELTBands; i++ {
		logEnergies[i] = eMeans[i]
	}

	result := eq.QuantizeCoarse(logEnergies, true)
	bits := EstimateCoarseBits(result)

	// Should have at least 1 bit for intra flag plus some for each band
	if bits < NumCELTBands+1 {
		t.Errorf("Estimated bits %d too low", bits)
	}

	// Should not be unreasonably high
	if bits > NumCELTBands*10 {
		t.Errorf("Estimated bits %d too high", bits)
	}
}

func TestComputeTotalFineBits(t *testing.T) {
	tests := []struct {
		budget     int
		coarseBits int
		wantMin    int
		wantMax    int
	}{
		{100, 50, 0, 25},       // Half of remaining
		{50, 60, 0, 0},         // Budget exhausted
		{500, 100, 50, 200},    // Normal case
		{10000, 100, 100, 168}, // Capped by max fine bits
	}

	for _, tt := range tests {
		fine := ComputeTotalFineBits(tt.budget, tt.coarseBits)
		if fine < tt.wantMin || fine > tt.wantMax {
			t.Errorf("ComputeTotalFineBits(%d, %d) = %d, want in [%d, %d]",
				tt.budget, tt.coarseBits, fine, tt.wantMin, tt.wantMax)
		}
	}
}

func TestEnergyQuantRoundTrip(t *testing.T) {
	// Test multiple frames to verify prediction state consistency
	eq := NewEnergyQuantizer(3)

	for frame := 0; frame < 5; frame++ {
		// Create varying energies
		var logEnergies [NumCELTBands]float64
		for i := 0; i < NumCELTBands; i++ {
			logEnergies[i] = eMeans[i] + 5.0*math.Sin(float64(frame+i))
		}

		result := eq.QuantizeCoarse(logEnergies, false)
		eq.QuantizeFine(result, 30)

		// Verify reconstruction error is reasonable
		for band := 0; band < NumCELTBands; band++ {
			err := math.Abs(logEnergies[band] - result.ReconstructedEnergy[band])
			// With coarse + fine, error should be small
			if err > 6.0 { // Less than one coarse step
				t.Errorf("Frame %d, band %d: error %.2f dB too large", frame, band, err)
			}
		}
	}
}

func TestDecayBound(t *testing.T) {
	eq := NewEnergyQuantizer(3)

	// First frame with high energy
	var highEnergy [NumCELTBands]float64
	for i := 0; i < NumCELTBands; i++ {
		highEnergy[i] = 20.0 // High energy
	}
	result1 := eq.QuantizeCoarse(highEnergy, false)

	// Second frame with very low energy
	var lowEnergy [NumCELTBands]float64
	for i := 0; i < NumCELTBands; i++ {
		lowEnergy[i] = -20.0 // Very low energy (40 dB drop)
	}
	result := eq.QuantizeCoarse(lowEnergy, false)

	// Decay should be limited by maxDecayDB (plus some tolerance for quantization)
	// The decay bound prevents the energy from dropping too fast
	for band := 0; band < NumCELTBands; band++ {
		prevEnergy := result1.ReconstructedEnergy[band]
		currEnergy := result.ReconstructedEnergy[band]
		drop := prevEnergy - currEnergy

		// The decay mechanism limits drop, but with inter-frame prediction,
		// we allow up to maxDecayDB + coarseQuantStep + some tolerance for prediction effects
		maxAllowedDrop := maxDecayDB + coarseQuantStep + 6.0
		if drop > maxAllowedDrop {
			t.Errorf("Band %d: drop %.2f dB exceeds max allowed %.2f", band, drop, maxAllowedDrop)
		}
		// Energy should still be above floor
		if currEnergy < energyFloorDB-0.1 {
			t.Errorf("Band %d: energy %.2f below floor %.2f", band, currEnergy, energyFloorDB)
		}
	}
}

func BenchmarkQuantizeCoarse(b *testing.B) {
	eq := NewEnergyQuantizer(3)

	var logEnergies [NumCELTBands]float64
	for i := 0; i < NumCELTBands; i++ {
		logEnergies[i] = eMeans[i] + float64(i%5)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eq.QuantizeCoarse(logEnergies, i%10 == 0) // Mix of intra and inter
	}
}

func BenchmarkQuantizeFine(b *testing.B) {
	eq := NewEnergyQuantizer(3)

	var logEnergies [NumCELTBands]float64
	for i := 0; i < NumCELTBands; i++ {
		logEnergies[i] = eMeans[i] + float64(i%5)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := eq.QuantizeCoarse(logEnergies, false)
		eq.QuantizeFine(result, 42)
	}
}
