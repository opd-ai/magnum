package magnum

import (
	"math"
	"testing"
)

func TestCELTFrameEncoderCreation(t *testing.T) {
	testCases := []struct {
		name      string
		config    CELTFrameConfig
		expectErr bool
	}{
		{
			name: "valid 48kHz config",
			config: CELTFrameConfig{
				SampleRate: 48000,
				Channels:   1,
				FrameSize:  960,
				Bitrate:    64000,
			},
			expectErr: false,
		},
		{
			name: "valid 24kHz config",
			config: CELTFrameConfig{
				SampleRate: 24000,
				Channels:   2,
				FrameSize:  480,
				Bitrate:    32000,
			},
			expectErr: false,
		},
		{
			name: "invalid sample rate",
			config: CELTFrameConfig{
				SampleRate: 8000,
				Channels:   1,
				FrameSize:  960,
				Bitrate:    64000,
			},
			expectErr: true,
		},
		{
			name: "invalid channels",
			config: CELTFrameConfig{
				SampleRate: 48000,
				Channels:   0,
				FrameSize:  960,
				Bitrate:    64000,
			},
			expectErr: true,
		},
		{
			name: "invalid frame size",
			config: CELTFrameConfig{
				SampleRate: 48000,
				Channels:   1,
				FrameSize:  100,
				Bitrate:    64000,
			},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewCELTFrameEncoder(tc.config)
			if tc.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if enc == nil {
					t.Error("encoder is nil")
				}
			}
		})
	}
}

func TestCELTFrameDecoderCreation(t *testing.T) {
	config := CELTFrameConfig{
		SampleRate: 48000,
		Channels:   1,
		FrameSize:  960,
		Bitrate:    64000,
	}

	dec, err := NewCELTFrameDecoder(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec == nil {
		t.Fatal("decoder is nil")
	}
}

func TestCELTFrameEncodeSilence(t *testing.T) {
	config := CELTFrameConfig{
		SampleRate: 48000,
		Channels:   1,
		FrameSize:  960,
		Bitrate:    64000,
	}

	enc, err := NewCELTFrameEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	// Create silence frame
	samples := make([]float64, config.FrameSize)

	frame, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("failed to encode silence: %v", err)
	}

	if !frame.IsSilence {
		t.Error("expected silence frame flag to be set")
	}
	if frame.Bits != 1 {
		t.Errorf("silence frame should use 1 bit, got %d", frame.Bits)
	}
}

func TestCELTFrameEncodeSine(t *testing.T) {
	config := CELTFrameConfig{
		SampleRate: 48000,
		Channels:   1,
		FrameSize:  960,
		Bitrate:    64000,
	}

	enc, err := NewCELTFrameEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	// Create sine wave at 1 kHz
	samples := make([]float64, config.FrameSize)
	freq := 1000.0 // 1 kHz
	for i := range samples {
		t := float64(i) / float64(config.SampleRate)
		samples[i] = 0.5 * math.Sin(2*math.Pi*freq*t)
	}

	frame, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("failed to encode sine: %v", err)
	}

	if frame.IsSilence {
		t.Error("sine wave should not be detected as silence")
	}
	if len(frame.Data) == 0 {
		t.Error("encoded data should not be empty")
	}
}

func TestCELTFrameDecodeSilence(t *testing.T) {
	config := CELTFrameConfig{
		SampleRate: 48000,
		Channels:   1,
		FrameSize:  960,
		Bitrate:    64000,
	}

	enc, err := NewCELTFrameEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	dec, err := NewCELTFrameDecoder(config)
	if err != nil {
		t.Fatalf("failed to create decoder: %v", err)
	}

	// Encode silence
	samples := make([]float64, config.FrameSize)
	frame, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("failed to encode: %v", err)
	}

	// Decode
	decoded, err := dec.DecodeFrame(frame.Data)
	if err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	// Verify decoded samples are close to zero
	for i, s := range decoded {
		if math.Abs(s) > 0.01 {
			t.Errorf("decoded sample %d = %f, expected ~0", i, s)
			break
		}
	}
}

