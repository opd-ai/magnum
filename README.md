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

```go
import "github.com/opd-ai/magnum"

// Create a 48 kHz mono encoder.
enc, err := magnum.NewEncoder(48000, 1)
if err != nil {
    log.Fatal(err)
}

// (Optional) set a target bitrate.
enc.SetBitrate(64000)

// Feed 20 ms of PCM samples (960 samples at 48 kHz).
// Encode returns nil until a full frame has been buffered.
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

## API

| Symbol | Description |
|---|---|
| `NewEncoder(sampleRate, channels int) (*Encoder, error)` | Create an encoder. Supported rates: 8000, 16000, 24000, 48000 Hz. Channels: 1 or 2. |
| `(*Encoder).Encode(pcm []int16) ([]byte, error)` | Encode one 20 ms frame. Returns `nil` until the buffer holds a full frame. |
| `(*Encoder).SetBitrate(bitrate int)` | Set target bitrate (bps, clamped to 6000–510000). |
| `Decode(packet []byte) ([]int16, error)` | Decode a packet produced by `Encode`. |

## Packet format

```
byte 0      : Opus TOC header (config | stereo flag | frame code)
bytes 1…    : flate-compressed little-endian int16 PCM samples
```

The TOC header follows RFC 6716 §3.1. This encoder always uses
`ConfigurationCELTFB20ms` (configuration 29) and `frameCodeOneFrame`.

## Limitations

* **Not RFC 6716 compliant** — payload uses `compress/flate`, not SILK/CELT.
* **Single-frame packets only** — one 20 ms frame per call to `Encode`.
* **No PLC / FEC** — no packet-loss concealment or forward error correction.
* **Bitrate hint only** — `SetBitrate` is stored but not currently used.
* **No resampling** — input must already be at the chosen sample rate.

## Roadmap

Future work could replace the flate payload with a real SILK or CELT encoder to
produce RFC 6716–compliant packets decodable by pion/opus and libopus.

## License

MIT — see [LICENSE](LICENSE).
