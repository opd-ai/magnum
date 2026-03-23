# magnum

[![CI](https://github.com/opd-ai/magnum/actions/workflows/ci.yml/badge.svg)](https://github.com/opd-ai/magnum/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/opd-ai/magnum/branch/main/graph/badge.svg)](https://codecov.io/gh/opd-ai/magnum)

Opus encoder in pure Go — a minimal, pure-Go Opus-compatible audio encoder
following [pion/opus](https://github.com/pion/opus) API patterns.

## Status

This is a **simplified reference implementation**. The encoder wraps 20 ms PCM
frames in valid Opus TOC-header packets and compresses the payload with Go's
standard `compress/flate`. It does **not** implement the SILK or CELT codecs
defined in RFC 6716, so packets are not interoperable with standard Opus
decoders. Use the bundled `Decode` function for encode/decode round-trips.

## Installation

```sh
go get github.com/opd-ai/magnum
```

## Usage

### Mono (1 channel)

```go
import "github.com/opd-ai/magnum"

// Create a 48 kHz mono encoder.
enc, err := magnum.NewEncoder(48000, 1)
if err != nil {
    log.Fatal(err)
}

// (Optional) set a target bitrate.
enc.SetBitrate(64000)

// Feed 20 ms of PCM samples.
// For mono 48 kHz: 960 samples = sampleRate / 50.
// Encode returns nil until a full frame has been buffered.
// Pass nil to drain any buffered frames without supplying new samples.
pcm := make([]int16, 960)
// … fill pcm with audio data …

packet, err := enc.Encode(pcm)
if err != nil {
    log.Fatal(err)
}

// Round-trip: decode the packet back to PCM.
decoded, err := magnum.Decode(packet)
if err != nil {
    log.Fatal(err)
}
_ = decoded
```

### Stereo (2 channels)

For stereo, PCM samples are **interleaved** (L₀, R₀, L₁, R₁, …). A 20 ms
stereo frame at 48 kHz therefore requires **1920** int16 samples
(960 samples/channel × 2 channels):

```go
enc, err := magnum.NewEncoder(48000, 2)
if err != nil {
    log.Fatal(err)
}

// 1920 interleaved int16 samples = 20 ms at 48 kHz stereo.
pcm := make([]int16, 1920)
// … fill pcm with interleaved L/R samples …

packet, err := enc.Encode(pcm)
```

Frame sizes for other sample rates:

| Sample rate | Channels | Samples per 20 ms frame |
|-------------|----------|-------------------------|
| 8 000 Hz    | 1 (mono) | 160                     |
| 8 000 Hz    | 2 (stereo)| 320                    |
| 16 000 Hz   | 1 (mono) | 320                     |
| 16 000 Hz   | 2 (stereo)| 640                    |
| 24 000 Hz   | 1 (mono) | 480                     |
| 24 000 Hz   | 2 (stereo)| 960                    |
| 48 000 Hz   | 1 (mono) | 960                     |
| 48 000 Hz   | 2 (stereo)| 1920                   |

## API

### Encoder

| Symbol | Description |
|---|---|
| `Application` | Type representing encoder application mode (VoIP, Audio, LowDelay). |
| `ApplicationVoIP` | Optimizes for voice over IP (low latency, speech). Value: 2048. |
| `ApplicationAudio` | Optimizes for general audio (best quality). Value: 2049. Default. |
| `ApplicationLowDelay` | Lowest possible latency. Value: 2051. |
| `Bandwidth` | Type representing encoder bandwidth setting. |
| `BandwidthNarrowband` | Limits audio to 4 kHz bandwidth. Value: 1101. |
| `BandwidthMediumband` | Limits audio to 6 kHz bandwidth. Value: 1102. |
| `BandwidthWideband` | Limits audio to 8 kHz bandwidth. Value: 1103. |
| `BandwidthSuperwideband` | Limits audio to 12 kHz bandwidth. Value: 1104. |
| `BandwidthFullband` | Allows full 20 kHz bandwidth. Value: 1105. |
| `BandwidthAuto` | Automatic bandwidth selection. Value: -1000. Default. |
| `NewEncoder(sampleRate, channels int) (*Encoder, error)` | Create an encoder with default `ApplicationAudio`. Supported rates: 8000, 16000, 24000, 48000 Hz. Channels: 1 or 2. |
| `NewEncoderWithApplication(sampleRate, channels int, app Application) (*Encoder, error)` | Create an encoder with explicit application mode. Follows pion/opus API pattern. |
| `(*Encoder).Encode(pcm []int16) ([]byte, error)` | Encode one 20 ms frame. Buffers input; returns `nil` until a complete frame is ready. Pass `nil` to drain buffered frames. |
| `(*Encoder).Flush() ([]byte, error)` | Flush any remaining buffered samples as a zero-padded final frame. |
| `(*Encoder).SetBitrate(bitrate int)` | Set target bitrate (bps, clamped to 6000–510000). Stored for future CELT/SILK integration; currently unused. |
| `(*Encoder).SetComplexity(complexity int)` | Set complexity level (0-10). Stored for future CELT/SILK integration; currently unused. |
| `(*Encoder).Complexity() int` | Returns the complexity level configured for this encoder. |
| `(*Encoder).SetBandwidth(bandwidth Bandwidth)` | Set maximum audio bandwidth. Stored for future CELT/SILK integration; currently unused. |
| `(*Encoder).Bandwidth() Bandwidth` | Returns the bandwidth setting configured for this encoder. |
| `(*Encoder).Application() Application` | Returns the application mode configured for this encoder. |

### Decoder

| Symbol | Description |
|---|---|
| `NewDecoder(sampleRate, channels int) (*Decoder, error)` | Create a decoder. Same rate/channel constraints as `NewEncoder`. |
| `(*Decoder).Decode(packet, out []int16) (int, error)` | Decode into pre-allocated buffer. Returns sample count. |
| `(*Decoder).DecodeAlloc(packet []byte) ([]int16, error)` | Decode and allocate output slice. |
| `(*Decoder).SampleRate() int` | Returns configured sample rate. |
| `(*Decoder).Channels() int` | Returns configured channel count. |
| `Decode(packet []byte) ([]int16, error)` | Standalone decode function for simple use cases. |
| `DecodeWithInfo(packet []byte) ([]int16, bool, error)` | Decode returning samples and stereo flag. |

### Range Coder (RFC 6716 §4.1)

| Symbol | Description |
|---|---|
| `NewRangeEncoder() *RangeEncoder` | Create an arithmetic range encoder. |
| `(*RangeEncoder).Encode(fl, fh, ft uint32)` | Encode a symbol with frequency range [fl, fh) out of ft. |
| `(*RangeEncoder).EncodeBits(value, bits uint32)` | Encode raw bits (1–25 bits) directly. |
| `(*RangeEncoder).EncodeLogP(val int, logp uint)` | Encode binary symbol with probability 1/2^logp. |
| `(*RangeEncoder).Bytes() []byte` | Finalize and return encoded byte stream. |
| `(*RangeEncoder).Reset()` | Reset encoder for reuse. |
| `NewRangeDecoder(data []byte) *RangeDecoder` | Create a range decoder from encoded data. |
| `(*RangeDecoder).Decode(ft uint32) uint32` | Decode symbol frequency (call Update after). |
| `(*RangeDecoder).Update(fl, fh, ft uint32)` | Update state after determining symbol range. |
| `(*RangeDecoder).DecodeBits(bits uint32) uint32` | Decode raw bits directly. |
| `(*RangeDecoder).DecodeLogP(logp uint) int` | Decode binary symbol. |
| `(*RangeDecoder).Remaining() int` | Return unread bytes count. |

### Configuration Constants

| Symbol | Description |
|---|---|
| `Configuration` | Type representing Opus TOC configuration (RFC 6716 §3.1). |
| `ConfigurationSILKNB20ms` | SILK narrowband 8 kHz, 20 ms. |
| `ConfigurationSILKWB20ms` | SILK wideband 16 kHz, 20 ms. |
| `ConfigurationCELTSWB20ms` | CELT superwideband 24 kHz, 20 ms. |
| `ConfigurationCELTFB20ms` | CELT fullband 48 kHz, 20 ms. |
| `SampleRate8k`, `SampleRate16k`, `SampleRate24k`, `SampleRate48k` | Supported sample rate constants. |

### Sentinel Errors

Exported sentinel errors for `errors.Is` branching:

| Error | Returned by |
|---|---|
| `ErrUnsupportedSampleRate` | `NewEncoder`/`NewDecoder` with unsupported sample rate |
| `ErrUnsupportedChannelCount` | `NewEncoder`/`NewDecoder` with channels ≠ 1 or 2 |
| `ErrTooShortForTableOfContentsHeader` | `Decode` with empty packet |
| `ErrInvalidFrameData` | `Decode` with odd-length decompressed payload |
| `ErrPayloadTooLarge` | `Decode` when decompressed data exceeds 64 KiB |
| `ErrUnsupportedFrameCode` | `Decode` with multi-frame packet (codes 1, 2, 3) |
| `ErrChannelMismatch` | `Decoder.Decode` when packet stereo flag ≠ decoder channels |
| `ErrSampleRateMismatch` | `Decoder.Decode` when packet config ≠ decoder sample rate |

## Packet format

```
byte 0      : Opus TOC header (config | stereo flag | frame code)
bytes 1…    : flate-compressed little-endian int16 PCM samples
```

The TOC header follows RFC 6716 §3.1. The configuration field reflects the
actual bandwidth of the configured sample rate (e.g., CELT fullband for 48 kHz,
SILK wideband for 16 kHz). This encoder always produces single-frame packets
(`frameCodeOneFrame`).

## Limitations

* **Not RFC 6716 compliant** — payload uses `compress/flate`, not SILK/CELT.
* **Single-frame packets only** — one 20 ms frame per call to `Encode`.
* **No PLC / FEC** — no packet-loss concealment or forward error correction.
* **Bitrate hint only** — `SetBitrate` is stored but not currently used.
* **No resampling** — input must already be at the chosen sample rate.

## Roadmap

See [ROADMAP.md](ROADMAP.md) for the full milestone plan covering the path
from this baseline to RFC 6716–compliant packets interoperable with libopus
and pion/opus (CELT, SILK, hybrid mode, variable frame durations, conformance
test vectors, and a standard `Decoder` type).

## License

MIT — see [LICENSE](LICENSE).
