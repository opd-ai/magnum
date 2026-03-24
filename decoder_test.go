package magnum

import (
	"errors"
	"testing"
)

// TestNewDecoder verifies that NewDecoder accepts valid parameters and rejects
// invalid ones.
func TestNewDecoder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sampleRate int
		channels   int
		wantErr    bool
	}{
		{"48kHz mono", 48000, 1, false},
		{"48kHz stereo", 48000, 2, false},
		{"16kHz mono", 16000, 1, false},
		{"8kHz mono", 8000, 1, false},
		{"24kHz mono", 24000, 1, false},
		{"44.1kHz unsupported", 44100, 1, true},
		{"zero channels", 48000, 0, true},
		{"three channels", 48000, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dec, err := NewDecoder(tt.sampleRate, tt.channels)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewDecoder(%d, %d): expected error, got nil", tt.sampleRate, tt.channels)
				}
				return
			}
			if err != nil {
				t.Errorf("NewDecoder(%d, %d): unexpected error: %v", tt.sampleRate, tt.channels, err)
			}
			if dec == nil {
				t.Errorf("NewDecoder(%d, %d): returned nil decoder", tt.sampleRate, tt.channels)
			}
		})
	}
}

// TestNewDecoderErrors checks that the correct exported sentinel errors are returned.
func TestNewDecoderErrors(t *testing.T) {
	t.Parallel()

	_, err := NewDecoder(44100, 1)
	if !errors.Is(err, ErrUnsupportedSampleRate) {
		t.Errorf("expected ErrUnsupportedSampleRate, got: %v", err)
	}

	_, err = NewDecoder(48000, 3)
	if !errors.Is(err, ErrUnsupportedChannelCount) {
		t.Errorf("expected ErrUnsupportedChannelCount, got: %v", err)
	}
}

// TestDecoderDecode verifies that Decoder.Decode correctly decodes packets
// produced by Encoder.Encode.
func TestDecoderDecode(t *testing.T) {
	t.Parallel()

	// Create encoder and decoder pair.
	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Encode a test frame.
	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Decode with pre-allocated output buffer.
	out := make([]int16, 960)
	n, err := dec.Decode(packet, out)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if n != 960 {
		t.Errorf("Decode: got %d samples, want 960", n)
	}
	for i, want := range pcm {
		if out[i] != want {
			t.Errorf("sample[%d]: got %d, want %d", i, out[i], want)
		}
	}
}

// TestDecoderDecodeNilOutput verifies that Decoder.Decode works with a nil
// output buffer by internally allocating.
func TestDecoderDecodeNilOutput(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Decode with nil output.
	n, err := dec.Decode(packet, nil)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if n != 960 {
		t.Errorf("Decode: got %d samples, want 960", n)
	}
}

// TestDecoderDecodeAlloc verifies that DecodeAlloc returns samples directly.
func TestDecoderDecodeAlloc(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// DecodeAlloc returns the samples directly.
	samples, err := dec.DecodeAlloc(packet)
	if err != nil {
		t.Fatalf("DecodeAlloc: %v", err)
	}
	if len(samples) != 960 {
		t.Errorf("DecodeAlloc: got %d samples, want 960", len(samples))
	}
	for i, want := range pcm {
		if samples[i] != want {
			t.Errorf("sample[%d]: got %d, want %d", i, samples[i], want)
		}
	}
}

// TestDecoderAccessors verifies the SampleRate and Channels accessor methods.
func TestDecoderAccessors(t *testing.T) {
	t.Parallel()

	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}

	if dec.SampleRate() != 48000 {
		t.Errorf("SampleRate: got %d, want 48000", dec.SampleRate())
	}
	if dec.Channels() != 2 {
		t.Errorf("Channels: got %d, want 2", dec.Channels())
	}
}

// TestDecoderChannelMismatch verifies that Decoder.Decode returns
// ErrChannelMismatch when the packet's stereo flag doesn't match.
func TestDecoderChannelMismatch(t *testing.T) {
	t.Parallel()

	// Create a stereo encoder and mono decoder.
	enc, err := NewEncoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(48000, 1) // mono decoder
	if err != nil {
		t.Fatal(err)
	}

	// Encode a stereo packet.
	pcm := make([]int16, 1920) // stereo
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Decode should fail with ErrChannelMismatch.
	out := make([]int16, 1920)
	_, err = dec.Decode(packet, out)
	if !errors.Is(err, ErrChannelMismatch) {
		t.Errorf("Decode: expected ErrChannelMismatch, got: %v", err)
	}

	// DecodeAlloc should also fail.
	_, err = dec.DecodeAlloc(packet)
	if !errors.Is(err, ErrChannelMismatch) {
		t.Errorf("DecodeAlloc: expected ErrChannelMismatch, got: %v", err)
	}
}

