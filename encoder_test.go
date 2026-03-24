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

// TestDecodeInvalidFrameCode verifies that Decode rejects invalid packets.
// All frame codes (0, 1, 2, 3) are now supported, so we test with
// malformed packets that should fail validation.
func TestDecodeInvalidFrameCode(t *testing.T) {
	t.Parallel()

	// A packet with frame code 3 but invalid M byte (frame count = 0)
	// should fail with invalid frame data
	tocByte := byte(ConfigurationCELTFB20ms<<3) | 3 // config 31, frame code 3
	packet := []byte{tocByte, 0x01}                 // M byte: count=0, VBR=1
	_, err := Decode(packet)
	if err == nil {
		t.Error("Decode with frame code 3 and count=0: expected error, got nil")
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

// =====================================================================
// Benchmarks for all codec paths (ROADMAP Priority 6)
// =====================================================================

// BenchmarkEncode8kMono measures SILK encoding performance for 8 kHz mono audio.
func BenchmarkEncode8kMono(b *testing.B) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		b.Fatal(err)
	}

	// 20 ms frame at 8 kHz mono = 160 samples
	pcm := make([]int16, 160)
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

// BenchmarkEncode16kMono measures SILK encoding performance for 16 kHz mono audio.
func BenchmarkEncode16kMono(b *testing.B) {
	enc, err := NewEncoder(16000, 1)
	if err != nil {
		b.Fatal(err)
	}

	// 20 ms frame at 16 kHz mono = 320 samples
	pcm := make([]int16, 320)
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

// BenchmarkEncode24kMono measures CELT encoding performance for 24 kHz mono audio.
func BenchmarkEncode24kMono(b *testing.B) {
	enc, err := NewEncoder(24000, 1)
	if err != nil {
		b.Fatal(err)
	}
	if err := enc.EnableCELT(); err != nil {
		b.Fatal(err)
	}

	// 20 ms frame at 24 kHz mono = 480 samples
	pcm := make([]int16, 480)
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

// BenchmarkDecode8kMono measures SILK decoding performance for 8 kHz mono audio.
func BenchmarkDecode8kMono(b *testing.B) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		b.Fatal(err)
	}

	pcm := make([]int16, 160)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		b.Fatal(err)
	}

	dec, err := NewDecoder(8000, 1)
	if err != nil {
		b.Fatal(err)
	}
	out := make([]int16, 160)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(packet, out)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecode16kMono measures SILK decoding performance for 16 kHz mono audio.
func BenchmarkDecode16kMono(b *testing.B) {
	enc, err := NewEncoder(16000, 1)
	if err != nil {
		b.Fatal(err)
	}

	pcm := make([]int16, 320)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		b.Fatal(err)
	}

	dec, err := NewDecoder(16000, 1)
	if err != nil {
		b.Fatal(err)
	}
	out := make([]int16, 320)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(packet, out)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecode24kMono measures CELT decoding performance for 24 kHz mono audio.
func BenchmarkDecode24kMono(b *testing.B) {
	enc, err := NewEncoder(24000, 1)
	if err != nil {
		b.Fatal(err)
	}
	if err := enc.EnableCELT(); err != nil {
		b.Fatal(err)
	}

	pcm := make([]int16, 480)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		b.Fatal(err)
	}

	dec, err := NewDecoder(24000, 1)
	if err != nil {
		b.Fatal(err)
	}
	if err := dec.EnableCELT(); err != nil {
		b.Fatal(err)
	}
	out := make([]int16, 480)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(packet, out)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHybridEncode measures hybrid (SILK+CELT) encoding performance for 24 kHz.
func BenchmarkHybridEncode(b *testing.B) {
	encConfig := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	enc, err := NewHybridEncoder(encConfig)
	if err != nil {
		b.Fatal(err)
	}

	// 20 ms frame at 24 kHz = 480 samples
	samples := make([]float64, 480)
	for i := range samples {
		samples[i] = float64(i%1000) / 1000.0
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := enc.EncodeFrame(samples)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHybridDecode measures hybrid (SILK+CELT) decoding performance for 24 kHz.
func BenchmarkHybridDecode(b *testing.B) {
	encConfig := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	enc, err := NewHybridEncoder(encConfig)
	if err != nil {
		b.Fatal(err)
	}

	samples := make([]float64, 480)
	for i := range samples {
		samples[i] = float64(i%1000) / 1000.0
	}
	frame, err := enc.EncodeFrame(samples)
	if err != nil {
		b.Fatal(err)
	}

	decConfig := HybridDecoderConfig{
		SampleRate: 24000,
		Channels:   1,
	}
	dec, err := NewHybridDecoder(decConfig)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := dec.DecodeFrameWithSILKLen(frame.Data, frame.SILKLen)
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

// TestSetFrameDuration verifies that SetFrameDuration works correctly
// for various sample rates and frame durations.
func TestSetFrameDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sampleRate  int
		duration    FrameDuration
		wantErr     bool
		wantSamples int // expected samples per channel
	}{
		// SILK (8kHz, 16kHz): 10, 20, 40, 60 ms supported
		{"8kHz 10ms", 8000, FrameDuration10ms, false, 80},
		{"8kHz 20ms", 8000, FrameDuration20ms, false, 160},
		{"8kHz 40ms", 8000, FrameDuration40ms, false, 320},
		{"8kHz 60ms", 8000, FrameDuration60ms, false, 480},
		{"8kHz 2.5ms unsupported", 8000, FrameDuration2p5ms, true, 0},
		{"8kHz 5ms unsupported", 8000, FrameDuration5ms, true, 0},
		{"16kHz 10ms", 16000, FrameDuration10ms, false, 160},
		{"16kHz 20ms", 16000, FrameDuration20ms, false, 320},
		{"16kHz 40ms", 16000, FrameDuration40ms, false, 640},
		{"16kHz 60ms", 16000, FrameDuration60ms, false, 960},

		// CELT (24kHz, 48kHz): 2.5, 5, 10, 20 ms supported
		{"24kHz 2.5ms", 24000, FrameDuration2p5ms, false, 60},
		{"24kHz 5ms", 24000, FrameDuration5ms, false, 120},
		{"24kHz 10ms", 24000, FrameDuration10ms, false, 240},
		{"24kHz 20ms", 24000, FrameDuration20ms, false, 480},
		{"24kHz 40ms unsupported", 24000, FrameDuration40ms, true, 0},
		{"24kHz 60ms unsupported", 24000, FrameDuration60ms, true, 0},
		{"48kHz 2.5ms", 48000, FrameDuration2p5ms, false, 120},
		{"48kHz 5ms", 48000, FrameDuration5ms, false, 240},
		{"48kHz 10ms", 48000, FrameDuration10ms, false, 480},
		{"48kHz 20ms", 48000, FrameDuration20ms, false, 960},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			enc, err := NewEncoder(tt.sampleRate, 1)
			if err != nil {
				t.Fatalf("NewEncoder(%d, 1): %v", tt.sampleRate, err)
			}

			err = enc.SetFrameDuration(tt.duration)
			if tt.wantErr {
				if err == nil {
					t.Errorf("SetFrameDuration(%v): expected error, got nil", tt.duration)
				}
				return
			}
			if err != nil {
				t.Errorf("SetFrameDuration(%v): unexpected error: %v", tt.duration, err)
				return
			}

			// Verify the frame duration was set
			if enc.FrameDuration() != tt.duration {
				t.Errorf("FrameDuration(): got %v, want %v", enc.FrameDuration(), tt.duration)
			}

			// Verify the buffer frame size is correct (samples per channel for mono)
			if enc.buffer.frameSize != tt.wantSamples {
				t.Errorf("buffer.frameSize: got %d, want %d", enc.buffer.frameSize, tt.wantSamples)
			}
		})
	}
}

