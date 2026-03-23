// Package magnum provides a simplified pure-Go Opus-compatible audio encoder.
//
// This file implements Low Bit-Rate Redundancy (LBRR) for in-band Forward
// Error Correction (FEC) as specified in RFC 6716 §4.2.4.
//
// LBRR provides redundancy against packet loss by encoding a low bit-rate
// version of the previous frame and including it in the current packet.
// When a packet is lost, the decoder can use the LBRR data from the next
// received packet to reconstruct the lost frame with better quality than
// plain packet loss concealment.

package magnum

// LBRRMode represents the LBRR operation mode.
type LBRRMode int

const (
	// LBRRModeOff disables LBRR encoding.
	LBRRModeOff LBRRMode = iota
	// LBRRModeLow encodes LBRR with minimal overhead.
	LBRRModeLow
	// LBRRModeMedium encodes LBRR with moderate quality.
	LBRRModeMedium
	// LBRRModeHigh encodes LBRR with best quality (highest overhead).
	LBRRModeHigh
)

// LBRRConfig holds configuration for LBRR encoding.
type LBRRConfig struct {
	// Mode controls the LBRR encoding quality/overhead tradeoff.
	Mode LBRRMode
	// PacketLossPercentage is the expected packet loss rate.
	// Higher values trigger more aggressive LBRR encoding.
	PacketLossPercentage int
	// ThresholdPLC is the minimum packet loss percentage to enable LBRR.
	ThresholdPLC int
}

// DefaultLBRRConfig returns the default LBRR configuration.
func DefaultLBRRConfig() LBRRConfig {
	return LBRRConfig{
		Mode:                 LBRRModeOff,
		PacketLossPercentage: 0,
		ThresholdPLC:         3, // Enable LBRR when packet loss > 3%
	}
}

// LBRRFrame represents a low bit-rate redundancy frame.
type LBRRFrame struct {
	// Valid indicates whether this LBRR frame contains data.
	Valid bool
	// Index is the frame index this LBRR corresponds to (relative to primary).
	// Typically -1 (previous frame) or -2 (two frames ago).
	Index int
	// Data is the compressed LBRR payload.
	Data []byte
	// Flags contains signaling bits for the LBRR frame.
	Flags LBRRFlags
}

// LBRRFlags contains signaling information for LBRR frames.
type LBRRFlags struct {
	// VADFlag indicates voice activity in the LBRR frame.
	VADFlag bool
	// GainsOnly indicates the frame contains only gain information.
	GainsOnly bool
}

// LBRREncoder encodes low bit-rate redundancy frames for FEC.
type LBRREncoder struct {
	config LBRRConfig

	// History of recent primary frames for LBRR encoding.
	// We keep the last 2 frames to support different LBRR depths.
	frameHistory [2]*LBRRFrameData
	historyIndex int

	// LBRR bitrate targets based on mode.
	targetBitrate int

	// sampleRate for the encoder.
	sampleRate int
	// channels for the encoder.
	channels int
}

// LBRRFrameData stores data needed to create an LBRR frame from a primary frame.
type LBRRFrameData struct {
	// LPCCoeffs are the quantized LPC coefficients.
	LPCCoeffs []float64
	// Gains are the subframe gains.
	Gains []float64
	// PitchLags are the pitch lags per subframe.
	PitchLags []int
	// VADFlag indicates voice activity.
	VADFlag bool
	// Energy is the frame energy.
	Energy float64
	// Samples is a copy of the original PCM samples for re-encoding.
	Samples []float64
}

// NewLBRREncoder creates a new LBRR encoder.
func NewLBRREncoder(sampleRate, channels int, config LBRRConfig) *LBRREncoder {
	enc := &LBRREncoder{
		config:       config,
		frameHistory: [2]*LBRRFrameData{nil, nil},
		historyIndex: 0,
		sampleRate:   sampleRate,
		channels:     channels,
	}
	enc.updateTargetBitrate()
	return enc
}

// updateTargetBitrate sets the target bitrate based on LBRR mode.
func (e *LBRREncoder) updateTargetBitrate() {
	switch e.config.Mode {
	case LBRRModeOff:
		e.targetBitrate = 0
	case LBRRModeLow:
		e.targetBitrate = 6000 // Minimum intelligible bitrate
	case LBRRModeMedium:
		e.targetBitrate = 8000
	case LBRRModeHigh:
		e.targetBitrate = 12000
	default:
		e.targetBitrate = 0
	}
}

// StorePrimaryFrame stores data from the primary frame for future LBRR encoding.
// This should be called after encoding each primary frame.
func (e *LBRREncoder) StorePrimaryFrame(data *LBRRFrameData) {
	e.frameHistory[e.historyIndex] = data
	e.historyIndex = (e.historyIndex + 1) % 2
}

// EncodeLBRR creates an LBRR frame from stored primary frame data.
// Returns nil if LBRR is disabled or no suitable frame is available.
func (e *LBRREncoder) EncodeLBRR() *LBRRFrame {
	if e.config.Mode == LBRRModeOff {
		return nil
	}

	// Get the previous frame data
	prevIndex := (e.historyIndex + 1) % 2
	prevData := e.frameHistory[prevIndex]
	if prevData == nil {
		return nil
	}

	// Create LBRR frame with reduced precision
	lbrr := &LBRRFrame{
		Valid: true,
		Index: -1, // Previous frame
		Flags: LBRRFlags{
			VADFlag:   prevData.VADFlag,
			GainsOnly: e.config.Mode == LBRRModeLow && !prevData.VADFlag,
		},
	}

	// Encode LBRR payload
	// For now, we store a simplified representation that can be used
	// with the PLC system to improve reconstruction.
	lbrr.Data = e.encodePayload(prevData)

	return lbrr
}