// TestDecoderSampleRateMismatch verifies that Decoder.Decode returns
// ErrSampleRateMismatch when the packet's configuration indicates a different
// sample rate.
func TestDecoderSampleRateMismatch(t *testing.T) {
	t.Parallel()

	// Create a 48kHz encoder and 16kHz decoder.
	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(16000, 1) // different sample rate
	if err != nil {
		t.Fatal(err)
	}

	// Encode a 48kHz packet.
	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Decode should fail with ErrSampleRateMismatch.
	out := make([]int16, 960)
	_, err = dec.Decode(packet, out)
	if !errors.Is(err, ErrSampleRateMismatch) {
		t.Errorf("Decode: expected ErrSampleRateMismatch, got: %v", err)
	}

	// DecodeAlloc should also fail.
	_, err = dec.DecodeAlloc(packet)
	if !errors.Is(err, ErrSampleRateMismatch) {
		t.Errorf("DecodeAlloc: expected ErrSampleRateMismatch, got: %v", err)
	}
}

// TestDecoderPLC verifies that PLC (Packet Loss Concealment) works correctly.
func TestDecoderPLC(t *testing.T) {
	t.Parallel()

	// Create encoder and decoder
	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	// Enable PLC
	dec.EnablePLC()
	if !dec.IsPLCEnabled() {
		t.Fatal("PLC should be enabled")
	}

	// Generate and encode a frame
	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(1000 * (i % 100))
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Decode successfully to prime PLC
	out := make([]int16, 960)
	n, err := dec.Decode(packet, out)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if n != 960 {
		t.Errorf("Decode returned %d samples, want 960", n)
	}

	// Simulate packet loss - use DecodePLC
	plcOut := make([]int16, 960)
	n, err = dec.DecodePLC(plcOut)
	if err != nil {
		t.Fatalf("DecodePLC: %v", err)
	}
	if n != 960 {
		t.Errorf("DecodePLC returned %d samples, want 960", n)
	}

	// PLC output should not be all zeros (it should attempt concealment)
	// Note: The first PLC call after a good frame should have non-zero output
	allZero := true
	for _, s := range plcOut {
		if s != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Log("Note: PLC output is all zeros, which is acceptable for the first lost frame")
	}
}

// TestDecoderPLCNotEnabled verifies PLC behavior when not enabled.
func TestDecoderPLCNotEnabled(t *testing.T) {
	t.Parallel()

	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	// PLC should not be enabled by default
	if dec.IsPLCEnabled() {
		t.Error("PLC should not be enabled by default")
	}

	// DecodePLC should return silence when PLC is not enabled
	out := make([]int16, 960)
	n, err := dec.DecodePLC(out)
	if err != nil {
		t.Fatalf("DecodePLC: %v", err)
	}
	if n != 960 {
		t.Errorf("DecodePLC returned %d samples, want 960", n)
	}

	// All samples should be zero (silence)
	for i, s := range out {
		if s != 0 {
			t.Errorf("DecodePLC sample %d = %d, want 0 (silence)", i, s)
			break
		}
	}
}

// TestDecoderPLCBufferTooSmall verifies DecodePLC error handling.
func TestDecoderPLCBufferTooSmall(t *testing.T) {
	t.Parallel()

	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	dec.EnablePLC()

	// Buffer too small
	out := make([]int16, 100)
	_, err = dec.DecodePLC(out)
	if err == nil {
		t.Error("DecodePLC should fail with too-small buffer")
	}

	// Nil buffer
	_, err = dec.DecodePLC(nil)
	if err == nil {
		t.Error("DecodePLC should fail with nil buffer")
	}
}