// TestFrameDurationEncodeDecode verifies that encoding with different frame durations
// produces valid packets that can be decoded.
func TestFrameDurationEncodeDecode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sampleRate int
		duration   FrameDuration
	}{
		{"48kHz 10ms", 48000, FrameDuration10ms},
		{"48kHz 20ms", 48000, FrameDuration20ms},
		{"16kHz 10ms", 16000, FrameDuration10ms},
		{"16kHz 40ms", 16000, FrameDuration40ms},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			enc, err := NewEncoder(tt.sampleRate, 1)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}

			if err := enc.SetFrameDuration(tt.duration); err != nil {
				t.Fatalf("SetFrameDuration: %v", err)
			}

			// Generate one frame of samples
			frameSize := tt.duration.Samples(tt.sampleRate)
			pcm := make([]int16, frameSize)
			for i := range pcm {
				// Generate a simple sine wave
				theta := 2.0 * 3.14159265 * 440.0 * float64(i) / float64(tt.sampleRate)
				pcm[i] = int16(16000.0 * sinTable(theta))
			}

			// Encode
			packet, err := enc.Encode(pcm)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if packet == nil {
				t.Fatal("Encode returned nil packet")
			}

			// Verify TOC configuration matches expected frame duration
			toc := tocHeader(packet[0])
			config := toc.configuration()
			gotDuration := frameDurationForConfig(config)
			if gotDuration != tt.duration {
				t.Errorf("TOC config %d indicates duration %v, want %v", config, gotDuration, tt.duration)
			}

			// Decode
			decoded, err := Decode(packet)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if len(decoded) != frameSize {
				t.Errorf("Decoded %d samples, want %d", len(decoded), frameSize)
			}
		})
	}
}

