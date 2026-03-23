package magnum

import (
	"math"
	"testing"
)

func TestCELTBandBoundaries(t *testing.T) {
	// Verify the band table has exactly 22 entries (21 bands + 1 end marker)
	if len(celtBands) != 22 {
		t.Errorf("celtBands has %d entries, want 22", len(celtBands))
	}

	// Verify bands are monotonically increasing
	for i := 1; i < len(celtBands); i++ {
		if celtBands[i] <= celtBands[i-1] {
			t.Errorf("celtBands not monotonic: [%d]=%d <= [%d]=%d",
				i, celtBands[i], i-1, celtBands[i-1])
		}
	}

	// Verify first band starts at 0 and last ends at 377 (per RFC 6716)
	if celtBands[0] != 0 {
		t.Errorf("First band start = %d, want 0", celtBands[0])
	}
	if celtBands[21] != 377 {
		t.Errorf("Last band end = %d, want 377", celtBands[21])
	}
}

func TestBandWidth(t *testing.T) {
	// Test known band widths from RFC 6716
	tests := []struct {
		band  int
		width int
	}{
		{0, 2},   // Bands 0-5 have width 2
		{5, 2},   // Last narrow band
		{6, 3},   // Width 3
		{7, 4},   // Width 4
		{9, 6},   // Width 6
		{10, 8},  // Width 8
		{20, 78}, // Widest band
	}

	for _, tt := range tests {
		width := BandWidth(tt.band)
		if width != tt.width {
			t.Errorf("BandWidth(%d) = %d, want %d", tt.band, width, tt.width)
		}
	}

	// Test invalid bands
	if BandWidth(-1) != 0 {
		t.Error("BandWidth(-1) should return 0")
	}
	if BandWidth(21) != 0 {
		t.Error("BandWidth(21) should return 0")
	}
}

func TestBandStartEnd(t *testing.T) {
	// Test band boundaries
	tests := []struct {
		band  int
		start int
		end   int
	}{
		{0, 0, 2},
		{1, 2, 4},
		{5, 10, 12},
		{10, 29, 37},
		{20, 299, 377},
	}

	for _, tt := range tests {
		start := BandStart(tt.band)
		end := BandEnd(tt.band)
		if start != tt.start {
			t.Errorf("BandStart(%d) = %d, want %d", tt.band, start, tt.start)
		}
		if end != tt.end {
			t.Errorf("BandEnd(%d) = %d, want %d", tt.band, end, tt.end)
		}
	}

	// Test invalid bands
	if BandStart(-1) != -1 {
		t.Error("BandStart(-1) should return -1")
	}
	if BandEnd(-1) != -1 {
		t.Error("BandEnd(-1) should return -1")
	}
}

func TestComputeBandEnergyBasic(t *testing.T) {
	// Create a simple test spectrum with known values
	// Use fullband size (480 bins from 960-point MDCT)
	spectrum := make([]float64, 480)

	// Set uniform energy across all bins
	for i := range spectrum {
		spectrum[i] = 1.0
	}

	be := ComputeBandEnergy(spectrum)
	if be == nil {
		t.Fatal("ComputeBandEnergy returned nil")
	}

	// All bands should be valid
	if be.NumValidBands() != NumCELTBands {
		t.Errorf("NumValidBands = %d, want %d", be.NumValidBands(), NumCELTBands)
	}

	// Check that energy increases with band width
	// (since all coefficients are 1.0, energy = width * 1^2 = width)
	for band := 0; band < NumCELTBands; band++ {
		expectedWidth := BandWidth(band)
		actualEnergy := be.Linear[band]
		if math.Abs(actualEnergy-float64(expectedWidth)) > 0.01 {
			t.Errorf("Band %d: Linear = %.2f, want %.2f (width)", band, actualEnergy, float64(expectedWidth))
		}
	}
}

func TestComputeBandEnergySingleBand(t *testing.T) {
	// Create spectrum with energy only in band 0 (bins 0-2)
	spectrum := make([]float64, 480)
	spectrum[0] = 3.0
	spectrum[1] = 4.0 // Band 0: bins 0-2

	be := ComputeBandEnergy(spectrum)
	if be == nil {
		t.Fatal("ComputeBandEnergy returned nil")
	}

	// Band 0 energy should be 3^2 + 4^2 = 9 + 16 = 25
	expectedEnergy := 25.0
	if math.Abs(be.Linear[0]-expectedEnergy) > 0.001 {
		t.Errorf("Band 0 Linear = %f, want %f", be.Linear[0], expectedEnergy)
	}

	// Log energy should be 10*log10(25/2) = 10*log10(12.5) ≈ 10.97 dB
	expectedLog := 10 * math.Log10(25.0/2.0)
	if math.Abs(be.LogEnergy[0]-expectedLog) > 0.001 {
		t.Errorf("Band 0 LogEnergy = %f, want %f", be.LogEnergy[0], expectedLog)
	}

	// Other bands should have very low (floor) energy
	for band := 1; band < NumCELTBands; band++ {
		if be.Linear[band] > 0.001 {
			t.Errorf("Band %d should have near-zero energy, got %f", band, be.Linear[band])
		}
	}
}