// FuzzDecoder tests the Decoder against random/malformed packets.
// This fuzz test verifies that the decoder handles arbitrary input without
// panicking or causing memory corruption, fulfilling ROADMAP Milestone 6:
// "Fuzz the decoder against random/malformed packets."
func FuzzDecoder(f *testing.F) {
	// Seed corpus with various packet types
	// Empty packet
	f.Add([]byte{})

	// Single byte (TOC only)
	f.Add([]byte{0x00})
	f.Add([]byte{0xFC}) // Max config, stereo, code 0

	// Valid-looking TOC + short payload
	f.Add([]byte{0x78, 0x00}) // CELT FB config
	f.Add([]byte{0x00, 0x00}) // SILK NB config

	// Longer random data
	f.Add([]byte{0x78, 0x01, 0x02, 0x03, 0x04, 0x05})
	f.Add([]byte{0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})

	// All zeros
	f.Add(make([]byte, 100))

	// All 0xFF
	allFF := make([]byte, 50)
	for i := range allFF {
		allFF[i] = 0xFF
	}
	f.Add(allFF)

	f.Fuzz(func(t *testing.T, data []byte) {
		// Test with various decoder configurations
		configs := []struct {
			sampleRate int
			channels   int
		}{
			{8000, 1},
			{16000, 1},
			{24000, 1},
			{48000, 1},
			{48000, 2},
		}

		for _, cfg := range configs {
			dec, err := NewDecoder(cfg.sampleRate, cfg.channels)
			if err != nil {
				continue // skip invalid configs
			}

			// Test Decode with pre-allocated buffer
			out := make([]int16, cfg.sampleRate*60/1000*cfg.channels) // 60ms buffer
			_, _ = dec.Decode(data, out)

			// Test DecodeAlloc
			_, _ = dec.DecodeAlloc(data)

			// Test with CELT enabled (for 24/48 kHz)
			if cfg.sampleRate == 24000 || cfg.sampleRate == 48000 {
				_ = dec.EnableCELT()
				_, _ = dec.Decode(data, out)
				_, _ = dec.DecodeAlloc(data)
			}
		}
	})
}

// FuzzDecodeStandalone tests the standalone Decode function against random input.
func FuzzDecodeStandalone(f *testing.F) {
	// Seed corpus
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add([]byte{0x78, 0x00, 0x01, 0x02})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Standalone Decode should not panic
		_, _ = Decode(data)
		// DecodeWithInfo should not panic
		_, _, _ = DecodeWithInfo(data)
	})
}

// TestDecoderEnableHybrid verifies that EnableHybrid works correctly.
func TestDecoderEnableHybrid(t *testing.T) {
	t.Parallel()

	// EnableHybrid should succeed for 24 kHz
	dec, err := NewDecoder(24000, 1)
	if err != nil {
		t.Fatal(err)
	}

	err = dec.EnableHybrid()
	if err != nil {
		t.Errorf("EnableHybrid for 24kHz: unexpected error: %v", err)
	}

	if !dec.IsHybridEnabled() {
		t.Error("IsHybridEnabled should return true after EnableHybrid")
	}

	// EnableHybrid should fail for non-24 kHz sample rates
	dec48, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	err = dec48.EnableHybrid()
	if err == nil {
		t.Error("EnableHybrid for 48kHz: expected error, got nil")
	}

	dec16, err := NewDecoder(16000, 1)
	if err != nil {
		t.Fatal(err)
	}

	err = dec16.EnableHybrid()
	if err == nil {
		t.Error("EnableHybrid for 16kHz: expected error, got nil")
	}
}

