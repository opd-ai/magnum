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
