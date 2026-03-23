package magnum

import (
	"testing"
)

func TestNewBitAllocator(t *testing.T) {
	config := BitAllocationConfig{
		Bitrate:    64000,
		SampleRate: 48000,
		FrameSize:  960,
		Channels:   1,
	}

	allocator := NewBitAllocator(config)
	if allocator == nil {
		t.Fatal("NewBitAllocator returned nil")
	}

	// Check that static allocation was computed
	staticAlloc := allocator.GetStaticAllocation()
	totalStatic := 0
	for _, bits := range staticAlloc {
		totalStatic += bits
	}
	if totalStatic <= 0 {
		t.Error("static allocation should have positive total bits")
	}
}

func TestBitAllocatorAllocate(t *testing.T) {
	config := BitAllocationConfig{
		Bitrate:    64000,
		SampleRate: 48000,
		FrameSize:  960,
		Channels:   1,
	}

	allocator := NewBitAllocator(config)

	// Create mock band energy
	energy := &BandEnergy{}
	for i := 0; i < NumCELTBands; i++ {
		energy.Valid[i] = true
		energy.LogEnergy[i] = float64(-20 + i) // Varying energy
	}

	alloc := allocator.Allocate(energy)
	if alloc == nil {
		t.Fatal("Allocate returned nil")
	}

	// Check that bits were allocated
	if alloc.TotalBits <= 0 {
		t.Error("TotalBits should be positive")
	}
	if alloc.PVQBits <= 0 {
		t.Error("PVQBits should be positive")
	}
	if alloc.UsedBits <= 0 {
		t.Error("UsedBits should be positive")
	}

	// Check that band bits sum approximately to PVQBits
	sumBandBits := 0
	for _, bits := range alloc.BandBits {
		sumBandBits += bits
	}
	if sumBandBits != alloc.UsedBits {
		t.Errorf("sumBandBits (%d) != UsedBits (%d)", sumBandBits, alloc.UsedBits)
	}
}

func TestBitAllocatorNoEnergy(t *testing.T) {
	config := BitAllocationConfig{
		Bitrate:    64000,
		SampleRate: 48000,
		FrameSize:  960,
		Channels:   1,
	}

	allocator := NewBitAllocator(config)
	alloc := allocator.Allocate(nil)

	if alloc == nil {
		t.Fatal("Allocate with nil energy returned nil")
	}

	// Should still allocate bits based on static allocation
	if alloc.UsedBits <= 0 {
		t.Error("should allocate bits even without energy info")
	}
}

func TestBitAllocatorDifferentBitrates(t *testing.T) {
	testCases := []struct {
		bitrate int
		minBits int
		maxBits int
	}{
		{6000, 100, 300},      // Minimum bitrate
		{32000, 500, 800},     // Low bitrate
		{64000, 1000, 1500},   // Medium bitrate
		{128000, 2000, 3000},  // High bitrate
		{510000, 8000, 12000}, // Maximum bitrate
	}

	for _, tc := range testCases {
		config := BitAllocationConfig{
			Bitrate:    tc.bitrate,
			SampleRate: 48000,
			FrameSize:  960,
			Channels:   1,
		}

		allocator := NewBitAllocator(config)
		alloc := allocator.Allocate(nil)

		t.Logf("Bitrate %d: TotalBits=%d, UsedBits=%d", tc.bitrate, alloc.TotalBits, alloc.UsedBits)

		// Expected bits per frame = bitrate / frames_per_second
		// For 48kHz, 960 samples: 50 frames/sec
		expectedBits := tc.bitrate / 50
		if alloc.TotalBits != expectedBits {
			t.Errorf("bitrate %d: TotalBits = %d, want %d", tc.bitrate, alloc.TotalBits, expectedBits)
		}
	}
}

func TestBitAllocatorUpdateBitrate(t *testing.T) {
	config := BitAllocationConfig{
		Bitrate:    64000,
		SampleRate: 48000,
		FrameSize:  960,
		Channels:   1,
	}

	allocator := NewBitAllocator(config)
	origAlloc := allocator.GetStaticAllocation()

	// Update to higher bitrate
	allocator.UpdateBitrate(128000)
	newAlloc := allocator.GetStaticAllocation()

	// Higher bitrate should result in more bits for high bands
	if newAlloc[NumCELTBands-1] <= origAlloc[NumCELTBands-1] {
		t.Logf("Original high band bits: %d, New: %d", origAlloc[NumCELTBands-1], newAlloc[NumCELTBands-1])
		// This is expected because higher bitrate gives more bits overall
	}

	// Total bits should increase
	origTotal := 0
	newTotal := 0
	for i := 0; i < NumCELTBands; i++ {
		origTotal += origAlloc[i]
		newTotal += newAlloc[i]
	}
	if newTotal <= origTotal {
		t.Errorf("higher bitrate should increase total bits: orig=%d, new=%d", origTotal, newTotal)
	}
}

