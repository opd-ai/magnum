package magnum

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
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

// TestEncoderFlush verifies that Flush returns a zero-padded frame for partial
// buffered data, and nil when the buffer is empty.
func TestEncoderFlush(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Empty buffer: Flush returns nil.
	packet, err := enc.Flush()
	if err != nil {
		t.Fatalf("Flush on empty buffer: unexpected error: %v", err)
	}
	if packet != nil {
		t.Errorf("Flush on empty buffer: expected nil, got %d bytes", len(packet))
	}

	// Partial fill: 100 samples (less than 960 needed for 20 ms @ 48 kHz mono).
	pcm := make([]int16, 100)
	for i := range pcm {
		pcm[i] = int16(i + 1) // non-zero values to verify they're preserved
	}
	_, err = enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode partial: unexpected error: %v", err)
	}

	// Flush should return a packet with zero-padded frame.
	packet, err = enc.Flush()
	if err != nil {
		t.Fatalf("Flush: unexpected error: %v", err)
	}
	if packet == nil {
		t.Fatal("Flush: expected packet for partial frame, got nil")
	}

	// Decode and verify: first 100 samples should match input, rest should be zeros.
	decoded, err := Decode(packet)
	if err != nil {
		t.Fatalf("Decode flushed packet: %v", err)
	}
	if len(decoded) != 960 {
		t.Fatalf("Decoded length: got %d, want 960", len(decoded))
	}
	for i := 0; i < 100; i++ {
		if decoded[i] != pcm[i] {
			t.Errorf("sample[%d]: got %d, want %d", i, decoded[i], pcm[i])
		}
	}
	for i := 100; i < 960; i++ {
		if decoded[i] != 0 {
			t.Errorf("zero-padded sample[%d]: got %d, want 0", i, decoded[i])
		}
	}

	// After flush, buffer should be empty, so another Flush returns nil.
	packet, err = enc.Flush()
	if err != nil {
		t.Fatalf("Flush after drain: unexpected error: %v", err)
	}
	if packet != nil {
		t.Errorf("Flush after drain: expected nil, got %d bytes", len(packet))
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

// TestDecodeWithInfoReturnsStereoFlag verifies that DecodeWithInfo correctly
// returns the stereo flag from the TOC header for both mono and stereo packets.
func TestDecodeWithInfoReturnsStereoFlag(t *testing.T) {
	t.Parallel()

	// Test mono packet.
	encMono, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	pcmMono := make([]int16, 960)
	for i := range pcmMono {
		pcmMono[i] = int16(i % 1000)
	}
	packetMono, err := encMono.Encode(pcmMono)
	if err != nil {
		t.Fatalf("Encode mono: %v", err)
	}

	samplesMono, stereoMono, err := DecodeWithInfo(packetMono)
	if err != nil {
		t.Fatalf("DecodeWithInfo mono: %v", err)
	}
	if stereoMono {
		t.Error("DecodeWithInfo mono: expected stereo=false, got true")
	}
	if len(samplesMono) != len(pcmMono) {
		t.Errorf("DecodeWithInfo mono: got %d samples, want %d", len(samplesMono), len(pcmMono))
	}

	// Test stereo packet.
	encStereo, err := NewEncoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	pcmStereo := make([]int16, 1920) // 960 samples * 2 channels
	for i := range pcmStereo {
		pcmStereo[i] = int16(i % 1000)
	}
	packetStereo, err := encStereo.Encode(pcmStereo)
	if err != nil {
		t.Fatalf("Encode stereo: %v", err)
	}

	samplesStereo, stereoStereo, err := DecodeWithInfo(packetStereo)
	if err != nil {
		t.Fatalf("DecodeWithInfo stereo: %v", err)
	}
	if !stereoStereo {
		t.Error("DecodeWithInfo stereo: expected stereo=true, got false")
	}
	if len(samplesStereo) != len(pcmStereo) {
		t.Errorf("DecodeWithInfo stereo: got %d samples, want %d", len(samplesStereo), len(pcmStereo))
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

// TestDecodeInvalidFrameCode verifies that Decode rejects packets with
// multi-frame encoding (frame codes 1, 2, 3).
func TestDecodeInvalidFrameCode(t *testing.T) {
	t.Parallel()

	// Frame codes 1, 2, 3 are stored in bits 1-0 of the TOC byte.
	// Use a minimal valid-looking packet (TOC byte + some flate data).
	for _, fc := range []byte{1, 2, 3} {
		tocByte := byte(ConfigurationCELTFB20ms<<3) | fc // config 31, frame code fc
		packet := []byte{tocByte, 0x00}                  // minimal packet with invalid frame code
		_, err := Decode(packet)
		if !errors.Is(err, ErrUnsupportedFrameCode) {
			t.Errorf("Decode with frame code %d: expected ErrUnsupportedFrameCode, got: %v", fc, err)
		}
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

// TestFrameBufferQueueLimit verifies that the frame buffer respects the
// maxQueueDepth limit when configured.
func TestFrameBufferQueueLimit(t *testing.T) {
	t.Parallel()

	// Create a frame buffer with a limited queue depth.
	frameSize := 48000 * 20 / 1000 * 1 // 960 samples per frame at 48kHz mono
	fb := &frameBuffer{
		samples:       make([]int16, 0, frameSize),
		ready:         make([][]int16, 0, 4),
		frameSize:     frameSize,
		maxQueueDepth: 3, // Only allow 3 frames in queue
	}

	// Write first 3 frames - should succeed.
	pcm := make([]int16, frameSize*3)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	if err := fb.write(pcm); err != nil {
		t.Fatalf("first write should succeed: %v", err)
	}
	if len(fb.ready) != 3 {
		t.Errorf("expected 3 ready frames, got %d", len(fb.ready))
	}

	// Try to write one more frame - should fail.
	oneFrame := make([]int16, frameSize)
	if err := fb.write(oneFrame); err != ErrFrameQueueFull {
		t.Errorf("expected ErrFrameQueueFull, got: %v", err)
	}

	// Consume one frame.
	if frame := fb.next(); frame == nil {
		t.Error("expected a frame from next()")
	}

	// Now writing should succeed again.
	if err := fb.write(oneFrame); err != nil {
		t.Fatalf("write after consuming should succeed: %v", err)
	}
	if len(fb.ready) != 3 {
		t.Errorf("expected 3 ready frames after write, got %d", len(fb.ready))
	}
}

// TestFrameBufferUnboundedDefault verifies that the default frame buffer
// is unbounded (backward compatibility).
func TestFrameBufferUnboundedDefault(t *testing.T) {
	t.Parallel()

	fb := newFrameBuffer(48000, 1)

	// Write many frames without consuming - should not error.
	// 100 frames = ~2 seconds of audio at 20ms per frame.
	frameSize := 48000 * 20 / 1000 // 960 samples
	pcm := make([]int16, frameSize*100)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	if err := fb.write(pcm); err != nil {
		t.Fatalf("unbounded write should not fail: %v", err)
	}
	if len(fb.ready) != 100 {
		t.Errorf("expected 100 ready frames, got %d", len(fb.ready))
	}
}

// BenchmarkEncode48kMono measures encoding performance for 48 kHz mono audio.
func BenchmarkEncode48kMono(b *testing.B) {
	enc, err := NewEncoder(48000, 1)
	if err != nil {
		b.Fatal(err)
	}

	// Pre-allocate a 20 ms frame (960 samples at 48 kHz mono).
	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := enc.Encode(pcm)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEncode48kStereo measures encoding performance for 48 kHz stereo audio.
func BenchmarkEncode48kStereo(b *testing.B) {
	enc, err := NewEncoder(48000, 2)
	if err != nil {
		b.Fatal(err)
	}

	// Pre-allocate a 20 ms frame (1920 samples at 48 kHz stereo).
	pcm := make([]int16, 1920)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := enc.Encode(pcm)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecode measures decoding performance for 48 kHz mono audio.
func BenchmarkDecode(b *testing.B) {
	enc, err := NewEncoder(48000, 1)
	if err != nil {
		b.Fatal(err)
	}

	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := Decode(packet)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecoderDecode measures Decoder.Decode performance with buffer reuse.
// This benchmark demonstrates the performance benefit of using the Decoder type
// with a pre-allocated output buffer versus the standalone Decode function.
func BenchmarkDecoderDecode(b *testing.B) {
	enc, err := NewEncoder(48000, 1)
	if err != nil {
		b.Fatal(err)
	}

	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		b.Fatal(err)
	}

	dec, err := NewDecoder(48000, 1)
	if err != nil {
		b.Fatal(err)
	}
	out := make([]int16, 960)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(packet, out)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// TestCELTEncoderDecoderIntegration tests the CELT encoder/decoder integration
// introduced in ROADMAP Milestone 2f. Unlike flate-based encoding, CELT is lossy
// so we cannot expect exact sample reconstruction. Instead, we verify that the
// encoded packet can be decoded without error and produces output of the expected
// size with reasonable signal quality (non-zero energy).
func TestCELTEncoderDecoderIntegration(t *testing.T) {
	t.Parallel()

	// Test 48 kHz mono
	t.Run("48kHz_mono", func(t *testing.T) {
		enc, err := NewEncoder(48000, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.EnableCELT(); err != nil {
			t.Fatalf("EnableCELT: %v", err)
		}
		if !enc.IsCELTEnabled() {
			t.Error("IsCELTEnabled should return true after EnableCELT")
		}

		dec, err := NewDecoder(48000, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := dec.EnableCELT(); err != nil {
			t.Fatalf("Decoder EnableCELT: %v", err)
		}

		// Generate a test signal (sine wave)
		pcm := make([]int16, 960)
		for i := range pcm {
			pcm[i] = int16(10000.0 * sinTable(float64(i)*2*3.14159/100))
		}

		packet, err := enc.Encode(pcm)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if len(packet) < 2 {
			t.Fatalf("Encoded packet too short: %d bytes", len(packet))
		}

		decoded, err := dec.DecodeAlloc(packet)
		if err != nil {
			t.Fatalf("DecodeAlloc: %v", err)
		}

		// CELT produces N/2 samples due to MDCT overlap-add
		// The decoded length may differ from input
		if len(decoded) == 0 {
			t.Error("Decoded packet has zero samples")
		}

		// Check that we have non-zero energy (signal was decoded)
		var energy float64
		for _, s := range decoded {
			energy += float64(s) * float64(s)
		}
		if energy == 0 {
			t.Error("Decoded signal has zero energy")
		}
	})

	// Test 24 kHz mono
	t.Run("24kHz_mono", func(t *testing.T) {
		enc, err := NewEncoder(24000, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.EnableCELT(); err != nil {
			t.Fatalf("EnableCELT: %v", err)
		}

		dec, err := NewDecoder(24000, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := dec.EnableCELT(); err != nil {
			t.Fatalf("Decoder EnableCELT: %v", err)
		}

		// 24 kHz mono: 480 samples per 20ms frame
		pcm := make([]int16, 480)
		for i := range pcm {
			pcm[i] = int16(10000.0 * sinTable(float64(i)*2*3.14159/50))
		}

		packet, err := enc.Encode(pcm)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}

		decoded, err := dec.DecodeAlloc(packet)
		if err != nil {
			t.Fatalf("DecodeAlloc: %v", err)
		}

		if len(decoded) == 0 {
			t.Error("Decoded packet has zero samples")
		}
	})

	// Test that EnableCELT fails for 8 kHz (SILK-only)
	t.Run("8kHz_not_supported", func(t *testing.T) {
		enc, err := NewEncoder(8000, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.EnableCELT(); err == nil {
			t.Error("EnableCELT should fail for 8 kHz")
		}
	})
}

// sinTable returns sin(x) - a simple sine function for test signal generation
func sinTable(x float64) float64 {
	// Use Taylor series approximation for simplicity
	x = x - float64(int(x/(2*3.14159)))*2*3.14159
	if x > 3.14159 {
		x -= 2 * 3.14159
	}
	return x - x*x*x/6 + x*x*x*x*x/120
}

// TestCELTLibopusValidation validates that CELT-encoded packets can be decoded
// by libopus (via opusdec). This test requires the opus-tools package to be
// installed and is skipped if opusdec is not available.
//
// This test fulfills ROADMAP Milestone 2f: "Validate encoded packets with
// opusdec / opus_demo from libopus."
func TestCELTLibopusValidation(t *testing.T) {
	// Skip if opusdec is not available
	if _, err := exec.LookPath("opusdec"); err != nil {
		t.Skip("opusdec not available; install opus-tools to run this test")
	}

	// Create temporary directory for test files
	tmpDir := t.TempDir()
	oggFile := tmpDir + "/test.opus"
	wavFile := tmpDir + "/test.wav"

	// Create encoder with CELT enabled
	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.EnableCELT(); err != nil {
		t.Fatalf("EnableCELT: %v", err)
	}
	enc.SetBitrate(64000)

	// Generate 1 second of 440 Hz sine wave at 48kHz
	const (
		sampleRate = 48000
		duration   = 1.0
		frequency  = 440.0
		frameSize  = 960 // 20ms at 48kHz
	)
	numSamples := int(float64(sampleRate) * duration)
	pcm := make([]int16, numSamples)
	for i := range pcm {
		theta := 2.0 * 3.14159265359 * frequency * float64(i) / float64(sampleRate)
		pcm[i] = int16(16000.0 * sinTable(theta))
	}

	// Encode all frames
	var packets [][]byte
	for i := 0; i < len(pcm); i += frameSize {
		end := i + frameSize
		if end > len(pcm) {
			end = len(pcm)
		}
		packet, err := enc.Encode(pcm[i:end])
		if err != nil {
			t.Fatalf("Encode frame %d: %v", i/frameSize, err)
		}
		if packet != nil {
			packets = append(packets, packet)
		}
	}
	if packet, err := enc.Flush(); err == nil && packet != nil {
		packets = append(packets, packet)
	}

	if len(packets) == 0 {
		t.Fatal("No packets encoded")
	}

	// Write to Ogg Opus file
	if err := writeOggOpusFile(oggFile, packets, sampleRate); err != nil {
		t.Fatalf("writeOggOpusFile: %v", err)
	}

	// Decode with opusdec
	cmd := exec.Command("opusdec", oggFile, wavFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Log packet details for debugging
		if len(packets) > 0 {
			toc := packets[0][0]
			t.Logf("First packet TOC: 0x%02x (config=%d, stereo=%d, code=%d)",
				toc, (toc>>3)&0x1F, (toc>>2)&0x01, toc&0x03)
			t.Logf("First packet size: %d bytes", len(packets[0]))
		}
		t.Fatalf("opusdec failed: %v\nOutput: %s", err, output)
	}

	// Verify WAV file was created
	info, err := os.Stat(wavFile)
	if err != nil {
		t.Fatalf("WAV file not created: %v", err)
	}
	if info.Size() < 1000 {
		t.Errorf("WAV file too small: %d bytes", info.Size())
	}

	t.Logf("Successfully validated %d CELT packets with libopus (opusdec)", len(packets))
}

// writeOggOpusFile writes Opus packets to an Ogg container file.
func writeOggOpusFile(filename string, packets [][]byte, sampleRate int) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// ID Header page
	idHeader := createOpusIDHeader(1, sampleRate)
	if err := writeOggPage(f, idHeader, 0, 0, true, false); err != nil {
		return err
	}

	// Comment Header page
	commentHeader := createOpusCommentHeader()
	if err := writeOggPage(f, commentHeader, 0, 1, false, false); err != nil {
		return err
	}

	// Audio data pages
	granulePos := uint64(0)
	for i, pkt := range packets {
		granulePos += 960 // 20ms at 48kHz
		isLast := i == len(packets)-1
		if err := writeOggPage(f, pkt, granulePos, uint32(i+2), false, isLast); err != nil {
			return err
		}
	}

	return nil
}

func createOpusIDHeader(channels, sampleRate int) []byte {
	buf := new(bytes.Buffer)
	buf.WriteString("OpusHead")
	buf.WriteByte(1)                                           // version
	buf.WriteByte(byte(channels))                              // channel count
	binary.Write(buf, binary.LittleEndian, uint16(0))          // pre-skip
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate)) // input sample rate
	binary.Write(buf, binary.LittleEndian, int16(0))           // output gain
	buf.WriteByte(0)                                           // channel mapping family
	return buf.Bytes()
}

func createOpusCommentHeader() []byte {
	buf := new(bytes.Buffer)
	buf.WriteString("OpusTags")
	vendor := "magnum"
	binary.Write(buf, binary.LittleEndian, uint32(len(vendor)))
	buf.WriteString(vendor)
	binary.Write(buf, binary.LittleEndian, uint32(0)) // no user comments
	return buf.Bytes()
}

func writeOggPage(w io.Writer, data []byte, granulePos uint64, pageSeq uint32, bos, eos bool) error {
	buf := new(bytes.Buffer)

	// Capture pattern
	buf.WriteString("OggS")

	// Stream structure version
	buf.WriteByte(0)

	// Header type flag
	flags := byte(0)
	if bos {
		flags |= 0x02
	}
	if eos {
		flags |= 0x04
	}
	buf.WriteByte(flags)

	// Granule position
	binary.Write(buf, binary.LittleEndian, granulePos)

	// Bitstream serial number
	binary.Write(buf, binary.LittleEndian, uint32(1))

	// Page sequence number
	binary.Write(buf, binary.LittleEndian, pageSeq)

	// CRC checksum (placeholder)
	crcPos := buf.Len()
	binary.Write(buf, binary.LittleEndian, uint32(0))

	// Number of page segments
	numSegments := (len(data) + 254) / 255
	if numSegments == 0 {
		numSegments = 1
	}
	buf.WriteByte(byte(numSegments))

	// Segment table
	remaining := len(data)
	for i := 0; i < numSegments; i++ {
		if remaining >= 255 {
			buf.WriteByte(255)
			remaining -= 255
		} else {
			buf.WriteByte(byte(remaining))
			remaining = 0
		}
	}

	// Payload
	buf.Write(data)

	// Calculate CRC
	pageData := buf.Bytes()
	crc := oggCRC32(pageData)
	binary.LittleEndian.PutUint32(pageData[crcPos:], crc)

	_, err := w.Write(pageData)
	return err
}

// oggCRCTable is the CRC32 lookup table for Ogg pages.
var oggCRCTable = func() [256]uint32 {
	const poly = 0x04c11db7
	var table [256]uint32
	for i := 0; i < 256; i++ {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ poly
			} else {
				r <<= 1
			}
		}
		table[i] = r
	}
	return table
}()

func oggCRC32(data []byte) uint32 {
	crc := uint32(0)
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}

// TestSILKEncoderIntegration tests SILK encoding integration at 8 kHz and 16 kHz.
func TestSILKEncoderIntegration(t *testing.T) {
	t.Parallel()

	// Test 8 kHz mono
	t.Run("8kHz_mono", func(t *testing.T) {
		enc, err := NewEncoder(8000, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.EnableSILK(); err != nil {
			t.Fatalf("EnableSILK: %v", err)
		}
		if !enc.IsSILKEnabled() {
			t.Error("IsSILKEnabled should return true after EnableSILK")
		}

		// Generate a test signal (sine wave) - 160 samples for 20ms at 8kHz
		pcm := make([]int16, 160)
		for i := range pcm {
			pcm[i] = int16(10000.0 * sinTable(float64(i)*2*3.14159/40))
		}

		packet, err := enc.Encode(pcm)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if len(packet) < 2 {
			t.Fatalf("Encoded packet too short: %d bytes", len(packet))
		}
		t.Logf("8kHz SILK encoded %d samples to %d bytes", len(pcm), len(packet))
	})

	// Test 16 kHz mono
	t.Run("16kHz_mono", func(t *testing.T) {
		enc, err := NewEncoder(16000, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.EnableSILK(); err != nil {
			t.Fatalf("EnableSILK: %v", err)
		}
		if !enc.IsSILKEnabled() {
			t.Error("IsSILKEnabled should return true after EnableSILK")
		}

		// Generate a test signal (sine wave) - 320 samples for 20ms at 16kHz
		pcm := make([]int16, 320)
		for i := range pcm {
			pcm[i] = int16(10000.0 * sinTable(float64(i)*2*3.14159/80))
		}

		packet, err := enc.Encode(pcm)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if len(packet) < 2 {
			t.Fatalf("Encoded packet too short: %d bytes", len(packet))
		}
		t.Logf("16kHz SILK encoded %d samples to %d bytes", len(pcm), len(packet))
	})

	// Test error: SILK on 48 kHz should fail
	t.Run("48kHz_unsupported", func(t *testing.T) {
		enc, err := NewEncoder(48000, 1)
		if err != nil {
			t.Fatal(err)
		}
		err = enc.EnableSILK()
		if err == nil {
			t.Error("EnableSILK should fail for 48 kHz")
		}
	})

	// Test that IsSILKEnabled returns false by default
	t.Run("disabled_by_default", func(t *testing.T) {
		enc, err := NewEncoder(8000, 1)
		if err != nil {
			t.Fatal(err)
		}
		if enc.IsSILKEnabled() {
			t.Error("IsSILKEnabled should return false by default")
		}
	})
}

// TestSILKLibopusValidation validates that SILK-encoded packets can be decoded
// by libopus (via opusdec). This test requires the opus-tools package to be
// installed and is skipped if opusdec is not available.
//
// This test fulfills ROADMAP Milestone 3f: "Validate encoded packets with
// opusdec / opus_demo from libopus."
func TestSILKLibopusValidation(t *testing.T) {
	// Skip if opusdec is not available
	if _, err := exec.LookPath("opusdec"); err != nil {
		t.Skip("opusdec not available; install opus-tools to run this test")
	}

	testCases := []struct {
		name          string
		sampleRate    int
		frameSize     int
		granulePerPkt uint64 // granule increment per packet (in 48kHz samples)
	}{
		{"8kHz", 8000, 160, 960},   // 20ms = 160 samples at 8kHz = 960 at 48kHz
		{"16kHz", 16000, 320, 960}, // 20ms = 320 samples at 16kHz = 960 at 48kHz
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create temporary directory for test files
			tmpDir := t.TempDir()
			oggFile := tmpDir + "/test.opus"
			wavFile := tmpDir + "/test.wav"

			// Create encoder with SILK enabled
			enc, err := NewEncoder(tc.sampleRate, 1)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.EnableSILK(); err != nil {
				t.Fatalf("EnableSILK: %v", err)
			}
			enc.SetBitrate(12000)

			// Generate 0.5 second of 440 Hz sine wave
			const duration = 0.5
			const frequency = 440.0
			numSamples := int(float64(tc.sampleRate) * duration)
			pcm := make([]int16, numSamples)
			for i := range pcm {
				theta := 2.0 * 3.14159265359 * frequency * float64(i) / float64(tc.sampleRate)
				pcm[i] = int16(16000.0 * sinTable(theta))
			}

			// Encode all frames
			var packets [][]byte
			for i := 0; i < len(pcm); i += tc.frameSize {
				end := i + tc.frameSize
				if end > len(pcm) {
					end = len(pcm)
				}
				packet, err := enc.Encode(pcm[i:end])
				if err != nil {
					t.Fatalf("Encode frame %d: %v", i/tc.frameSize, err)
				}
				if packet != nil {
					packets = append(packets, packet)
				}
			}
			if packet, err := enc.Flush(); err == nil && packet != nil {
				packets = append(packets, packet)
			}

			if len(packets) == 0 {
				t.Fatal("No packets encoded")
			}

			// Write to Ogg Opus file (resampled sample rate must be 48kHz for Opus header)
			if err := writeSILKOggOpusFile(oggFile, packets, tc.sampleRate, tc.granulePerPkt); err != nil {
				t.Fatalf("writeOggOpusFile: %v", err)
			}

			// Decode with opusdec
			cmd := exec.Command("opusdec", oggFile, wavFile)
			output, err := cmd.CombinedOutput()
			if err != nil {
				// Log packet details for debugging
				if len(packets) > 0 {
					toc := packets[0][0]
					t.Logf("First packet TOC: 0x%02x (config=%d, stereo=%d, code=%d)",
						toc, (toc>>3)&0x1F, (toc>>2)&0x01, toc&0x03)
					t.Logf("First packet size: %d bytes", len(packets[0]))
				}
				t.Fatalf("opusdec failed: %v\nOutput: %s", err, output)
			}

			// Verify WAV file was created
			info, err := os.Stat(wavFile)
			if err != nil {
				t.Fatalf("WAV file not created: %v", err)
			}
			if info.Size() < 100 {
				t.Errorf("WAV file too small: %d bytes", info.Size())
			}

			t.Logf("Successfully validated %d SILK packets at %dHz with libopus (opusdec)", len(packets), tc.sampleRate)
		})
	}
}

// writeSILKOggOpusFile writes SILK Opus packets to an Ogg container file.
func writeSILKOggOpusFile(filename string, packets [][]byte, sampleRate int, granulePerPkt uint64) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// ID Header page - note: Opus header always uses 48kHz as reference
	idHeader := createOpusIDHeader(1, sampleRate)
	if err := writeOggPage(f, idHeader, 0, 0, true, false); err != nil {
		return err
	}

	// Comment Header page
	commentHeader := createOpusCommentHeader()
	if err := writeOggPage(f, commentHeader, 0, 1, false, false); err != nil {
		return err
	}

	// Audio data pages
	granulePos := uint64(0)
	for i, pkt := range packets {
		granulePos += granulePerPkt // 20ms worth of samples at 48kHz reference
		isLast := i == len(packets)-1
		if err := writeOggPage(f, pkt, granulePos, uint32(i+2), false, isLast); err != nil {
			return err
		}
	}

	return nil
}
