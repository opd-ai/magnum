# magnum

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

| Symbol | Description |
|---|---|
| `NewEncoder(sampleRate, channels int) (*Encoder, error)` | Create an encoder. Supported rates: 8000, 16000, 24000, 48000 Hz. Channels: 1 or 2. |
| `(*Encoder).Encode(pcm []int16) ([]byte, error)` | Encode one 20 ms frame. Buffers input; returns `nil` until a complete frame is ready. Pass `nil` to drain buffered frames. |
| `(*Encoder).SetBitrate(bitrate int)` | Set target bitrate (bps, clamped to 6000–510000). |
| `Decode(packet []byte) ([]int16, error)` | Decode a packet produced by `Encode`. |

Exported sentinel errors for `errors.Is` branching:

| Error | Returned by |
|---|---|
| `ErrUnsupportedSampleRate` | `NewEncoder` with an unsupported sample rate |
| `ErrUnsupportedChannelCount` | `NewEncoder` with channels ≠ 1 or 2 |
| `ErrTooShortForTableOfContentsHeader` | `Decode` with an empty packet |
| `ErrInvalidFrameData` | `Decode` with an odd-length decompressed payload |
| `ErrPayloadTooLarge` | `Decode` when decompressed data exceeds 64 KiB |

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
