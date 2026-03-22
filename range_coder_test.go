package magnum

import (
	"testing"
)

// TestRangeEncoderBasic verifies basic single-symbol encoding/decoding.
func TestRangeEncoderBasic(t *testing.T) {
	t.Parallel()

	// Simple uniform distribution test - just verify no panics and basic structure.
	enc := NewRangeEncoder()
	enc.Encode(0, 1, 4)
	encoded := enc.Bytes()

	if len(encoded) == 0 {
		t.Error("Bytes() returned empty slice")
	}

	dec := NewRangeDecoder(encoded)
	fs := dec.Decode(4)

	// Just verify it returns something in valid range.
	if fs > 4 {
		t.Errorf("decoded fs=%d exceeds ft=4", fs)
	}
}

// TestRangeEncoderNew verifies encoder initialization.
func TestRangeEncoderNew(t *testing.T) {
	t.Parallel()

	enc := NewRangeEncoder()
	if enc == nil {
		t.Error("NewRangeEncoder() returned nil")
	}
	if enc.rng != 0x100000000 {
		t.Errorf("initial range = %x, want %x", enc.rng, 0x100000000)
	}
}

// TestRangeDecoderNew verifies decoder initialization.
func TestRangeDecoderNew(t *testing.T) {
	t.Parallel()

	data := []byte{0x12, 0x34, 0x56, 0x78}
	dec := NewRangeDecoder(data)
	if dec == nil {
		t.Error("NewRangeDecoder() returned nil")
	}
	if dec.rng != 0x100000000 {
		t.Errorf("initial range = %x, want %x", dec.rng, 0x100000000)
	}
}

// TestRangeCoderRoundTripUniform tests round-trip encoding/decoding with uniform distribution.
func TestRangeCoderRoundTripUniform(t *testing.T) {
	t.Parallel()

	// Test with uniform distribution (all symbols equally likely)
	symbols := []uint32{0, 1, 2, 3, 0, 1, 2, 3, 0, 2, 1, 3}
	ft := uint32(4)

	enc := NewRangeEncoder()
	for _, s := range symbols {
		enc.Encode(s, s+1, ft)
	}
	encoded := enc.Bytes()

	dec := NewRangeDecoder(encoded)
	for i, want := range symbols {
		fs := dec.Decode(ft)
		dec.Update(fs, fs+1, ft)
		if fs != want {
			t.Errorf("symbol %d: got %d, want %d", i, fs, want)
		}
	}
}

// TestRangeCoderRoundTripSkewed tests round-trip with skewed probability distribution.
func TestRangeCoderRoundTripSkewed(t *testing.T) {
	t.Parallel()

	// Skewed distribution: f[0]=5, f[1]=2, f[2]=1, ft=8
	// fl[0]=0, fh[0]=5, fl[1]=5, fh[1]=7, fl[2]=7, fh[2]=8
	fls := []uint32{0, 5, 7}
	fhs := []uint32{5, 7, 8}
	ft := uint32(8)

	symbols := []uint32{0, 0, 0, 1, 0, 0, 2, 0, 1, 2, 0, 0}

	enc := NewRangeEncoder()
	for _, s := range symbols {
		enc.Encode(fls[s], fhs[s], ft)
	}
	encoded := enc.Bytes()

	dec := NewRangeDecoder(encoded)
	for i, want := range symbols {
		fs := dec.Decode(ft)
		// Map fs to symbol
		var got uint32
		for k := uint32(0); k < 3; k++ {
			if fs >= fls[k] && fs < fhs[k] {
				got = k
				break
			}
		}
		dec.Update(fls[got], fhs[got], ft)
		if got != want {
			t.Errorf("symbol %d: got %d, want %d (fs=%d)", i, got, want, fs)
		}
	}
}

// TestRangeCoderRoundTripLargeFt tests with maximum frequency values.
func TestRangeCoderRoundTripLargeFt(t *testing.T) {
	t.Parallel()

	// Large ft close to 16-bit max
	ft := uint32(65535)
	symbols := []uint32{0, 1000, 30000, 65534, 0, 50000}

	enc := NewRangeEncoder()
	for _, s := range symbols {
		enc.Encode(s, s+1, ft)
	}
	encoded := enc.Bytes()

	dec := NewRangeDecoder(encoded)
	for i, want := range symbols {
		fs := dec.Decode(ft)
		dec.Update(fs, fs+1, ft)
		if fs != want {
			t.Errorf("symbol %d: got %d, want %d", i, fs, want)
		}
	}
}

// TestRangeCoderSingleSymbol tests encoding a single symbol.
func TestRangeCoderSingleSymbol(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		symbol uint32
		ft     uint32
	}{
		{"first symbol", 0, 4},
		{"last symbol", 3, 4},
		{"middle symbol", 2, 5},
		{"binary 0", 0, 2},
		{"binary 1", 1, 2},
		{"large ft first", 0, 1000},
		{"large ft last", 999, 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewRangeEncoder()
			enc.Encode(tc.symbol, tc.symbol+1, tc.ft)
			encoded := enc.Bytes()

			dec := NewRangeDecoder(encoded)
			fs := dec.Decode(tc.ft)
			if fs != tc.symbol {
				t.Errorf("got %d, want %d", fs, tc.symbol)
			}
		})
	}
}