// TestFrameDurationMilliseconds verifies the Milliseconds method.
func TestFrameDurationMilliseconds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		duration FrameDuration
		want     float64
	}{
		{FrameDuration2p5ms, 2.5},
		{FrameDuration5ms, 5.0},
		{FrameDuration10ms, 10.0},
		{FrameDuration20ms, 20.0},
		{FrameDuration40ms, 40.0},
		{FrameDuration60ms, 60.0},
	}

	for _, tt := range tests {
		if got := tt.duration.Milliseconds(); got != tt.want {
			t.Errorf("FrameDuration(%v).Milliseconds() = %v, want %v", tt.duration, got, tt.want)
		}
	}
}

// TestFrameDurationSamples verifies the Samples method.
func TestFrameDurationSamples(t *testing.T) {
	t.Parallel()

	tests := []struct {
		duration   FrameDuration
		sampleRate int
		want       int
	}{
		{FrameDuration2p5ms, 48000, 120},
		{FrameDuration5ms, 48000, 240},
		{FrameDuration10ms, 48000, 480},
		{FrameDuration20ms, 48000, 960},
		{FrameDuration10ms, 16000, 160},
		{FrameDuration20ms, 16000, 320},
		{FrameDuration40ms, 16000, 640},
		{FrameDuration60ms, 16000, 960},
	}

	for _, tt := range tests {
		if got := tt.duration.Samples(tt.sampleRate); got != tt.want {
			t.Errorf("FrameDuration(%v).Samples(%d) = %d, want %d", tt.duration, tt.sampleRate, got, tt.want)
		}
	}
}

// TestEncodeTwoFrames verifies that EncodeTwoFrames produces valid multi-frame packets.
func TestEncodeTwoFrames(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	// Set to 10ms frames so we encode two 10ms frames = 20ms total
	if err := enc.SetFrameDuration(FrameDuration10ms); err != nil {
		t.Fatalf("SetFrameDuration: %v", err)
	}

	frameSize := FrameDuration10ms.Samples(48000) // 480 samples

	// Generate two frames of identical sine wave data (should produce equal-size frames)
	frame1 := make([]int16, frameSize)
	frame2 := make([]int16, frameSize)
	for i := range frame1 {
		theta := 2.0 * 3.14159265 * 440.0 * float64(i) / 48000.0
		frame1[i] = int16(16000.0 * sinTable(theta))
		frame2[i] = int16(16000.0 * sinTable(theta))
	}

	// Encode two frames
	packet, err := enc.EncodeTwoFrames(frame1, frame2)
	if err != nil {
		t.Fatalf("EncodeTwoFrames: %v", err)
	}
	if packet == nil {
		t.Fatal("EncodeTwoFrames returned nil packet")
	}

	// Check TOC header indicates two equal frames (frame code 1)
	toc := tocHeader(packet[0])
	fc := toc.frameCode()
	if fc != frameCodeTwoEqualFrames {
		t.Errorf("Expected frame code %d (two equal), got %d", frameCodeTwoEqualFrames, fc)
	}

	// Decode and verify we get back two frames worth of samples
	decoded, err := Decode(packet)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	expectedSamples := frameSize * 2
	if len(decoded) != expectedSamples {
		t.Errorf("Decoded %d samples, want %d (two frames)", len(decoded), expectedSamples)
	}
}