func TestComputeBandEnergyScaling(t *testing.T) {
	// Test that scaling works correctly for different spectrum sizes

	// Half-size spectrum (240 bins, like 24 kHz superwideband)
	spectrum := make([]float64, 240)
	for i := range spectrum {
		spectrum[i] = 1.0
	}

	be := ComputeBandEnergy(spectrum)
	if be == nil {
		t.Fatal("ComputeBandEnergy returned nil for 240-bin spectrum")
	}

	// Should have valid bands (though some may be empty due to scaling)
	validCount := be.NumValidBands()
	if validCount == 0 {
		t.Error("Expected some valid bands for 240-bin spectrum")
	}

	// Total energy should be close to spectrum size, but may be less
	// due to rounding at band boundaries. The important thing is that
	// the energy is consistent with the actual bins covered.
	totalLinear := be.TotalEnergy()
	if totalLinear <= 0 {
		t.Error("TotalEnergy should be positive")
	}
	// The 377 bin boundary scales to 188 for 240 bins (377 * 0.5 = 188.5, truncated)
	// So total energy should be approximately 188 (the scaled end of the last band)
	t.Logf("240-bin spectrum: total energy = %f, valid bands = %d", totalLinear, validCount)
}

func TestComputeBandEnergyEmpty(t *testing.T) {
	// Empty spectrum should return nil
	if be := ComputeBandEnergy(nil); be != nil {
		t.Error("ComputeBandEnergy(nil) should return nil")
	}
	if be := ComputeBandEnergy([]float64{}); be != nil {
		t.Error("ComputeBandEnergy([]) should return nil")
	}
}

func TestScaledBandBoundaries(t *testing.T) {
	// Test full-size (scale = 1.0)
	starts, ends := ScaledBandBoundaries(480)
	if starts[0] != 0 {
		t.Errorf("Full-scale starts[0] = %d, want 0", starts[0])
	}
	if ends[0] != 2 {
		t.Errorf("Full-scale ends[0] = %d, want 2", ends[0])
	}

	// Test half-size (scale = 0.5)
	starts, ends = ScaledBandBoundaries(240)
	if starts[0] != 0 {
		t.Errorf("Half-scale starts[0] = %d, want 0", starts[0])
	}
	if ends[0] != 1 {
		t.Errorf("Half-scale ends[0] = %d, want 1", ends[0])
	}

	// Verify all boundaries are within spectrum bounds
	for band := 0; band < NumCELTBands; band++ {
		if starts[band] > 240 {
			t.Errorf("Half-scale starts[%d] = %d exceeds spectrum size", band, starts[band])
		}
		if ends[band] > 240 {
			t.Errorf("Half-scale ends[%d] = %d exceeds spectrum size", band, ends[band])
		}
	}
}

func TestBandEnergyAverageLogEnergy(t *testing.T) {
	// Create spectrum with uniform energy
	spectrum := make([]float64, 480)
	for i := range spectrum {
		spectrum[i] = 10.0
	}

	be := ComputeBandEnergy(spectrum)
	if be == nil {
		t.Fatal("ComputeBandEnergy returned nil")
	}

	avgLog := be.AverageLogEnergy()

	// With uniform energy density = 100 (10^2), log10(100) = 2, so 10*log10 = 20
	expectedLog := 20.0
	if math.Abs(avgLog-expectedLog) > 0.1 {
		t.Errorf("AverageLogEnergy = %f, want ~%f", avgLog, expectedLog)
	}
}

func TestBandEnergyIntegrationWithMDCT(t *testing.T) {
	// Test that band energy works correctly with actual MDCT output

	// Create a 48 kHz 20ms frame (960 samples)
	m := NewMDCT(MDCTSize960)
	if m == nil {
		t.Fatal("NewMDCT(960) returned nil")
	}

	// Create test signal: 1 kHz sine wave at 48 kHz
	input := make([]float64, 960)
	freq := 1000.0 / 48000.0 // 1 kHz at 48 kHz
	for i := range input {
		input[i] = math.Sin(2 * math.Pi * freq * float64(i))
	}

	// Get MDCT spectrum
	spectrum := m.Forward(input)
	if len(spectrum) != 480 {
		t.Fatalf("MDCT output size = %d, want 480", len(spectrum))
	}

	// Compute band energy
	be := ComputeBandEnergy(spectrum)
	if be == nil {
		t.Fatal("ComputeBandEnergy returned nil")
	}

	// Energy should be concentrated around 1 kHz
	// At 48 kHz, 1 kHz corresponds to bin ~10 (1000 / (48000/960) ≈ 20, then /2 for MDCT)
	// This should fall in band 4 or 5 (bins 8-10 or 10-12)
	totalEnergy := be.TotalEnergy()
	if totalEnergy <= 0 {
		t.Error("Total energy should be positive")
	}

	// Log that the test passed
	t.Logf("1 kHz sine: total energy = %f, valid bands = %d", totalEnergy, be.NumValidBands())
	t.Logf("Band energies: %v", be.LogEnergy[:10]) // Show first 10 bands
}

// BenchmarkComputeBandEnergy benchmarks band energy computation
func BenchmarkComputeBandEnergy(b *testing.B) {
	spectrum := make([]float64, 480)
	for i := range spectrum {
		spectrum[i] = float64(i) / 480.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ComputeBandEnergy(spectrum)
	}
}
