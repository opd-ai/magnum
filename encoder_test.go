package magnum

import (
	"errors"
	"io"
	"testing"
)

// TestNewEncoder verifies that NewEncoder accepts valid parameters and rejects
// invalid ones.
func TestNewEncoder(t *testing.T) {
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
			enc, err := NewEncoder(tt.sampleRate, tt.channels)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewEncoder(%d, %d): expected error, got nil", tt.sampleRate, tt.channels)
				}
				return
			}
			if err != nil {
				t.Errorf("NewEncoder(%d, %d): unexpected error: %v", tt.sampleRate, tt.channels, err)
			}
			if enc == nil {
				t.Errorf("NewEncoder(%d, %d): returned nil encoder", tt.sampleRate, tt.channels)
			}
		})
	}
}

// TestNewEncoderErrors checks that the correct exported sentinel errors are returned.
func TestNewEncoderErrors(t *testing.T) {
	t.Parallel()

	_, err := NewEncoder(44100, 1)
	if !errors.Is(err, ErrUnsupportedSampleRate) {
		t.Errorf("expected ErrUnsupportedSampleRate, got: %v", err)
	}

	_, err = NewEncoder(48000, 3)
	if !errors.Is(err, ErrUnsupportedChannelCount) {
		t.Errorf("expected ErrUnsupportedChannelCount, got: %v", err)
	}
}

// TestSetBitrate verifies that SetBitrate stores the value and clamps
// out-of-range inputs.
func TestSetBitrate(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	enc.SetBitrate(128000)
	if enc.bitrate != 128000 {
		t.Errorf("SetBitrate(128000): got %d, want 128000", enc.bitrate)
	}

	// Below minimum: should clamp to 6000.
	enc.SetBitrate(1)
	if enc.bitrate != 6000 {
		t.Errorf("SetBitrate(1): got %d, want 6000 (minimum)", enc.bitrate)
	}

	// Above maximum: should clamp to 510000.
	enc.SetBitrate(999999)
	if enc.bitrate != 510000 {
		t.Errorf("SetBitrate(999999): got %d, want 510000 (maximum)", enc.bitrate)
	}
}

// TestEncodeEmptyInput verifies that Encode returns nil for an empty slice
// when the buffer is also empty.
func TestEncodeEmptyInput(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	result, err := enc.Encode(nil)
	if err != nil {
		t.Fatalf("Encode(nil): unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("Encode(nil): expected nil, got %d bytes", len(result))
	}
}

// TestEncodePartialFrame verifies that a buffer smaller than one full frame
// returns nil without error.
func TestEncodePartialFrame(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	// 960 samples are needed for 20 ms at 48 kHz mono; supply fewer.
	pcm := make([]int16, 100)
	result, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("Encode: expected nil for partial frame, got %d bytes", len(result))
	}
}

// TestEncodeFrame verifies that a complete 20 ms mono frame produces a
// non-empty packet with a valid TOC header.
func TestEncodeFrame(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	// 960 samples = 20 ms at 48 kHz mono.
	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}

	packet, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode: expected non-empty packet")
	}

	toc := tocHeader(packet[0])
	if got := toc.configuration(); got != ConfigurationCELTFB20ms {
		t.Errorf("TOC configuration: got %d, want %d (ConfigurationCELTFB20ms)", got, ConfigurationCELTFB20ms)
	}
	if toc.isStereo() {
		t.Error("TOC stereo flag: expected mono (false), got true")
	}
	if got := toc.frameCode(); got != frameCodeOneFrame {
		t.Errorf("TOC frame code: got %d, want %d (frameCodeOneFrame)", got, frameCodeOneFrame)
	}
}

// TestEncodeFrameStereo verifies that a 48 kHz stereo encoder sets the TOC
// stereo flag and requires 1920 interleaved int16 samples per 20 ms frame
// (960 samples/channel × 2 channels).
func TestEncodeFrameStereo(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}

	// 1920 samples = 960 frames * 2 channels (L/R interleaved) for 20 ms at 48 kHz.
	pcm := make([]int16, 1920)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}

	packet, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode: expected non-empty packet for stereo frame")
	}
	if !tocHeader(packet[0]).isStereo() {
		t.Error("TOC stereo flag: expected true for stereo encoder, got false")
	}
}

// TestEncodeDecodeRoundTrip verifies that Decode recovers the original PCM
// samples from a packet produced by Encode.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Build a test signal: a simple sawtooth wave.
	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}

	packet, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := Decode(packet)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if len(decoded) != len(pcm) {
		t.Fatalf("Decode: got %d samples, want %d", len(decoded), len(pcm))
	}
	for i, want := range pcm {
		if decoded[i] != want {
			t.Errorf("sample[%d]: got %d, want %d", i, decoded[i], want)
		}
	}
}