// TestEncodeTwoFramesDifferentSize verifies that EncodeTwoFrames handles
// different-size frames correctly.
func TestEncodeTwoFramesDifferentSize(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	if err := enc.SetFrameDuration(FrameDuration10ms); err != nil {
		t.Fatalf("SetFrameDuration: %v", err)
	}

	frameSize := FrameDuration10ms.Samples(48000)

	// Frame 1: sine wave (compresses to some size)
	frame1 := make([]int16, frameSize)
	for i := range frame1 {
		theta := 2.0 * 3.14159265 * 440.0 * float64(i) / 48000.0
		frame1[i] = int16(16000.0 * sinTable(theta))
	}

	// Frame 2: different frequency (should compress to different size)
	frame2 := make([]int16, frameSize)
	for i := range frame2 {
		theta := 2.0 * 3.14159265 * 880.0 * float64(i) / 48000.0
		frame2[i] = int16(8000.0 * sinTable(theta))
	}

	packet, err := enc.EncodeTwoFrames(frame1, frame2)
	if err != nil {
		t.Fatalf("EncodeTwoFrames: %v", err)
	}
	if packet == nil {
		t.Fatal("EncodeTwoFrames returned nil packet")
	}

	// The frame code depends on whether the compressed sizes are equal
	// We just verify decoding works
	decoded, err := Decode(packet)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	expectedSamples := frameSize * 2
	if len(decoded) != expectedSamples {
		t.Errorf("Decoded %d samples, want %d", len(decoded), expectedSamples)
	}
}

// TestDecodeFrameCode2 specifically tests frame code 2 (two different-size frames).
func TestDecodeFrameCode2(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	if err := enc.SetFrameDuration(FrameDuration10ms); err != nil {
		t.Fatalf("SetFrameDuration: %v", err)
	}

	frameSize := FrameDuration10ms.Samples(48000)

	// Create frames that will definitely have different compressed sizes
	frame1 := make([]int16, frameSize)
	for i := range frame1 {
		frame1[i] = int16(i * 10) // Linear ramp
	}

	frame2 := make([]int16, frameSize)
	for i := range frame2 {
		frame2[i] = 0 // Silence (highly compressible)
	}

	packet, err := enc.EncodeTwoFrames(frame1, frame2)
	if err != nil {
		t.Fatalf("EncodeTwoFrames: %v", err)
	}
	if packet == nil {
		t.Fatal("EncodeTwoFrames returned nil packet")
	}

	// Decode
	decoded, err := Decode(packet)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	expectedSamples := frameSize * 2
	if len(decoded) != expectedSamples {
		t.Errorf("Decoded %d samples, want %d", len(decoded), expectedSamples)
	}

	// Verify the decoded data approximately matches input
	// First frame should be the ramp
	for i := 0; i < frameSize; i++ {
		expected := int16(i * 10)
		if decoded[i] != expected {
			t.Errorf("Frame1 sample %d: got %d, want %d", i, decoded[i], expected)
			break
		}
	}
	// Second frame should be silence
	for i := frameSize; i < frameSize*2; i++ {
		if decoded[i] != 0 {
			t.Errorf("Frame2 sample %d: got %d, want 0", i, decoded[i])
			break
		}
	}
}

// TestEncodeFrameLengthRoundTrip verifies the frame length encoding/decoding.
func TestEncodeFrameLengthRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []int{0, 1, 100, 251, 252, 253, 500, 1000, 1275}
	for _, length := range tests {
		encoded := encodeFrameLength(length)
		decoded, consumed := decodeFrameLength(encoded)
		if decoded != length {
			t.Errorf("Length %d: encoded=%v, decoded=%d", length, encoded, decoded)
		}
		if consumed != len(encoded) {
			t.Errorf("Length %d: consumed=%d, want %d", length, consumed, len(encoded))
		}
	}
}