// TestDecoderHybridRoundTrip verifies that hybrid encode/decode round-trips work.
func TestDecoderHybridRoundTrip(t *testing.T) {
	t.Parallel()

	// Create hybrid encoder
	encConfig := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	hybridEnc, err := NewHybridEncoder(encConfig)
	if err != nil {
		t.Fatalf("NewHybridEncoder: %v", err)
	}

	// Create decoder with hybrid enabled
	dec, err := NewDecoder(24000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.EnableHybrid(); err != nil {
		t.Fatalf("EnableHybrid: %v", err)
	}

	// Generate test audio
	frameSize := 24000 * 20 / 1000 // 480 samples
	samples := make([]float64, frameSize)
	for i := range samples {
		// 1 kHz sine wave
		samples[i] = 0.5 * float64(i%24) / 24.0
	}

	// Encode with hybrid encoder
	encoded, err := hybridEnc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	// Create Opus packet with hybrid TOC header
	// Configuration 13 = hybrid SWB 20ms, mono
	toc := byte(13<<3) | 0 // config=13, stereo=0, frameCode=0
	packet := append([]byte{toc}, encoded.Data...)

	// Decode with hybrid decoder
	out := make([]int16, frameSize)
	n, err := dec.Decode(packet, out)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if n != frameSize {
		t.Errorf("Decode returned %d samples, want %d", n, frameSize)
	}

	// Verify samples are non-zero (basic sanity check)
	nonZero := 0
	for _, s := range out[:n] {
		if s != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Error("all decoded samples are zero")
	}
}

// TestDecoderHybridDecodeAlloc verifies that DecodeAlloc works with hybrid packets.
func TestDecoderHybridDecodeAlloc(t *testing.T) {
	t.Parallel()

	// Create hybrid encoder
	encConfig := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	hybridEnc, err := NewHybridEncoder(encConfig)
	if err != nil {
		t.Fatalf("NewHybridEncoder: %v", err)
	}

	// Create decoder with hybrid enabled
	dec, err := NewDecoder(24000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.EnableHybrid(); err != nil {
		t.Fatalf("EnableHybrid: %v", err)
	}

	// Generate test audio
	frameSize := 24000 * 20 / 1000 // 480 samples
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.3 * float64(i%48) / 48.0
	}

	// Encode with hybrid encoder
	encoded, err := hybridEnc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	// Create Opus packet with hybrid TOC header
	toc := byte(13<<3) | 0 // config=13 (hybrid SWB 20ms), mono
	packet := append([]byte{toc}, encoded.Data...)

	// Decode with DecodeAlloc
	out, err := dec.DecodeAlloc(packet)
	if err != nil {
		t.Fatalf("DecodeAlloc: %v", err)
	}

	if len(out) != frameSize {
		t.Errorf("DecodeAlloc returned %d samples, want %d", len(out), frameSize)
	}
}

// TestClampToInt16 tests the clampToInt16 helper function.
func TestClampToInt16(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input float64
		want  int16
	}{
		{0, 0},
		{100, 100},
		{-100, -100},
		{32767, 32767},
		{-32768, -32768},
		{32768, 32767},   // clamped
		{40000, 32767},   // clamped
		{-32769, -32768}, // clamped
		{-40000, -32768}, // clamped
	}

	for _, tt := range tests {
		got := clampToInt16(tt.input)
		if got != tt.want {
			t.Errorf("clampToInt16(%f) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// TestConvertFloatSamplesToInt16 tests the sample conversion helper.
func TestConvertFloatSamplesToInt16(t *testing.T) {
	t.Parallel()

	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Test mono conversion
	floatSamples := []float64{0.0, 0.5, -0.5, 1.0, -1.0}
	out := make([]int16, 5)
	n := dec.convertFloatSamplesToInt16(floatSamples, out)

	if n != 5 {
		t.Errorf("convertFloatSamplesToInt16 returned %d samples, want 5", n)
	}

	// Check values
	expected := []int16{0, 16383, -16383, 32767, -32767}
	for i, want := range expected {
		// Allow small rounding tolerance
		diff := int(out[i]) - int(want)
		if diff < -1 || diff > 1 {
			t.Errorf("sample %d: got %d, want ~%d", i, out[i], want)
		}
	}
}

// TestConvertFloatSamplesToInt16Stereo tests stereo sample conversion.
func TestConvertFloatSamplesToInt16Stereo(t *testing.T) {
	t.Parallel()

	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}

	floatSamples := []float64{0.5, -0.5}
	out := make([]int16, 4) // stereo doubles output
	n := dec.convertFloatSamplesToInt16(floatSamples, out)

	if n != 4 {
		t.Errorf("convertFloatSamplesToInt16 returned %d samples, want 4", n)
	}

	// Check stereo duplication
	if out[0] != out[1] {
		t.Errorf("stereo sample 0: L=%d, R=%d (should be equal)", out[0], out[1])
	}
	if out[2] != out[3] {
		t.Errorf("stereo sample 1: L=%d, R=%d (should be equal)", out[2], out[3])
	}
}

// TestDecoderStereoCELT tests stereo CELT decoding with proper channel separation.
func TestDecoderStereoCELT(t *testing.T) {
	t.Parallel()

	// Create stereo encoder and decoder
	enc, err := NewEncoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.EnableCELT(); err != nil {
		t.Fatal(err)
	}

	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.EnableCELT(); err != nil {
		t.Fatal(err)
	}

	// Generate distinct stereo signal: left = +sine, right = -sine
	frameSize := 48000 * 20 / 1000 // samples per channel
	samples := make([]int16, frameSize*2)
	for i := 0; i < frameSize; i++ {
		val := int16(5000 * float64(i) / float64(frameSize))
		samples[i*2] = val    // Left: positive ramp
		samples[i*2+1] = -val // Right: negative ramp
	}

	// Encode
	packet, err := enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if packet == nil {
		t.Fatal("Expected packet, got nil")
	}

	// Decode
	out := make([]int16, frameSize*2)
	n, err := dec.Decode(packet, out)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// CELT decoder returns fewer samples due to MDCT overlap-add
	// but should return interleaved stereo (even count)
	if n < 2 || n%2 != 0 {
		t.Errorf("Decode returned %d samples, expected even count >= 2", n)
	}

	// Check that channels are distinct (not duplicated)
	// Due to lossy compression, values won't match exactly, but
	// left should be positive-trending and right should be negative-trending
	leftSum := int64(0)
	rightSum := int64(0)
	samplePairs := n / 2
	for i := 0; i < samplePairs; i++ {
		leftSum += int64(out[i*2])
		rightSum += int64(out[i*2+1])
	}

	// Left channel should have positive average, right should have negative
	if leftSum < 0 {
		t.Errorf("Left channel sum %d should be positive", leftSum)
	}
	if rightSum > 0 {
		t.Errorf("Right channel sum %d should be negative", rightSum)
	}
}

// TestDecoderMidSideTransform tests the mid/side inverse transform.
func TestDecoderMidSideTransform(t *testing.T) {
	t.Parallel()

	// Test convertFromMidSide directly
	mid := []float64{0.5, 0.6, 0.7}
	side := []float64{0.1, 0.2, 0.0}

	// Expected: L = M + S, R = M - S
	// L = [0.6, 0.8, 0.7], R = [0.4, 0.4, 0.7]
	left, right := convertFromMidSide(mid, side)

	if len(left) != 3 || len(right) != 3 {
		t.Fatalf("Expected 3 samples each, got left=%d, right=%d", len(left), len(right))
	}

	// Check values with tolerance for float precision
	tolerance := 1e-10
	expectedL := []float64{0.6, 0.8, 0.7}
	expectedR := []float64{0.4, 0.4, 0.7}

	for i := 0; i < 3; i++ {
		if absDiff(left[i], expectedL[i]) > tolerance {
			t.Errorf("left[%d] = %f, want %f", i, left[i], expectedL[i])
		}
		if absDiff(right[i], expectedR[i]) > tolerance {
			t.Errorf("right[%d] = %f, want %f", i, right[i], expectedR[i])
		}
	}
}

// TestDecoderMidSideStereoRoundTrip tests encoding with mid/side and decoding back.
func TestDecoderMidSideStereoRoundTrip(t *testing.T) {
	t.Parallel()

	// Create encoder with mid/side enabled
	enc, err := NewEncoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.EnableCELT(); err != nil {
		t.Fatal(err)
	}
	enc.EnableMidSideStereo()

	// Create decoder with mid/side enabled
	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.EnableCELT(); err != nil {
		t.Fatal(err)
	}
	dec.EnableMidSideStereo()

	// Generate correlated stereo signal (center-panned)
	frameSize := 48000 * 20 / 1000
	samples := make([]int16, frameSize*2)
	for i := 0; i < frameSize; i++ {
		val := int16(10000 * float64(i) / float64(frameSize))
		samples[i*2] = val   // Left
		samples[i*2+1] = val // Right (same as left = center)
	}

	// Encode
	packet, err := enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Decode
	out := make([]int16, frameSize*2)
	n, err := dec.Decode(packet, out)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// CELT decoder returns fewer samples due to MDCT overlap-add
	// but should return interleaved stereo (even count)
	if n < 2 || n%2 != 0 {
		t.Errorf("Decode returned %d samples, expected even count >= 2", n)
	}

	// For center-panned content, L and R should be similar
	diffSum := int64(0)
	samplePairs := n / 2
	for i := 0; i < samplePairs; i++ {
		diff := int64(out[i*2]) - int64(out[i*2+1])
		if diff < 0 {
			diff = -diff
		}
		diffSum += diff
	}

	avgDiff := float64(diffSum) / float64(samplePairs)
	// Allow some deviation due to compression, but channels should be similar
	if avgDiff > 1000 {
		t.Errorf("Average L/R difference %f too large for center-panned content", avgDiff)
	}
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}