// TestRangeCoderBitsRoundTrip tests the EncodeBits/DecodeBits functions.
func TestRangeCoderBitsRoundTrip(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		value uint32
		bits  uint32
	}{
		{0, 1},
		{1, 1},
		{0, 8},
		{255, 8},
		{127, 8},
		{0, 16},
		{65535, 16},
		{12345, 16},
		{0, 24},
		{0xFFFFFF, 24},
		{0x123456, 24},
	}

	for _, tc := range testCases {
		enc := NewRangeEncoder()
		enc.EncodeBits(tc.value, tc.bits)
		encoded := enc.Bytes()

		dec := NewRangeDecoder(encoded)
		got := dec.DecodeBits(tc.bits)
		if got != tc.value {
			t.Errorf("EncodeBits/DecodeBits(%d, %d bits): got %d, want %d",
				tc.value, tc.bits, got, tc.value)
		}
	}
}

// TestRangeCoderLogPRoundTrip tests the EncodeLogP/DecodeLogP functions.
func TestRangeCoderLogPRoundTrip(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		value int
		logp  uint
	}{
		{0, 1},
		{1, 1},
		{0, 4},
		{1, 4},
		{0, 8},
		{1, 8},
	}

	for _, tc := range testCases {
		enc := NewRangeEncoder()
		enc.EncodeLogP(tc.value, tc.logp)
		encoded := enc.Bytes()

		dec := NewRangeDecoder(encoded)
		got := dec.DecodeLogP(tc.logp)
		if got != tc.value {
			t.Errorf("EncodeLogP/DecodeLogP(%d, logp=%d): got %d, want %d",
				tc.value, tc.logp, got, tc.value)
		}
	}
}

// TestRangeCoderMixedSequence tests a mixed sequence of encoding methods.
func TestRangeCoderMixedSequence(t *testing.T) {
	t.Parallel()

	enc := NewRangeEncoder()

	// Mix of different encoding methods
	enc.Encode(2, 3, 8)       // Symbol 2 out of 8
	enc.EncodeBits(0xAB, 8)   // 8 raw bits
	enc.EncodeLogP(1, 4)      // Binary 1 with logp=4
	enc.Encode(5, 6, 16)      // Symbol 5 out of 16
	enc.EncodeBits(0x123, 12) // 12 raw bits
	enc.EncodeLogP(0, 2)      // Binary 0 with logp=2

	encoded := enc.Bytes()

	dec := NewRangeDecoder(encoded)

	// Decode in same order
	fs := dec.Decode(8)
	dec.Update(fs, fs+1, 8)
	if fs != 2 {
		t.Errorf("symbol 0: got %d, want 2", fs)
	}

	bits := dec.DecodeBits(8)
	if bits != 0xAB {
		t.Errorf("bits 0: got %x, want %x", bits, 0xAB)
	}

	b := dec.DecodeLogP(4)
	if b != 1 {
		t.Errorf("logp 0: got %d, want 1", b)
	}

	fs = dec.Decode(16)
	dec.Update(fs, fs+1, 16)
	if fs != 5 {
		t.Errorf("symbol 1: got %d, want 5", fs)
	}

	bits = dec.DecodeBits(12)
	if bits != 0x123 {
		t.Errorf("bits 1: got %x, want %x", bits, 0x123)
	}

	b = dec.DecodeLogP(2)
	if b != 0 {
		t.Errorf("logp 1: got %d, want 0", b)
	}
}

// TestRangeEncoderReset verifies the Reset function.
func TestRangeEncoderReset(t *testing.T) {
	t.Parallel()

	enc := NewRangeEncoder()
	enc.Encode(1, 2, 4)
	enc.Encode(2, 3, 4)
	first := enc.Bytes()

	enc.Reset()
	enc.Encode(1, 2, 4)
	enc.Encode(2, 3, 4)
	second := enc.Bytes()

	if len(first) != len(second) {
		t.Errorf("Reset: lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("Reset: byte %d differs: %x vs %x", i, first[i], second[i])
		}
	}
}

// BenchmarkRangeEncoder measures encoding performance.
func BenchmarkRangeEncoder(b *testing.B) {
	enc := NewRangeEncoder()
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		enc.Reset()
		// Encode 100 symbols.
		for j := 0; j < 100; j++ {
			enc.Encode(uint32(j%16), uint32((j%16)+1), 16)
		}
		_ = enc.Bytes()
	}
}

// BenchmarkRangeDecoder measures decoding performance.
func BenchmarkRangeDecoder(b *testing.B) {
	// Create encoded data.
	enc := NewRangeEncoder()
	for j := 0; j < 100; j++ {
		enc.Encode(uint32(j%16), uint32((j%16)+1), 16)
	}
	encoded := enc.Bytes()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		dec := NewRangeDecoder(encoded)
		for j := 0; j < 100; j++ {
			fs := dec.Decode(16)
			dec.Update(fs, fs+1, 16)
		}
	}
}