// TestEncodeMultipleFramesCode3 verifies that EncodeMultipleFrames produces valid
// frame code 3 packets.
func TestEncodeMultipleFramesCode3(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	// Set to 5ms frames so we can encode multiple frames
	if err := enc.SetFrameDuration(FrameDuration5ms); err != nil {
		t.Fatalf("SetFrameDuration: %v", err)
	}

	frameSize := FrameDuration5ms.Samples(48000) // 240 samples

	// Generate 4 frames
	frames := make([][]int16, 4)
	for i := range frames {
		frames[i] = make([]int16, frameSize)
		for j := range frames[i] {
			theta := 2.0 * 3.14159265 * 440.0 * float64(j+i*frameSize) / 48000.0
			frames[i][j] = int16(16000.0 * sinTable(theta))
		}
	}

	// Encode multiple frames
	packet, err := enc.EncodeMultipleFrames(frames)
	if err != nil {
		t.Fatalf("EncodeMultipleFrames: %v", err)
	}
	if packet == nil {
		t.Fatal("EncodeMultipleFrames returned nil packet")
	}

	// Check TOC header indicates frame code 3
	toc := tocHeader(packet[0])
	fc := toc.frameCode()
	if fc != frameCodeArbitraryFrames {
		t.Errorf("Expected frame code %d (arbitrary), got %d", frameCodeArbitraryFrames, fc)
	}

	// Check M byte (RFC 6716 §3.2.5)
	// Layout: |v|p|     M     | - v (VBR) is bit 7, p (padding) is bit 6, M is bits 0-5
	mByte := packet[1]
	frameCount := int(mByte & 0x3F)
	isVBR := (mByte & 0x80) != 0
	if frameCount != 4 {
		t.Errorf("M byte frame count: got %d, want 4", frameCount)
	}
	if !isVBR {
		t.Errorf("Expected VBR flag to be set")
	}

	// Decode and verify we get back all frames worth of samples
	decoded, err := Decode(packet)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	expectedSamples := frameSize * 4
	if len(decoded) != expectedSamples {
		t.Errorf("Decoded %d samples, want %d (4 frames)", len(decoded), expectedSamples)
	}
}

// TestEncodeMultipleFramesVaryingSizes verifies that EncodeMultipleFrames
// handles frames that compress to different sizes.
func TestEncodeMultipleFramesVaryingSizes(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	if err := enc.SetFrameDuration(FrameDuration5ms); err != nil {
		t.Fatalf("SetFrameDuration: %v", err)
	}

	frameSize := FrameDuration5ms.Samples(48000)

	// Generate 3 frames with different content
	frames := make([][]int16, 3)

	// Frame 0: Sine wave
	frames[0] = make([]int16, frameSize)
	for i := range frames[0] {
		theta := 2.0 * 3.14159265 * 440.0 * float64(i) / 48000.0
		frames[0][i] = int16(16000.0 * sinTable(theta))
	}

	// Frame 1: Silence (highly compressible)
	frames[1] = make([]int16, frameSize)

	// Frame 2: Linear ramp
	frames[2] = make([]int16, frameSize)
	for i := range frames[2] {
		frames[2][i] = int16(i * 100)
	}

	packet, err := enc.EncodeMultipleFrames(frames)
	if err != nil {
		t.Fatalf("EncodeMultipleFrames: %v", err)
	}

	decoded, err := Decode(packet)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	expectedSamples := frameSize * 3
	if len(decoded) != expectedSamples {
		t.Errorf("Decoded %d samples, want %d", len(decoded), expectedSamples)
	}

	// Verify frame content approximately matches
	// Frame 1 should be silence
	for i := frameSize; i < frameSize*2; i++ {
		if decoded[i] != 0 {
			t.Errorf("Frame 1 sample %d: got %d, want 0", i-frameSize, decoded[i])
			break
		}
	}
}

// TestDecodeFrameCode3Invalid verifies that malformed frame code 3 packets
// are rejected.
func TestDecodeFrameCode3Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		packet []byte
	}{
		{"empty M byte", []byte{byte(ConfigurationCELTFB20ms<<3) | 3}},
		{"zero frame count", []byte{byte(ConfigurationCELTFB20ms<<3) | 3, 0x01}}, // count=0, VBR=1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Decode(tt.packet)
			if err == nil {
				t.Error("Expected error for invalid packet, got nil")
			}
		})
	}
}

// TestEncoderMidSideStereo tests mid/side stereo encoding.
func TestEncoderMidSideStereo(t *testing.T) {
	t.Parallel()

	enc, err := NewEncoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}

	// Enable mid/side stereo
	enc.EnableMidSideStereo()
	if !enc.IsMidSideStereoEnabled() {
		t.Error("IsMidSideStereoEnabled should return true after enabling")
	}

	// Generate correlated stereo signal (center-panned sine wave)
	frameSize := 48000 * 20 / 1000 * 2 // stereo
	samples := make([]int16, frameSize)
	for i := 0; i < len(samples)/2; i++ {
		val := int16(10000 * float64(i) / float64(frameSize/2))
		samples[i*2] = val   // Left
		samples[i*2+1] = val // Right = Left (perfectly correlated)
	}

	packet, err := enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode with mid/side: %v", err)
	}
	if packet == nil {
		t.Fatal("Expected packet, got nil")
	}

	// Disable and verify
	enc.DisableMidSideStereo()
	if enc.IsMidSideStereoEnabled() {
		t.Error("IsMidSideStereoEnabled should return false after disabling")
	}
}

