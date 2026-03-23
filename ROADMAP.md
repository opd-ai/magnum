# magnum — Roadmap to RFC 6716 Compliance and Standard Opus Interoperability

This document tracks the work required to evolve `magnum` from its current
simplified baseline into a fully RFC 6716–compliant Opus encoder/decoder that
produces packets interoperable with [libopus](https://opus-codec.org/) and
[pion/opus](https://github.com/pion/opus).

---

## Current state (baseline)

| Capability | Status |
|---|---|
| RFC 6716 §3.1 TOC header generation | ✅ implemented |
| TOC configuration matched to sample rate | ✅ implemented |
| Interleaved PCM frame buffering (mono/stereo) | ✅ implemented |
| `Encode` / `Decode` API (pion/opus-compatible shape) | ✅ implemented |
| `Decoder` type (magnum packets only) | ✅ implemented |
| `SetBitrate` | ✅ stored; not yet used |
| SILK codec (narrowband / wideband) | ❌ not implemented |
| CELT codec (superwideband / fullband) | ❌ not implemented |
| Hybrid mode (SILK + CELT) | ❌ not implemented |
| RFC 6716–compliant range coder | ✅ implemented |
| Variable frame durations (2.5 – 60 ms) | ❌ not implemented |
| Multi-frame packets (codes 1 / 2 / 3) | ❌ not implemented |
| Decoder type for standard Opus packets | ❌ not implemented |
| Packet loss concealment (PLC) | ❌ not implemented |
| In-band forward error correction (FEC) | ❌ not implemented |
| Interoperability with libopus / pion/opus | ❌ blocked by above |

---

## Milestone 1 — Range coder and entropy infrastructure

**Goal:** implement the range coder defined in RFC 6716 §4.1 as the
foundational bit-level I/O layer used by both SILK and CELT.

### Tasks

- [x] Implement `RangeEncoder` (`ec_enc`): encode symbols against a
  probability model using the RFC 6716 range coding algorithm.
- [x] Implement `RangeDecoder` (`ec_dec`): the matching decoder.
- [x] Implement raw-bits helpers (`ec_enc_bits`, `ec_dec_bits`) for
  fixed-length fields that bypass the probability model.
- [x] Unit-test encoder/decoder round-trips for a range of symbol
  distributions and input lengths.
- [ ] Verify bit-exact output against the reference C implementation
  (`opus/celt/entenc.c`, `entdec.c`) for a shared set of test vectors.

### Success criteria
`RangeDecoder(RangeEncoder(symbols)) == symbols` for all test vectors;
output bytes match the reference implementation byte-for-byte.

---

## Milestone 2 — CELT encoder (fullband 48 kHz path)

**Goal:** produce RFC 6716–compliant CELT-only packets for 48 kHz input
that libopus can decode.

CELT is the mandatory path for fullband (48 kHz) and superwideband (24 kHz)
audio. It is transform-based (MDCT → band energy → PVQ spectral coding).

### Tasks

#### 2a — MDCT
- [ ] Implement the windowed MDCT (Modified Discrete Cosine Transform) over
  the frame sizes used by Opus: 120, 240, 480, 960, 1920 samples
  (corresponding to 2.5, 5, 10, 20, 40 ms at 48 kHz).
- [ ] Implement the inverse MDCT for the decoder path.
- [ ] Validate against the reference `celt/mdct.c` test vectors.

#### 2b — Band energy encoding
- [ ] Implement log-domain band energy computation across the 21 CELT
  frequency bands defined in RFC 6716 §4.3.2.
- [ ] Implement the coarse and fine energy quantizers (`quant_coarse_energy`,
  `quant_fine_energy`) and their decoders.

#### 2c — PVQ spectral coding
- [ ] Implement Pyramid Vector Quantization (`alg_quant`) for spectral
  coefficient vectors.
- [ ] Implement the matching `alg_unquant` decoder.
- [ ] Implement the `spreading` and `tf_change` parameters.

#### 2d — CELT frame assembly
- [ ] Wire range coder, energy coding, and PVQ output into a single
  CELT frame bitstream following RFC 6716 §4.3.
- [ ] Implement the `PostFilter` (pitch post-filter) encoder and decoder.
- [ ] Implement `transient` detection and the transient subdivision logic.

#### 2e — Bitrate control
- [ ] Connect `SetBitrate` to the CELT allocation table so that bits are
  distributed across bands proportionally to the configured bitrate.

#### 2f — Integration
- [ ] Replace the current `flate` payload in `encodeFrame` with the CELT
  bitstream for 24 kHz and 48 kHz sample rates.
- [ ] Validate encoded packets with `opusdec` / `opus_demo` from libopus.

### Success criteria
Packets encoded by `magnum` for 48 kHz input decode without error in
`libopus >= 1.3` and `pion/opus`; PESQ / ViSQOL scores are within 0.5 MOS
of the libopus reference encoder at the same bitrate.

---

## Milestone 3 — SILK encoder (narrowband 8 kHz and wideband 16 kHz paths)

**Goal:** produce RFC 6716–compliant SILK-only packets for 8 kHz and 16 kHz
input that libopus can decode.

SILK is a speech-optimised linear predictive codec. It is used exclusively
for narrowband and wideband modes.

### Tasks

#### 3a — LPC analysis
- [ ] Implement autocorrelation-based LPC (Linear Predictive Coding)
  coefficient estimation (Levinson-Durbin / Burg algorithm).
- [ ] Implement NLSF (Normalised Line Spectral Frequency) conversion and
  the NLSF stabilisation procedure from the SILK spec (Appendix II).
- [ ] Implement NLSF interpolation between frames.

#### 3b — Pitch (long-term prediction)
- [ ] Implement open-loop pitch estimation for voiced speech detection.
- [ ] Implement closed-loop LTP (Long-Term Prediction) analysis and
  quantization.

#### 3c — LPC residual coding
- [ ] Implement subframe gain coding.
- [ ] Implement LPC excitation coding using the shape codebook.
- [ ] Implement the PLC (Packet Loss Concealment) state machine for
  the decoder path.

#### 3d — VAD and DTX
- [ ] Implement Voice Activity Detection (VAD) to detect silence frames.
- [ ] Implement Discontinuous Transmission (DTX) to suppress redundant
  silence packets.

#### 3e — In-band FEC
- [ ] Implement redundant LBRR (Low-Bit-Rate Redundancy) frames for
  in-band forward error correction (RFC 6716 §4.2.4).

#### 3f — Integration
- [ ] Replace the `flate` payload in `encodeFrame` with the SILK
  bitstream for 8 kHz and 16 kHz sample rates.
- [ ] Validate encoded packets with `opusdec` / `opus_demo`.

### Success criteria
Packets for 8 kHz and 16 kHz input decode in `libopus >= 1.3` and
`pion/opus`; MOS scores within 0.5 of the libopus reference.

---

## Milestone 4 — Hybrid mode (superwideband, SILK + CELT)

**Goal:** support 24 kHz input using the hybrid SILK+CELT mode
(Opus configurations 24–27, RFC 6716 §3.1).

### Tasks
- [ ] Split the 24 kHz input into a SILK band (0–8 kHz) and a CELT
  band (8–12 kHz) using the hybrid framing defined in RFC 6716 §3.1.
- [ ] Encode each band with the appropriate codec (SILK for lows,
  CELT for highs) and multiplex into a single packet.
- [ ] Implement the matching hybrid decoder.
- [ ] Validate with `opusdec`.

### Success criteria
24 kHz hybrid packets decode in `libopus >= 1.3`.

---

## Milestone 5 — Variable frame durations and multi-frame packets

**Goal:** lift the current "20 ms, single-frame only" restriction.

### Tasks
- [ ] Add a `FrameDuration` option to `NewEncoder` supporting 2.5, 5, 10,
  20, 40, and 60 ms (RFC 6716 §2.1.3).
- [ ] Implement frame-code 1 (two equal-size frames) and frame-code 2
  (two different-size frames) in the packet serialiser.
- [ ] Implement frame-code 3 (CBR/VBR multi-frame packets, RFC 6716 §3.2.5).
- [ ] Update `Decode` to demultiplex all four frame codes.

### Success criteria
Multi-frame packets produced by `magnum` round-trip through `Decode` and
also decode in `opusdec`; frame duration is configurable at the API level.

---

## Milestone 6 — Standard Decoder type

**Goal:** expose a `Decoder` type that can decode packets produced by
libopus, pion/opus, or any other compliant encoder — not only `magnum`.

### Tasks
- [ ] Add `type Decoder struct` with `NewDecoder(sampleRate, channels int)`
  mirroring the pion/opus API (`Decoder.Decode(in []byte, out []int16)`).
- [ ] Implement CELT decode path for configurations 24–31.
- [ ] Implement SILK decode path for configurations 0–15.
- [ ] Implement hybrid decode path for configurations 16–23.
- [ ] Implement PLC (zero-input synthesis on lost packets).
- [ ] Fuzz the decoder against random/malformed packets.

### Success criteria
`Decoder` successfully decodes `opus_demo`-generated test files for all
sample rates and channel counts; passes the `testvectors` suite included
with the libopus source tree.

---

## Milestone 7 — Conformance testing and interoperability validation

**Goal:** demonstrate bit-exact or perceptually equivalent output relative
to the libopus reference implementation.

### Tasks
- [ ] Integrate the official Opus test vectors
  (`https://opus-codec.org/testvectors/`) into the CI pipeline.
- [ ] Add a `TestConformance` suite that decodes each vector with
  `magnum.Decoder` and compares output against the reference PCM.
- [ ] Add cross-encode tests: encode with `magnum`, decode with `pion/opus`
  (and vice-versa); assert no decoder errors and acceptable signal quality.
- [ ] Integrate `opus_compare` (or a pure-Go equivalent) for MOS-LQO
  scoring in CI.
- [ ] Profile and optimise hot paths (MDCT, PVQ) to reach within 3× of
  libopus throughput on a representative benchmark corpus.

### Success criteria
All official Opus test vectors pass; cross-interop round-trips with
pion/opus produce no errors; CI green on `linux/amd64`, `linux/arm64`,
and `darwin/amd64`.

---

## Dependency and architecture notes

All milestones must be achievable with **zero CGO** and **no external
dependencies** beyond the Go standard library. The architecture should
remain layered so each codec (SILK, CELT) can be replaced or improved
independently without breaking the public `Encoder`/`Decoder` API.

Key reference material:
- **RFC 6716** — Definition of the Opus Audio Codec
  (<https://www.rfc-editor.org/rfc/rfc6716>)
- **libopus source** — `https://gitlab.xiph.org/xiph/opus` (C reference;
  useful for test vectors and algorithmic reference, not for copying code)
- **pion/opus** — `https://github.com/pion/opus` (Go; API compatibility
  target)
- **Opus codec website** — `https://opus-codec.org/` (test vectors, tools)
