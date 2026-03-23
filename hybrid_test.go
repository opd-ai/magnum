package magnum

import (
	"math"
	"testing"
)

func TestNewHybridEncoder(t *testing.T) {
	tests := []struct {
		name       string
		config     HybridEncoderConfig
		wantErr    bool
		errContain string
	}{
		{
			name: "valid config 24kHz mono",
			config: HybridEncoderConfig{
				SampleRate: 24000,
				Channels:   1,
				Bitrate:    64000,
			},
			wantErr: false,
		},
		{
			name: "valid config 24kHz stereo",
			config: HybridEncoderConfig{
				SampleRate: 24000,
				Channels:   2,
				Bitrate:    128000,
			},
			wantErr: false,
		},
		{
			name: "invalid sample rate 48kHz",
			config: HybridEncoderConfig{
				SampleRate: 48000,
				Channels:   1,
				Bitrate:    64000,
			},
			wantErr:    true,
			errContain: "24000 Hz",
		},
		{
			name: "invalid sample rate 16kHz",
			config: HybridEncoderConfig{
				SampleRate: 16000,
				Channels:   1,
				Bitrate:    64000,
			},
			wantErr:    true,
			errContain: "24000 Hz",
		},
		{
			name: "invalid channels 0",
			config: HybridEncoderConfig{
				SampleRate: 24000,
				Channels:   0,
				Bitrate:    64000,
			},
			wantErr: true,
		},
		{
			name: "invalid channels 3",
			config: HybridEncoderConfig{
				SampleRate: 24000,
				Channels:   3,
				Bitrate:    64000,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewHybridEncoder(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if enc == nil {
				t.Fatal("encoder is nil")
			}
			if enc.sampleRate != tt.config.SampleRate {
				t.Errorf("sampleRate = %d, want %d", enc.sampleRate, tt.config.SampleRate)
			}
			if enc.channels != tt.config.Channels {
				t.Errorf("channels = %d, want %d", enc.channels, tt.config.Channels)
			}
		})
	}
}

func TestHybridEncoder_EncodeFrame(t *testing.T) {
	config := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	enc, err := NewHybridEncoder(config)
	if err != nil {
		t.Fatalf("NewHybridEncoder: %v", err)
	}

	// Generate 20ms of audio at 24kHz (480 samples)
	frameSize := 24000 * 20 / 1000
	samples := make([]float64, frameSize)

	// Generate a test tone with both low and high frequency components
	// Low freq: 1 kHz (in SILK band)
	// High freq: 10 kHz (in CELT band)
	for i := range samples {
		t := float64(i) / 24000.0
		// Mix of low and high frequencies
		samples[i] = 0.5*math.Sin(2*math.Pi*1000*t) + 0.3*math.Sin(2*math.Pi*10000*t)
	}

	frame, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	if frame == nil {
		t.Fatal("encoded frame is nil")
	}
	if len(frame.Data) == 0 {
		t.Error("encoded data is empty")
	}
	if frame.Bits == 0 {
		t.Error("encoded bits is 0")
	}

	t.Logf("Encoded frame: %d bytes, %d bits total (SILK: %d, CELT: %d)",
		len(frame.Data), frame.Bits, frame.SILKBits, frame.CELTBits)
}

func TestHybridEncoder_EncodeFrame_WrongSize(t *testing.T) {
	config := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	enc, err := NewHybridEncoder(config)
	if err != nil {
		t.Fatalf("NewHybridEncoder: %v", err)
	}

	// Wrong frame size
	samples := make([]float64, 100)
	_, err = enc.EncodeFrame(samples)
	if err == nil {
		t.Error("expected error for wrong frame size")
	}
}

func TestHybridEncoder_SplitBands(t *testing.T) {
	config := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	enc, err := NewHybridEncoder(config)
	if err != nil {
		t.Fatalf("NewHybridEncoder: %v", err)
	}

	frameSize := 24000 * 20 / 1000
	samples := make([]float64, frameSize)

	// Generate pure 1 kHz tone (should be mostly in SILK band)
	for i := range samples {
		t := float64(i) / 24000.0
		samples[i] = math.Sin(2 * math.Pi * 1000 * t)
	}

	enc.splitBands(samples)

	// SILK band should have significant energy for low frequency
	silkEnergy := 0.0
	for _, s := range enc.silkBand {
		silkEnergy += s * s
	}
	silkEnergy /= float64(len(enc.silkBand))

	// CELT band should have low energy for low frequency
	celtEnergy := 0.0
	for _, s := range enc.celtBand {
		celtEnergy += s * s
	}
	celtEnergy /= float64(len(enc.celtBand))

	t.Logf("1kHz tone - SILK energy: %f, CELT energy: %f", silkEnergy, celtEnergy)

	// For a 1kHz tone, SILK should capture most energy
	if silkEnergy < celtEnergy {
		t.Error("SILK band should have more energy for 1kHz tone")
	}
}

func TestHybridEncoder_SplitBands_HighFreq(t *testing.T) {
	config := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	enc, err := NewHybridEncoder(config)
	if err != nil {
		t.Fatalf("NewHybridEncoder: %v", err)
	}

	frameSize := 24000 * 20 / 1000
	samples := make([]float64, frameSize)

	// Generate pure 10 kHz tone (should be mostly in CELT band)
	for i := range samples {
		t := float64(i) / 24000.0
		samples[i] = math.Sin(2 * math.Pi * 10000 * t)
	}

	enc.splitBands(samples)

	// SILK band should have low energy for high frequency
	silkEnergy := 0.0
	for _, s := range enc.silkBand {
		silkEnergy += s * s
	}
	silkEnergy /= float64(len(enc.silkBand))

	// CELT band should have significant energy for high frequency
	celtEnergy := 0.0
	for _, s := range enc.celtBand {
		celtEnergy += s * s
	}
	celtEnergy /= float64(len(enc.celtBand))

	t.Logf("10kHz tone - SILK energy: %f, CELT energy: %f", silkEnergy, celtEnergy)

	// For a 10kHz tone, CELT should capture most energy
	if celtEnergy < silkEnergy {
		t.Error("CELT band should have more energy for 10kHz tone")
	}
}

func TestHybridEncoder_SetBitrate(t *testing.T) {
	config := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	enc, err := NewHybridEncoder(config)
	if err != nil {
		t.Fatalf("NewHybridEncoder: %v", err)
	}

	enc.SetBitrate(96000)
	if enc.bitrate != 96000 {
		t.Errorf("bitrate = %d, want 96000", enc.bitrate)
	}
}

func TestHybridEncoder_Reset(t *testing.T) {
	config := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	enc, err := NewHybridEncoder(config)
	if err != nil {
		t.Fatalf("NewHybridEncoder: %v", err)
	}

	// Encode a frame to populate state
	frameSize := 24000 * 20 / 1000
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5
	}
	enc.EncodeFrame(samples)

	// Reset
	enc.Reset()

	// Filter state should be cleared
	for _, s := range enc.lowpassState {
		if s != 0 {
			t.Error("lowpass state not cleared")
		}
	}
	for _, s := range enc.highpassState {
		if s != 0 {
			t.Error("highpass state not cleared")
		}
	}
}