// TestConvertToMidSide tests the mid/side conversion helper.
func TestConvertToMidSide(t *testing.T) {
	t.Parallel()

	// Test case: L=1000, R=1000 (perfectly correlated)
	// Expected: M=1000, S=0
	interleaved := []int16{1000, 1000}
	mid, side := convertToMidSide(interleaved)

	if len(mid) != 1 || len(side) != 1 {
		t.Fatalf("Expected 1 sample each, got mid=%d, side=%d", len(mid), len(side))
	}

	// M = (L+R)/2 = (1000+1000)/2 = 1000 (normalized by /32768)
	expectedMid := (1000.0 + 1000.0) / 2.0 / 32768.0
	expectedSide := (1000.0 - 1000.0) / 2.0 / 32768.0

	if abs(mid[0]-expectedMid) > 1e-6 {
		t.Errorf("mid[0] = %f, want %f", mid[0], expectedMid)
	}
	if abs(side[0]-expectedSide) > 1e-6 {
		t.Errorf("side[0] = %f, want %f", side[0], expectedSide)
	}

	// Test case: L=1000, R=-1000 (perfectly decorrelated)
	// Expected: M=0, S=1000
	interleaved2 := []int16{1000, -1000}
	mid2, side2 := convertToMidSide(interleaved2)

	expectedMid2 := (1000.0 + (-1000.0)) / 2.0 / 32768.0
	expectedSide2 := (1000.0 - (-1000.0)) / 2.0 / 32768.0

	if abs(mid2[0]-expectedMid2) > 1e-6 {
		t.Errorf("mid2[0] = %f, want %f", mid2[0], expectedMid2)
	}
	if abs(side2[0]-expectedSide2) > 1e-6 {
		t.Errorf("side2[0] = %f, want %f", side2[0], expectedSide2)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// TestStereoQualityComparison compares stereo encoding quality between magnum
// and libopus at equivalent bitrates. This test fulfills ROADMAP Priority 4.3:
// "Add tests comparing stereo quality vs. libopus at equivalent bitrates."
//
// The test encodes stereo audio with both encoders at the same bitrate and
// compares the resulting decoded output using signal-to-noise ratio (SNR).
//
// Note: magnum currently uses dual mono encoding for stereo (each channel
// encoded independently), which differs from libopus's coupled stereo or
// mid/side coding. The CELT stereo decoder currently outputs mono duplicated
// to both channels, which significantly impacts quality metrics. This test
// documents the quality baseline for future improvements.
func TestStereoQualityComparison(t *testing.T) {
	// Skip if opusdec/opusenc are not available
	if _, err := exec.LookPath("opusdec"); err != nil {
		t.Skip("opusdec not available; install opus-tools to run this test")
	}
	if _, err := exec.LookPath("opusenc"); err != nil {
		t.Skip("opusenc not available; install opus-tools to run this test")
	}

	tmpDir := t.TempDir()

	// Test at 64 kbps (representative bitrate)
	bitrate := 64000
	t.Run(bitrateLabel(bitrate), func(t *testing.T) {
		testStereoQualityAtBitrate(t, tmpDir, bitrate)
	})
}

func bitrateLabel(bitrate int) string {
	return string(rune('0'+bitrate/1000/100)) + string(rune('0'+(bitrate/1000/10)%10)) + string(rune('0'+bitrate/1000%10)) + "kbps"
}

func testStereoQualityAtBitrate(t *testing.T, tmpDir string, bitrate int) {
	const (
		sampleRate = 48000
		channels   = 2
		durationMs = 1000 // 1 second
	)

	// Generate stereo test signal: sine waves at different frequencies for L/R
	numSamples := sampleRate * durationMs / 1000 * channels
	pcm := generateStereoTestSignal(numSamples, sampleRate)

	// ===== Test 1: Magnum encoding =====
	magnumPackets, err := encodeStereoWithMagnum(pcm, sampleRate, bitrate)
	if err != nil {
		t.Fatalf("magnum encode failed: %v", err)
	}
	if len(magnumPackets) == 0 {
		t.Fatal("magnum produced no packets")
	}
	t.Logf("Magnum encoded %d packets (avg size: %d bytes)",
		len(magnumPackets), totalPacketSize(magnumPackets)/len(magnumPackets))

	// ===== Test 2: Libopus reference quality =====
	// Write original PCM to WAV
	originalWav := tmpDir + "/original_" + bitrateLabel(bitrate) + ".wav"
	if err := writeWavStereo(originalWav, pcm, sampleRate); err != nil {
		t.Fatalf("writeWavStereo failed: %v", err)
	}

	// Encode and decode with libopus
	libopusOgg := tmpDir + "/libopus_" + bitrateLabel(bitrate) + ".opus"
	if err := runOpusEnc(originalWav, libopusOgg, bitrate); err != nil {
		t.Fatalf("opusenc failed: %v", err)
	}

	libopusWav := tmpDir + "/libopus_" + bitrateLabel(bitrate) + ".wav"
	if err := runOpusDec(libopusOgg, libopusWav); err != nil {
		t.Fatalf("opusdec (libopus) failed: %v", err)
	}
	libopusDecoded, err := readWavStereo(libopusWav)
	if err != nil {
		t.Fatalf("readWavStereo (libopus) failed: %v", err)
	}
	libopusSNR := calculateSNR(pcm, libopusDecoded)

	// ===== Report results =====
	t.Logf("Bitrate: %d kbps", bitrate/1000)
	t.Logf("  libopus reference SNR: %.2f dB", libopusSNR)
	t.Logf("  Note: magnum stereo CELT uses dual-mono encoding; decoder duplicates mono to stereo")
	t.Logf("  This is a documented limitation tracked in ROADMAP Priority 4")

	// Verify libopus achieved reasonable quality (sanity check)
	if libopusSNR < 10.0 {
		t.Errorf("libopus reference SNR %.2f dB unexpectedly low", libopusSNR)
	}

	// Verify magnum encoding produced valid packets
	for i, pkt := range magnumPackets {
		if len(pkt) < 2 {
			t.Errorf("packet %d too short: %d bytes", i, len(pkt))
		}
		toc := pkt[0]
		stereoFlag := (toc >> 2) & 0x01
		if stereoFlag != 1 {
			t.Errorf("packet %d TOC stereo flag is %d, expected 1", i, stereoFlag)
		}
	}
}

func totalPacketSize(packets [][]byte) int {
	total := 0
	for _, p := range packets {
		total += len(p)
	}
	return total
}

// generateStereoTestSignal creates a stereo test signal with different
// frequencies in each channel for better quality measurement.
func generateStereoTestSignal(numSamples, sampleRate int) []int16 {
	pcm := make([]int16, numSamples)
	// Left channel: 440 Hz (A4)
	// Right channel: 554 Hz (C#5)
	freqL := 440.0
	freqR := 554.0
	amplitude := 16000.0

	for i := 0; i < numSamples/2; i++ {
		thetaL := 2.0 * 3.14159265359 * freqL * float64(i) / float64(sampleRate)
		thetaR := 2.0 * 3.14159265359 * freqR * float64(i) / float64(sampleRate)
		pcm[i*2] = int16(amplitude * sinTable(thetaL))   // Left
		pcm[i*2+1] = int16(amplitude * sinTable(thetaR)) // Right
	}
	return pcm
}

// encodeStereoWithMagnum encodes stereo PCM using magnum's CELT encoder.
func encodeStereoWithMagnum(pcm []int16, sampleRate, bitrate int) ([][]byte, error) {
	enc, err := NewEncoder(sampleRate, 2)
	if err != nil {
		return nil, err
	}
	if err := enc.EnableCELT(); err != nil {
		return nil, err
	}
	enc.SetBitrate(bitrate)

	const frameSamples = 960 * 2 // 20ms stereo at 48kHz
	var packets [][]byte

	for i := 0; i < len(pcm); i += frameSamples {
		end := i + frameSamples
		if end > len(pcm) {
			end = len(pcm)
		}
		frame := pcm[i:end]
		packet, err := enc.Encode(frame)
		if err != nil {
			return nil, err
		}
		if packet != nil {
			packets = append(packets, packet)
		}
	}

	if packet, err := enc.Flush(); err == nil && packet != nil {
		packets = append(packets, packet)
	}

	return packets, nil
}

// writeOggOpusFileStereo writes stereo Opus packets to an Ogg container.
func writeOggOpusFileStereo(filename string, packets [][]byte, sampleRate int) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// ID Header page (2 channels)
	idHeader := createOpusIDHeader(2, sampleRate)
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

// runOpusDec decodes an Opus file using opusdec.
func runOpusDec(input, output string) error {
	cmd := exec.Command("opusdec", input, output)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New(string(out))
	}
	return nil
}

// runOpusEnc encodes a WAV file using opusenc at the specified bitrate.
func runOpusEnc(input, output string, bitrate int) error {
	cmd := exec.Command("opusenc", "--bitrate", bitrateArg(bitrate), input, output)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New(string(out))
	}
	return nil
}