// TestEncodeMultipleFrames verifies that feeding more than one frame's worth
// of samples in a single Encode call does not silently drop the extra frame.
// The second frame must be retrievable by a subsequent Encode call.
func TestEncodeMultipleFrames(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Build two distinct 960-sample frames.
	frame1 := make([]int16, 960)
	frame2 := make([]int16, 960)
	for i := range frame1 {
		frame1[i] = int16(i % 1000)
		frame2[i] = int16((i + 500) % 1000)
	}

	// Feed both frames in a single call (1920 samples).
	combined := append(frame1, frame2...)
	packet1, err := enc.Encode(combined)
	if err != nil {
		t.Fatalf("Encode (combined): %v", err)
	}
	if packet1 == nil {
		t.Fatal("Encode: expected first packet, got nil")
	}

	// Drain the second frame without supplying new samples.
	packet2, err := enc.Encode(nil)
	if err != nil {
		t.Fatalf("Encode (drain): %v", err)
	}
	if packet2 == nil {
		t.Fatal("Encode: expected second packet after drain, got nil (data loss)")
	}

	// Verify that the second decoded packet matches frame2 exactly.
	decoded2, err := Decode(packet2)
	if err != nil {
		t.Fatalf("Decode packet2: %v", err)
	}
	if len(decoded2) != len(frame2) {
		t.Fatalf("packet2 decoded length: got %d, want %d", len(decoded2), len(frame2))
	}
	for i, want := range frame2 {
		if decoded2[i] != want {
			t.Errorf("packet2 sample[%d]: got %d, want %d", i, decoded2[i], want)
		}
	}
}

// TestDecodeEmptyPacket verifies that Decode rejects a completely empty packet.
func TestDecodeEmptyPacket(t *testing.T) {
	t.Parallel()

	_, err := Decode([]byte{})
	if err == nil {
		t.Error("Decode: expected error for empty packet, got nil")
	}
	if !errors.Is(err, ErrTooShortForTableOfContentsHeader) {
		t.Errorf("Decode: expected ErrTooShortForTableOfContentsHeader, got: %v", err)
	}
}

// TestDecodeShortPacket verifies that Decode rejects a packet that has only a
// TOC byte but no payload, returning io.ErrUnexpectedEOF.
func TestDecodeShortPacket(t *testing.T) {
	t.Parallel()

	_, err := Decode([]byte{0})
	if err == nil {
		t.Error("Decode: expected error for 1-byte packet, got nil")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Decode: expected io.ErrUnexpectedEOF for TOC-only packet, got: %v", err)
	}
}

// TestFrameBufferBuffering verifies that the frameBuffer accumulates samples
// and releases complete frames correctly via next.
func TestFrameBufferBuffering(t *testing.T) {
	t.Parallel()

	fb := newFrameBuffer(48000, 1)
	if fb.frameSize != 960 {
		t.Fatalf("frameSize: got %d, want 960 (20ms @ 48kHz mono)", fb.frameSize)
	}

	// Partial write: no complete frame yet.
	fb.write(make([]int16, 500))
	if got := fb.next(); got != nil {
		t.Errorf("next() after partial write: expected nil, got %d samples", len(got))
	}
	if fb.buffered() != 500 {
		t.Errorf("buffered: got %d, want 500", fb.buffered())
	}

	// Supply the remaining samples to complete one frame.
	fb.write(make([]int16, 460))
	if got := fb.next(); got == nil {
		t.Error("next() after completing frame: expected frame, got nil")
	}
	// No more frames or partial samples remain.
	if got := fb.next(); got != nil {
		t.Errorf("next() after draining: expected nil, got %d samples", len(got))
	}
	if fb.buffered() != 0 {
		t.Errorf("buffered after full consume: got %d, want 0", fb.buffered())
	}
}

// TestFrameBufferMultipleFrames verifies that feeding more than one frame at
// once queues all complete frames without data loss.
func TestFrameBufferMultipleFrames(t *testing.T) {
	t.Parallel()

	fb := newFrameBuffer(48000, 1)

	// Feed exactly two frames (1920 samples at 48 kHz mono).
	fb.write(make([]int16, 1920))

	frame1 := fb.next()
	if frame1 == nil {
		t.Fatal("next(): expected first frame, got nil")
	}
	frame2 := fb.next()
	if frame2 == nil {
		t.Fatal("next(): expected second frame, got nil (data loss)")
	}
	if got := fb.next(); got != nil {
		t.Errorf("next() after two frames drained: expected nil, got frame")
	}
}

// TestFrameBufferFlush verifies that flush returns a zero-padded frame for
// partial data, and nil for an empty buffer.
func TestFrameBufferFlush(t *testing.T) {
	t.Parallel()

	fb := newFrameBuffer(48000, 1)

	// Empty buffer: flush returns nil.
	if got := fb.flush(); got != nil {
		t.Errorf("flush on empty buffer: expected nil, got %d samples", len(got))
	}

	// Partial fill: flush returns a full-length zero-padded frame.
	fb.write(make([]int16, 100))
	frame := fb.flush()
	if frame == nil {
		t.Fatal("flush: expected frame, got nil")
	}
	if len(frame) != fb.frameSize {
		t.Errorf("flush frame length: got %d, want %d", len(frame), fb.frameSize)
	}
	// Samples 0–99 are zero (input was zero-valued); 100–959 must be zero-padded.
	for i, s := range frame[100:] {
		if s != 0 {
			t.Errorf("flush padding at index %d: got %d, want 0", 100+i, s)
		}
	}

	// After flush the partial buffer should be empty.
	if fb.buffered() != 0 {
		t.Errorf("buffered after flush: got %d, want 0", fb.buffered())
	}
}