func TestCELTFrameEncodeDecodeInvalidInput(t *testing.T) {
	config := CELTFrameConfig{
		SampleRate: 48000,
		Channels:   1,
		FrameSize:  960,
		Bitrate:    64000,
	}

	enc, _ := NewCELTFrameEncoder(config)
	dec, _ := NewCELTFrameDecoder(config)

	// Test encoder with wrong frame size
	wrongSamples := make([]float64, 480)
	_, err := enc.EncodeFrame(wrongSamples)
	if err == nil {
		t.Error("expected error for wrong frame size")
	}

	// Test decoder with empty data
	_, err = dec.DecodeFrame([]byte{})
	if err == nil {
		t.Error("expected error for empty data")
	}
}

func TestCELTFrameMultipleFrames(t *testing.T) {
	config := CELTFrameConfig{
		SampleRate: 48000,
		Channels:   1,
		FrameSize:  960,
		Bitrate:    64000,
	}

	enc, err := NewCELTFrameEncoder(config)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}

	// Encode multiple frames with varying content
	for frameNum := 0; frameNum < 5; frameNum++ {
		samples := make([]float64, config.FrameSize)

		// Create varying content
		freq := float64(200 + frameNum*100)
		amplitude := 0.3 + float64(frameNum)*0.1
		for i := range samples {
			t := float64(i) / float64(config.SampleRate)
			samples[i] = amplitude * math.Sin(2*math.Pi*freq*t)
		}

		frame, err := enc.EncodeFrame(samples)
		if err != nil {
			t.Fatalf("frame %d: failed to encode: %v", frameNum, err)
		}

		if frame.IsSilence {
			t.Errorf("frame %d: unexpected silence flag", frameNum)
		}
	}
}

func TestDetectTransient(t *testing.T) {
	// Test transient detection
	n := 960

	// Steady signal - no transient
	steady := make([]float64, n)
	for i := range steady {
		steady[i] = 0.5
	}
	prev := make([]float64, n)
	for i := range prev {
		prev[i] = 0.5
	}
	if detectTransient(steady, prev) {
		t.Error("steady signal should not be detected as transient")
	}

	// Transient signal - sudden energy change in last quarter
	transient := make([]float64, n)
	for i := range transient {
		if i >= 3*n/4 {
			transient[i] = 5.0 // High energy in last quarter
		} else {
			transient[i] = 0.1
		}
	}
	if !detectTransient(transient, prev) {
		t.Error("signal with sudden energy increase should be detected as transient")
	}
}

func TestComputeFrameEnergy(t *testing.T) {
	// Test with known values
	samples := []float64{1.0, 2.0, 3.0, 4.0}
	energy := computeFrameEnergy(samples)

	// Expected: (1 + 4 + 9 + 16) / 4 = 30/4 = 7.5
	expected := 7.5
	if math.Abs(energy-expected) > 1e-10 {
		t.Errorf("energy = %f, want %f", energy, expected)
	}

	// Test silence
	silence := make([]float64, 100)
	silenceEnergy := computeFrameEnergy(silence)
	if silenceEnergy > 1e-10 {
		t.Errorf("silence energy = %f, want ~0", silenceEnergy)
	}
}

func TestComputeLM(t *testing.T) {
	testCases := []struct {
		frameSize  int
		expectedLM int
	}{
		{120, 0},
		{240, 1},
		{480, 2},
		{960, 3},
	}

	for _, tc := range testCases {
		lm := computeLM(tc.frameSize)
		if lm != tc.expectedLM {
			t.Errorf("computeLM(%d) = %d, want %d", tc.frameSize, lm, tc.expectedLM)
		}
	}
}

func TestIsValidFrameSize(t *testing.T) {
	valid := []int{120, 240, 480, 960}
	invalid := []int{100, 200, 500, 1000, 0, -1}

	for _, size := range valid {
		if !isValidFrameSize(size) {
			t.Errorf("isValidFrameSize(%d) = false, want true", size)
		}
	}

	for _, size := range invalid {
		if isValidFrameSize(size) {
			t.Errorf("isValidFrameSize(%d) = true, want false", size)
		}
	}
}

func BenchmarkCELTFrameEncode(b *testing.B) {
	config := CELTFrameConfig{
		SampleRate: 48000,
		Channels:   1,
		FrameSize:  960,
		Bitrate:    64000,
	}

	enc, _ := NewCELTFrameEncoder(config)

	samples := make([]float64, config.FrameSize)
	for i := range samples {
		t := float64(i) / float64(config.SampleRate)
		samples[i] = 0.5 * math.Sin(2*math.Pi*1000*t)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc.EncodeFrame(samples)
	}
}