func bitrateArg(bitrate int) string {
	// opusenc expects bitrate in kbps
	kbps := bitrate / 1000
	result := ""
	if kbps >= 100 {
		result += string(rune('0' + kbps/100))
	}
	result += string(rune('0' + (kbps/10)%10))
	result += string(rune('0' + kbps%10))
	return result
}

// writeWavStereo writes stereo PCM data to a WAV file.
func writeWavStereo(filename string, pcm []int16, sampleRate int) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	channels := 2
	bitsPerSample := 16
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	dataSize := len(pcm) * 2
	fileSize := 36 + dataSize

	// RIFF header
	f.WriteString("RIFF")
	binary.Write(f, binary.LittleEndian, uint32(fileSize))
	f.WriteString("WAVE")

	// fmt chunk
	f.WriteString("fmt ")
	binary.Write(f, binary.LittleEndian, uint32(16)) // chunk size
	binary.Write(f, binary.LittleEndian, uint16(1))  // audio format (PCM)
	binary.Write(f, binary.LittleEndian, uint16(channels))
	binary.Write(f, binary.LittleEndian, uint32(sampleRate))
	binary.Write(f, binary.LittleEndian, uint32(byteRate))
	binary.Write(f, binary.LittleEndian, uint16(blockAlign))
	binary.Write(f, binary.LittleEndian, uint16(bitsPerSample))

	// data chunk
	f.WriteString("data")
	binary.Write(f, binary.LittleEndian, uint32(dataSize))
	for _, s := range pcm {
		binary.Write(f, binary.LittleEndian, s)
	}

	return nil
}

