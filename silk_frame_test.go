// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file contains tests for SILK frame encoding.

package magnum

import (
	"math"
	"testing"
)

// TestNewSILKFrameEncoder tests SILK encoder creation.
func TestNewSILKFrameEncoder(t *testing.T) {
	tests := []struct {
		name    string
		config  SILKFrameConfig
		wantErr bool
	}{
		{
			name: "8kHz mono",
			config: SILKFrameConfig{
				SampleRate: 8000,
				Channels:   1,
				FrameSize:  160, // 20ms at 8kHz
				Bitrate:    12000,
			},
			wantErr: false,
		},
		{
			name: "16kHz mono",
			config: SILKFrameConfig{
				SampleRate: 16000,
				Channels:   1,
				FrameSize:  320, // 20ms at 16kHz
				Bitrate:    24000,
			},
			wantErr: false,
		},
		{
			name: "16kHz stereo",
			config: SILKFrameConfig{
				SampleRate: 16000,
				Channels:   2,
				FrameSize:  320,
				Bitrate:    32000,
			},
			wantErr: false,
		},
		{
			name: "invalid sample rate",
			config: SILKFrameConfig{
				SampleRate: 48000, // SILK doesn't support 48kHz
				Channels:   1,
				FrameSize:  960,
				Bitrate:    64000,
			},
			wantErr: true,
		},
		{
			name: "invalid channels",
			config: SILKFrameConfig{
				SampleRate: 16000,
				Channels:   3,
				FrameSize:  320,
				Bitrate:    24000,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewSILKFrameEncoder(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if enc == nil {
				t.Error("encoder is nil")
			}
		})
	}
}

// TestSILKFrameEncoder_EncodeFrame tests basic frame encoding.
func TestSILKFrameEncoder_EncodeFrame(t *testing.T) {
	config := SILKFrameConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameSize:  320,
		Bitrate:    24000,
	}

	enc, err := NewSILKFrameEncoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameEncoder error: %v", err)
	}

	// Generate test signal (200 Hz sine wave)
	samples := make([]float64, config.FrameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*200*float64(i)/float64(config.SampleRate))
	}

	frame, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("EncodeFrame error: %v", err)
	}

	if frame == nil {
		t.Fatal("EncodeFrame returned nil")
	}
	if len(frame.Data) == 0 {
		t.Error("encoded data is empty")
	}

	t.Logf("Encoded frame: %d bytes, voiced=%v", len(frame.Data), frame.IsVoiced)
}

// TestSILKFrameEncoder_Silence tests encoding silence.
func TestSILKFrameEncoder_Silence(t *testing.T) {
	config := SILKFrameConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameSize:  320,
		Bitrate:    24000,
	}

	enc, err := NewSILKFrameEncoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameEncoder error: %v", err)
	}

	// Generate silence
	samples := make([]float64, config.FrameSize)

	frame, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("EncodeFrame error: %v", err)
	}

	if frame == nil {
		t.Fatal("EncodeFrame returned nil")
	}

	// Silence frames should be unvoiced
	if frame.IsVoiced {
		t.Error("silence should be detected as unvoiced")
	}

	t.Logf("Silence frame: %d bytes", len(frame.Data))
}

// TestSILKFrameEncoder_NarrowBand tests 8kHz encoding.
func TestSILKFrameEncoder_NarrowBand(t *testing.T) {
	config := SILKFrameConfig{
		SampleRate: 8000,
		Channels:   1,
		FrameSize:  160, // 20ms at 8kHz
		Bitrate:    12000,
	}

	enc, err := NewSILKFrameEncoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameEncoder error: %v", err)
	}

	// Generate test signal
	samples := make([]float64, config.FrameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*200*float64(i)/float64(config.SampleRate))
	}

	frame, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("EncodeFrame error: %v", err)
	}

	if frame == nil {
		t.Fatal("EncodeFrame returned nil")
	}

	t.Logf("Narrowband frame: %d bytes", len(frame.Data))
}

// TestSILKFrameEncoder_InvalidFrameSize tests error on wrong frame size.
func TestSILKFrameEncoder_InvalidFrameSize(t *testing.T) {
	config := SILKFrameConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameSize:  320,
		Bitrate:    24000,
	}

	enc, err := NewSILKFrameEncoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameEncoder error: %v", err)
	}

	// Wrong frame size
	samples := make([]float64, 160)

	_, err = enc.EncodeFrame(samples)
	if err == nil {
		t.Error("expected error for wrong frame size")
	}
}

// TestSILKFrameEncoder_MultipleFrames tests encoding multiple frames.
func TestSILKFrameEncoder_MultipleFrames(t *testing.T) {
	config := SILKFrameConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameSize:  320,
		Bitrate:    24000,
	}

	enc, err := NewSILKFrameEncoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameEncoder error: %v", err)
	}

	// Encode multiple frames
	for frame := 0; frame < 10; frame++ {
		samples := make([]float64, config.FrameSize)
		freq := 200.0 + float64(frame)*50 // Varying frequency
		for i := range samples {
			samples[i] = 0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(config.SampleRate))
		}

		encoded, err := enc.EncodeFrame(samples)
		if err != nil {
			t.Fatalf("Frame %d: EncodeFrame error: %v", frame, err)
		}
		if encoded == nil {
			t.Fatalf("Frame %d: encoded nil", frame)
		}
	}
}