// encodePayload creates the LBRR bitstream from frame data.
func (e *LBRREncoder) encodePayload(data *LBRRFrameData) []byte {
	// Use range encoder for LBRR payload
	enc := NewRangeEncoder()

	// Encode VAD flag (1 bit)
	vadVal := 0
	if data.VADFlag {
		vadVal = 1
	}
	enc.EncodeLogP(vadVal, 1)

	// Encode quantized gains (simplified)
	for _, gain := range data.Gains {
		// Quantize gain to 6 bits (0-63)
		quantGain := int(gain * 63.0)
		if quantGain < 0 {
			quantGain = 0
		}
		if quantGain > 63 {
			quantGain = 63
		}
		enc.EncodeBits(uint32(quantGain), 6)
	}

	// For voiced frames, encode pitch information
	if data.VADFlag && len(data.PitchLags) > 0 {
		// Encode pitch lag for first subframe (9 bits, 0-511)
		pitchLag := data.PitchLags[0]
		if pitchLag < 0 {
			pitchLag = 0
		}
		if pitchLag > 511 {
			pitchLag = 511
		}
		enc.EncodeBits(uint32(pitchLag), 9)
	}

	// For higher quality modes, encode simplified LPC
	if e.config.Mode >= LBRRModeMedium && len(data.LPCCoeffs) > 0 {
		// Encode first 4 LPC coefficients at reduced precision
		numCoeffs := 4
		if numCoeffs > len(data.LPCCoeffs) {
			numCoeffs = len(data.LPCCoeffs)
		}
		for i := 0; i < numCoeffs; i++ {
			// Quantize to 8 bits
			coeff := data.LPCCoeffs[i]
			quantCoeff := int((coeff + 1.0) * 127.5) // Map [-1, 1] to [0, 255]
			if quantCoeff < 0 {
				quantCoeff = 0
			}
			if quantCoeff > 255 {
				quantCoeff = 255
			}
			enc.EncodeBits(uint32(quantCoeff), 8)
		}
	}

	return enc.Bytes()
}

// DecodeLBRR decodes an LBRR frame for use in packet loss concealment.
func DecodeLBRR(data []byte) (*LBRRFrameData, error) {
	if len(data) == 0 {
		return nil, nil
	}

	dec := NewRangeDecoder(data)

	// Decode VAD flag
	vadFlag := dec.DecodeLogP(1) != 0

	// Decode gains (4 subframes)
	gains := make([]float64, SILKSubFrames)
	for i := range gains {
		quantGain := dec.DecodeBits(6)
		gains[i] = float64(quantGain) / 63.0
	}

	frameData := &LBRRFrameData{
		VADFlag: vadFlag,
		Gains:   gains,
	}

	// For voiced frames, decode pitch
	if vadFlag {
		pitchLag := int(dec.DecodeBits(9))
		frameData.PitchLags = []int{pitchLag, pitchLag, pitchLag, pitchLag}
	}

	// Decode LPC coefficients if present
	if dec.Remaining() >= 4 {
		numCoeffs := dec.Remaining() / 1
		if numCoeffs > 4 {
			numCoeffs = 4
		}
		frameData.LPCCoeffs = make([]float64, numCoeffs)
		for i := 0; i < numCoeffs; i++ {
			quantCoeff := dec.DecodeBits(8)
			frameData.LPCCoeffs[i] = float64(quantCoeff)/127.5 - 1.0
		}
	}

	return frameData, nil
}

// SetMode sets the LBRR operating mode.
func (e *LBRREncoder) SetMode(mode LBRRMode) {
	e.config.Mode = mode
	e.updateTargetBitrate()
}

// Mode returns the current LBRR mode.
func (e *LBRREncoder) Mode() LBRRMode {
	return e.config.Mode
}

// SetPacketLossPercentage updates the expected packet loss rate.
// This may automatically enable LBRR if loss exceeds the threshold.
func (e *LBRREncoder) SetPacketLossPercentage(percentage int) {
	e.config.PacketLossPercentage = percentage

	// Auto-enable LBRR based on packet loss
	if percentage > e.config.ThresholdPLC {
		if e.config.Mode == LBRRModeOff {
			// Auto-select mode based on loss rate
			if percentage > 15 {
				e.config.Mode = LBRRModeHigh
			} else if percentage > 8 {
				e.config.Mode = LBRRModeMedium
			} else {
				e.config.Mode = LBRRModeLow
			}
			e.updateTargetBitrate()
		}
	}
}

// IsEnabled returns true if LBRR encoding is active.
func (e *LBRREncoder) IsEnabled() bool {
	return e.config.Mode != LBRRModeOff
}

// TargetBitrate returns the current LBRR target bitrate.
func (e *LBRREncoder) TargetBitrate() int {
	return e.targetBitrate
}

// Reset clears the LBRR encoder state.
func (e *LBRREncoder) Reset() {
	e.frameHistory = [2]*LBRRFrameData{nil, nil}
	e.historyIndex = 0
}

// Config returns the current LBRR configuration.
func (e *LBRREncoder) Config() LBRRConfig {
	return e.config
}

// SetConfig updates the LBRR configuration.
func (e *LBRREncoder) SetConfig(config LBRRConfig) {
	e.config = config
	e.updateTargetBitrate()
}