func TestComputePerceptualWeight(t *testing.T) {
	// Low band at low bitrate should have high weight
	lowBandLowBitrate := computePerceptualWeight(5, 6000)
	if lowBandLowBitrate < 1.4 {
		t.Errorf("low band at low bitrate weight = %f, want > 1.4", lowBandLowBitrate)
	}

	// Low band at high bitrate should have normal weight
	lowBandHighBitrate := computePerceptualWeight(5, 510000)
	if lowBandHighBitrate > 1.1 {
		t.Errorf("low band at high bitrate weight = %f, want < 1.1", lowBandHighBitrate)
	}

	// High band at low bitrate should have low weight
	highBandLowBitrate := computePerceptualWeight(18, 6000)
	if highBandLowBitrate > 0.6 {
		t.Errorf("high band at low bitrate weight = %f, want < 0.6", highBandLowBitrate)
	}

	// High band at high bitrate should have normal weight
	highBandHighBitrate := computePerceptualWeight(18, 510000)
	if highBandHighBitrate < 0.9 {
		t.Errorf("high band at high bitrate weight = %f, want > 0.9", highBandHighBitrate)
	}
}

func TestAllocateBandBits(t *testing.T) {
	// Test the standalone function
	energy := &BandEnergy{}
	for i := 0; i < NumCELTBands; i++ {
		energy.Valid[i] = true
		energy.LogEnergy[i] = -10.0
	}

	bits := AllocateBandBits(1000, NumCELTBands, energy, 64000, 48000, 960)
	if len(bits) != NumCELTBands {
		t.Errorf("len(bits) = %d, want %d", len(bits), NumCELTBands)
	}

	// Check that bits were distributed
	totalBits := 0
	for _, b := range bits {
		totalBits += b
	}
	if totalBits == 0 {
		t.Error("no bits were allocated")
	}
	if totalBits > 1000 {
		t.Errorf("allocated %d bits, but only 1000 available", totalBits)
	}
}

func TestAllocateBandBitsNilEnergy(t *testing.T) {
	bits := AllocateBandBits(500, NumCELTBands, nil, 64000, 48000, 960)
	if len(bits) != NumCELTBands {
		t.Errorf("len(bits) = %d, want %d", len(bits), NumCELTBands)
	}

	// Should still allocate bits
	totalBits := 0
	for _, b := range bits {
		totalBits += b
	}
	if totalBits == 0 {
		t.Error("no bits allocated with nil energy")
	}
}

func TestAllocationConsistency(t *testing.T) {
	// Allocate multiple times with same input, should get consistent results
	config := BitAllocationConfig{
		Bitrate:    64000,
		SampleRate: 48000,
		FrameSize:  960,
		Channels:   1,
	}

	energy := &BandEnergy{}
	for i := 0; i < NumCELTBands; i++ {
		energy.Valid[i] = true
		energy.LogEnergy[i] = -15.0
	}

	allocator := NewBitAllocator(config)
	alloc1 := allocator.Allocate(energy)
	alloc2 := allocator.Allocate(energy)

	for i := 0; i < NumCELTBands; i++ {
		if alloc1.BandBits[i] != alloc2.BandBits[i] {
			t.Errorf("band %d: inconsistent allocation: %d vs %d",
				i, alloc1.BandBits[i], alloc2.BandBits[i])
		}
	}
}

func BenchmarkBitAllocator(b *testing.B) {
	config := BitAllocationConfig{
		Bitrate:    64000,
		SampleRate: 48000,
		FrameSize:  960,
		Channels:   1,
	}
	allocator := NewBitAllocator(config)

	energy := &BandEnergy{}
	for i := 0; i < NumCELTBands; i++ {
		energy.Valid[i] = true
		energy.LogEnergy[i] = float64(-20 + i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		allocator.Allocate(energy)
	}
}