// TestSILKFrameEncoder_WithLBRR tests encoding with LBRR enabled.
func TestSILKFrameEncoder_WithLBRR(t *testing.T) {
	config := SILKFrameConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameSize:  320,
		Bitrate:    24000,
	}

	enc, err := NewSILKFrameEncoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameEncoder error: %v", err)
	}

	// Enable LBRR
	enc.EnableLBRR(LBRRModeLow)

	// Encode first frame (no LBRR yet, need history)
	samples := make([]float64, config.FrameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*200*float64(i)/float64(config.SampleRate))
	}

	frame1, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("Frame 1 error: %v", err)
	}

	// Encode second frame (should have LBRR from first)
	frame2, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("Frame 2 error: %v", err)
	}

	t.Logf("Frame 1: %d bytes, LBRR=%v", len(frame1.Data), frame1.HasLBRR)
	t.Logf("Frame 2: %d bytes, LBRR=%v", len(frame2.Data), frame2.HasLBRR)

	// Second frame should have LBRR
	if !frame2.HasLBRR {
		t.Error("Frame 2 should have LBRR data")
	}
}

// TestSILKFrameEncoder_Reset tests encoder reset.
func TestSILKFrameEncoder_Reset(t *testing.T) {
	config := SILKFrameConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameSize:  320,
		Bitrate:    24000,
	}

	enc, err := NewSILKFrameEncoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameEncoder error: %v", err)
	}

	// Encode a frame
	samples := make([]float64, config.FrameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*200*float64(i)/float64(config.SampleRate))
	}
	_, _ = enc.EncodeFrame(samples)

	// Reset
	enc.Reset()

	// Should be able to encode again
	frame, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("After reset: EncodeFrame error: %v", err)
	}
	if frame == nil {
		t.Error("After reset: encoded nil")
	}
}

// TestSILKFrameEncoder_SetBitrate tests bitrate setting.
func TestSILKFrameEncoder_SetBitrate(t *testing.T) {
	config := SILKFrameConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameSize:  320,
		Bitrate:    24000,
	}

	enc, err := NewSILKFrameEncoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameEncoder error: %v", err)
	}

	enc.SetBitrate(12000)
	if enc.config.Bitrate != 12000 {
		t.Errorf("bitrate = %d, want 12000", enc.config.Bitrate)
	}
}

// BenchmarkSILKEncodeFrame benchmarks SILK frame encoding.
func BenchmarkSILKEncodeFrame(b *testing.B) {
	config := SILKFrameConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameSize:  320,
		Bitrate:    24000,
	}

	enc, err := NewSILKFrameEncoder(config)
	if err != nil {
		b.Fatalf("NewSILKFrameEncoder error: %v", err)
	}

	samples := make([]float64, config.FrameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*200*float64(i)/float64(config.SampleRate))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.EncodeFrame(samples)
	}
}

// TestNewSILKFrameDecoder tests SILK decoder creation.
func TestNewSILKFrameDecoder(t *testing.T) {
	tests := []struct {
		name    string
		config  SILKFrameConfig
		wantErr bool
	}{
		{
			name: "8kHz mono",
			config: SILKFrameConfig{
				SampleRate: 8000,
				Channels:   1,
				FrameSize:  160,
				Bitrate:    12000,
			},
			wantErr: false,
		},
		{
			name: "16kHz mono",
			config: SILKFrameConfig{
				SampleRate: 16000,
				Channels:   1,
				FrameSize:  320,
				Bitrate:    24000,
			},
			wantErr: false,
		},
		{
			name: "invalid sample rate",
			config: SILKFrameConfig{
				SampleRate: 48000,
				Channels:   1,
				FrameSize:  960,
				Bitrate:    64000,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := NewSILKFrameDecoder(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if dec == nil {
				t.Error("decoder is nil")
			}
		})
	}
}

// TestSILKFrameDecoder_DecodeFrame tests basic decoding.
func TestSILKFrameDecoder_DecodeFrame(t *testing.T) {
	config := SILKFrameConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameSize:  320,
		Bitrate:    24000,
	}

	// Create encoder and decoder
	enc, err := NewSILKFrameEncoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameEncoder error: %v", err)
	}

	dec, err := NewSILKFrameDecoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameDecoder error: %v", err)
	}

	// Generate test signal
	samples := make([]float64, config.FrameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*200*float64(i)/float64(config.SampleRate))
	}

	// Encode
	encoded, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("EncodeFrame error: %v", err)
	}

	// Decode
	decoded, err := dec.DecodeFrame(encoded.Data)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}

	if len(decoded) != config.FrameSize {
		t.Errorf("decoded length = %d, want %d", len(decoded), config.FrameSize)
	}

	t.Logf("Encoded: %d bytes, Decoded: %d samples", len(encoded.Data), len(decoded))
}

// TestSILKFrameDecoder_PacketLoss tests PLC on packet loss.
func TestSILKFrameDecoder_PacketLoss(t *testing.T) {
	config := SILKFrameConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameSize:  320,
		Bitrate:    24000,
	}

	dec, err := NewSILKFrameDecoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameDecoder error: %v", err)
	}

	// Simulate packet loss with empty data
	concealed, err := dec.DecodeFrame(nil)
	if err != nil {
		t.Fatalf("DecodeFrame with nil error: %v", err)
	}

	if len(concealed) != config.FrameSize {
		t.Errorf("concealed length = %d, want %d", len(concealed), config.FrameSize)
	}
}

// TestSILKFrameDecoder_Reset tests decoder reset.
func TestSILKFrameDecoder_Reset(t *testing.T) {
	config := SILKFrameConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameSize:  320,
		Bitrate:    24000,
	}

	dec, err := NewSILKFrameDecoder(config)
	if err != nil {
		t.Fatalf("NewSILKFrameDecoder error: %v", err)
	}

	// Reset should not panic
	dec.Reset()

	// Should be able to decode after reset
	_, err = dec.DecodeFrame(nil)
	if err != nil {
		t.Fatalf("After reset: DecodeFrame error: %v", err)
	}
}