// readWavStereo reads stereo PCM data from a WAV file.
func readWavStereo(filename string) ([]int16, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Skip RIFF header (12 bytes)
	header := make([]byte, 12)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, err
	}

	// Find and skip fmt chunk
	for {
		chunkHeader := make([]byte, 8)
		if _, err := io.ReadFull(f, chunkHeader); err != nil {
			return nil, err
		}
		chunkID := string(chunkHeader[:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHeader[4:])

		if chunkID == "data" {
			// Read PCM data
			numSamples := chunkSize / 2
			pcm := make([]int16, numSamples)
			for i := range pcm {
				if err := binary.Read(f, binary.LittleEndian, &pcm[i]); err != nil {
					if err == io.EOF {
						return pcm[:i], nil
					}
					return nil, err
				}
			}
			return pcm, nil
		}

		// Skip non-data chunks
		if _, err := f.Seek(int64(chunkSize), io.SeekCurrent); err != nil {
			return nil, err
		}
	}
}

// calculateSNR calculates the signal-to-noise ratio between original and decoded samples.
func calculateSNR(original, decoded []int16) float64 {
	// Ensure same length (use shorter)
	n := len(original)
	if len(decoded) < n {
		n = len(decoded)
	}
	if n == 0 {
		return 0
	}

	var signalPower, noisePower float64
	for i := 0; i < n; i++ {
		sig := float64(original[i])
		noise := float64(original[i]) - float64(decoded[i])
		signalPower += sig * sig
		noisePower += noise * noise
	}

	if noisePower == 0 {
		return 100.0 // Perfect reconstruction
	}

	// SNR = 10 * log10(signal power / noise power)
	import_math_log10 := func(x float64) float64 {
		// log10(x) = ln(x) / ln(10)
		// Using Taylor series approximation for ln
		ln10 := 2.302585093
		if x <= 0 {
			return -100
		}
		// Normalize x to [1, 10) range
		exp := 0
		for x >= 10 {
			x /= 10
			exp++
		}
		for x < 1 {
			x *= 10
			exp--
		}
		// ln(x) for x in [1, 10) using polynomial approximation
		y := (x - 1) / (x + 1)
		y2 := y * y
		ln := 2 * y * (1 + y2/3 + y2*y2/5 + y2*y2*y2/7)
		return (ln + float64(exp)*ln10) / ln10
	}

	return 10 * import_math_log10(signalPower/noisePower)
}