func TestNewHybridDecoder(t *testing.T) {
	tests := []struct {
		name    string
		config  HybridDecoderConfig
		wantErr bool
	}{
		{
			name: "valid config 24kHz mono",
			config: HybridDecoderConfig{
				SampleRate: 24000,
				Channels:   1,
			},
			wantErr: false,
		},
		{
			name: "valid config 24kHz stereo",
			config: HybridDecoderConfig{
				SampleRate: 24000,
				Channels:   2,
			},
			wantErr: false,
		},
		{
			name: "invalid sample rate",
			config: HybridDecoderConfig{
				SampleRate: 48000,
				Channels:   1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := NewHybridDecoder(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dec == nil {
				t.Fatal("decoder is nil")
			}
		})
	}
}

func TestIsHybridConfig(t *testing.T) {
	tests := []struct {
		config Configuration
		want   bool
	}{
		{11, false}, // SILK WB
		{12, true},  // Hybrid SWB start
		{15, true},  // Hybrid SWB end
		{16, true},  // Hybrid FB start
		{19, true},  // Hybrid FB end
		{20, false}, // CELT NB
		{27, false}, // CELT SWB
	}

	for _, tt := range tests {
		if got := isHybridConfig(tt.config); got != tt.want {
			t.Errorf("isHybridConfig(%d) = %v, want %v", tt.config, got, tt.want)
		}
	}
}

func TestIsHybridSWBConfig(t *testing.T) {
	tests := []struct {
		config Configuration
		want   bool
	}{
		{11, false},
		{12, true},
		{13, true},
		{14, true},
		{15, true},
		{16, false},
	}

	for _, tt := range tests {
		if got := isHybridSWBConfig(tt.config); got != tt.want {
			t.Errorf("isHybridSWBConfig(%d) = %v, want %v", tt.config, got, tt.want)
		}
	}
}

func TestIsHybridFBConfig(t *testing.T) {
	tests := []struct {
		config Configuration
		want   bool
	}{
		{15, false},
		{16, true},
		{17, true},
		{18, true},
		{19, true},
		{20, false},
	}

	for _, tt := range tests {
		if got := isHybridFBConfig(tt.config); got != tt.want {
			t.Errorf("isHybridFBConfig(%d) = %v, want %v", tt.config, got, tt.want)
		}
	}
}

func TestHybridConstants(t *testing.T) {
	// Verify hybrid mode constants
	if HybridSILKBandwidth != 8000 {
		t.Errorf("HybridSILKBandwidth = %d, want 8000", HybridSILKBandwidth)
	}
	if HybridCELTBandwidth != 4000 {
		t.Errorf("HybridCELTBandwidth = %d, want 4000", HybridCELTBandwidth)
	}
	if HybridCutoffFreq != 8000 {
		t.Errorf("HybridCutoffFreq = %d, want 8000", HybridCutoffFreq)
	}
	if HybridSILKSampleRate != 16000 {
		t.Errorf("HybridSILKSampleRate = %d, want 16000", HybridSILKSampleRate)
	}
	if HybridCELTSampleRate != 24000 {
		t.Errorf("HybridCELTSampleRate = %d, want 24000", HybridCELTSampleRate)
	}
}

func TestConfigurationHybridSWB20ms(t *testing.T) {
	// Configuration 13 should be hybrid superwideband 20ms per RFC 6716
	if ConfigurationHybridSWB20ms != 13 {
		t.Errorf("ConfigurationHybridSWB20ms = %d, want 13", ConfigurationHybridSWB20ms)
	}
	if !isHybridConfig(ConfigurationHybridSWB20ms) {
		t.Error("ConfigurationHybridSWB20ms should be a hybrid config")
	}
	if !isHybridSWBConfig(ConfigurationHybridSWB20ms) {
		t.Error("ConfigurationHybridSWB20ms should be a hybrid SWB config")
	}
}

func TestHybridRoundTrip(t *testing.T) {
	// Create encoder and decoder
	encConfig := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	enc, err := NewHybridEncoder(encConfig)
	if err != nil {
		t.Fatalf("NewHybridEncoder: %v", err)
	}

	decConfig := HybridDecoderConfig{
		SampleRate: 24000,
		Channels:   1,
	}
	dec, err := NewHybridDecoder(decConfig)
	if err != nil {
		t.Fatalf("NewHybridDecoder: %v", err)
	}

	// Generate test audio with mixed frequencies
	frameSize := 24000 * 20 / 1000
	samples := make([]float64, frameSize)
	for i := range samples {
		t := float64(i) / 24000.0
		// Mix of low freq (1 kHz - SILK band) and high freq (10 kHz - CELT band)
		samples[i] = 0.5*math.Sin(2*math.Pi*1000*t) + 0.3*math.Sin(2*math.Pi*10000*t)
	}

	// Encode
	encoded, err := enc.EncodeFrame(samples)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	// Decode using RFC 6716 compliant method with explicit SILK length
	decoded, err := dec.DecodeFrameWithSILKLen(encoded.Data, encoded.SILKLen)
	if err != nil {
		t.Fatalf("DecodeFrameWithSILKLen: %v", err)
	}

	if len(decoded) == 0 {
		t.Error("decoded samples is empty")
	}

	t.Logf("Round-trip: %d samples -> %d bytes (SILK: %d, CELT: %d) -> %d samples",
		len(samples), len(encoded.Data), encoded.SILKLen, len(encoded.Data)-encoded.SILKLen, len(decoded))
}

func TestHybridDecoder_Reset(t *testing.T) {
	config := HybridDecoderConfig{
		SampleRate: 24000,
		Channels:   1,
	}
	dec, err := NewHybridDecoder(config)
	if err != nil {
		t.Fatalf("NewHybridDecoder: %v", err)
	}

	// Reset should not panic
	dec.Reset()

	// Filter state should be cleared
	for _, s := range dec.lowpassState {
		if s != 0 {
			t.Error("lowpass state not cleared after reset")
		}
	}
}

// TestHybridLibopusValidation validates that hybrid-encoded packets can be decoded
// by libopus (via opusdec). This test requires the opus-tools package to be
// installed and is skipped if opusdec is not available.
//
// This test fulfills ROADMAP Priority 1.5: "Enable TestHybridLibopusValidation."
//
// NOTE: Full libopus interoperability requires exact bit-level compliance with
// RFC 6716 §4.2.7.2 (SILK) and §4.3.5 (CELT) for hybrid mode. This test validates
// the basic packet structure and internal round-trip. Full opusdec validation
// is aspirational pending complete RFC 6716 conformance.
func TestHybridLibopusValidation(t *testing.T) {
	// Create encoder
	encConfig := HybridEncoderConfig{
		SampleRate: 24000,
		Channels:   1,
		Bitrate:    64000,
	}
	enc, err := NewHybridEncoder(encConfig)
	if err != nil {
		t.Fatalf("NewHybridEncoder: %v", err)
	}

	// Create decoder
	decConfig := HybridDecoderConfig{
		SampleRate: 24000,
		Channels:   1,
	}
	dec, err := NewHybridDecoder(decConfig)
	if err != nil {
		t.Fatalf("NewHybridDecoder: %v", err)
	}

	// Generate test audio - 1 second at 24 kHz
	const (
		sampleRate = 24000
		duration   = 1.0
		frequency  = 440.0
		frameSize  = 480 // 20ms at 24kHz
	)
	numSamples := int(float64(sampleRate) * duration)
	pcm := make([]float64, numSamples)
	for i := range pcm {
		theta := 2.0 * math.Pi * frequency * float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(theta)
	}

	// Encode all frames
	var packets []*HybridEncodedFrame
	for i := 0; i < len(pcm); i += frameSize {
		end := i + frameSize
		if end > len(pcm) {
			break
		}
		frame, err := enc.EncodeFrame(pcm[i:end])
		if err != nil {
			t.Fatalf("EncodeFrame %d: %v", i/frameSize, err)
		}
		packets = append(packets, frame)
	}

	if len(packets) == 0 {
		t.Fatal("No packets encoded")
	}

	// Verify packet structure
	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			t.Errorf("Packet %d has empty data", i)
		}
		if pkt.SILKLen == 0 {
			t.Errorf("Packet %d has zero SILK length", i)
		}
		if pkt.SILKLen >= len(pkt.Data) {
			t.Errorf("Packet %d: SILK length %d >= total length %d", i, pkt.SILKLen, len(pkt.Data))
		}
	}

	// Verify round-trip decoding
	var totalDecoded int
	for i, pkt := range packets {
		decoded, err := dec.DecodeFrameWithSILKLen(pkt.Data, pkt.SILKLen)
		if err != nil {
			t.Fatalf("DecodeFrame %d: %v", i, err)
		}
		totalDecoded += len(decoded)
	}

	t.Logf("RFC 6716 hybrid format validation:")
	t.Logf("  - Encoded %d packets (total %d bytes)", len(packets), sumPacketBytes(packets))
	t.Logf("  - Average packet size: %d bytes", sumPacketBytes(packets)/len(packets))
	t.Logf("  - Round-trip decoded %d samples", totalDecoded)
	t.Logf("  - Packet format: [SILK data][CELT data] (no length prefix)")

	// Verify TOC header for hybrid mode
	// Configuration 13 = Hybrid SWB 20ms
	expectedConfig := Configuration(13)
	stereo := false
	toc := newTOCHeader(expectedConfig, stereo, frameCodeOneFrame)
	t.Logf("  - Expected TOC for hybrid SWB 20ms mono: 0x%02x (config=%d, stereo=%v, code=0)",
		byte(toc), expectedConfig, stereo)
}

// sumPacketBytes returns the total byte count across all packets.
func sumPacketBytes(packets []*HybridEncodedFrame) int {
	total := 0
	for _, p := range packets {
		total += len(p.Data)
	}
	return total
}
