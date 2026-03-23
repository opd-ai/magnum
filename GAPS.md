# Implementation Gaps — 2026-03-22

This document identifies gaps between the project's stated goals and the current implementation. Gaps are ordered by impact on users.

---

## ~~Decoder.Decode Discards Samples When Output Buffer Is Nil~~ ✅ RESOLVED

- **Resolution**: Added `DecodeAlloc()` method that returns allocated samples when called without pre-allocated buffer.
- **Validation**: `TestDecoderDecodeNilOutput` passes.

---

## SetBitrate Is a No-Op Placeholder

- **Stated Goal**: README documents `SetBitrate(bitrate int)` — "Set target bitrate (bps, clamped to 6000–510000)."
- **Current State**: `encoder.go:71-84` stores the bitrate with proper clamping, but the flate compressor is initialized with `flate.DefaultCompression` at `encoder.go:48`. The stored bitrate is never read.
- **Impact**: Users calling `SetBitrate()` expecting smaller/larger packets see no change in output size. Documented in "Limitations" as "Bitrate hint only" but API suggests active control.
- **Closing the Gap**: 
  1. Short-term: Add prominent doc comment on `SetBitrate` stating it's a no-op placeholder for future CELT/SILK integration.
  2. Long-term (ROADMAP Milestone 2e): Wire `e.bitrate` to CELT bit allocation tables once implemented.
  3. Validate: Encode same frame with different bitrates, verify packet sizes are identical (confirming documented limitation).

---

## ~~Decoder Does Not Validate Packet Metadata Against Configuration~~ ✅ RESOLVED

- **Resolution**: Added `ErrChannelMismatch` and `ErrSampleRateMismatch` sentinel errors. `Decode()` now validates TOC stereo flag against `d.channels` and TOC configuration against `d.sampleRate`.
- **Validation**: `TestDecoderChannelMismatch` and `TestDecoderSampleRateMismatch` pass.

---

## No Application Type Parameter (pion/opus Divergence)

- **Stated Goal**: README states the encoder follows "pion/opus API patterns."
- **Current State**: pion/opus `NewEncoder` accepts an application type parameter (VOIP, AUDIO, RESTRICTED_LOWDELAY). magnum's `NewEncoder(sampleRate, channels)` omits this.
- **Impact**: API is not fully pion/opus compatible. Users migrating from pion/opus must adapt their code. Future CELT/SILK integration will need this parameter.
- **Closing the Gap**:
  1. Add optional `Application` type and `NewEncoderWithApplication(sampleRate, channels int, app Application)` constructor.
  2. Or add `SetApplication(app Application)` method to `Encoder`.
  3. `NewEncoder` remains the simple default, setting application to AUDIO.
  4. Validate: `go test -run TestNewEncoderWithApplication ./...`

---

## Missing Complexity Control (pion/opus Divergence)

- **Stated Goal**: pion/opus-compatible API for audio encoding.
- **Current State**: pion/opus offers `SetComplexity(complexity int)` to trade CPU for quality. magnum has no equivalent.
- **Impact**: Users cannot tune encoding performance vs. quality tradeoff. Less critical since flate compression doesn't have the same CPU/quality curve as CELT.
- **Closing the Gap**:
  1. Add `SetComplexity(complexity int)` as a no-op placeholder with doc comment explaining it's reserved for future CELT integration.
  2. When CELT is implemented (Milestone 2), wire to CELT complexity setting.
  3. Validate: `go build ./...`

---

## No Bandwidth Control (pion/opus Divergence)

- **Stated Goal**: pion/opus-compatible API patterns.
- **Current State**: pion/opus offers bandwidth control (auto, narrowband, mediumband, wideband, superwideband, fullband). magnum derives bandwidth implicitly from sample rate.
- **Impact**: Cannot force lower bandwidth for constrained channels. Less critical given the simplified compression model.
- **Closing the Gap**:
  1. Add `SetBandwidth(bandwidth Bandwidth)` as no-op placeholder.
  2. Document that bandwidth is currently derived from sample rate.
  3. Validate: `go build ./...`

---

## ~~High Memory Allocation in Encode Path~~ ✅ RESOLVED

- **Resolution**: Achieved 99.6% reduction (821KB → 3.6KB B/op) by reusing flate.Writer with `Reset()` and pre-allocating buffers.
- **Validation**: `go test -bench=BenchmarkEncode -benchmem ./...` shows ~3.6KB B/op.

---

## ROADMAP Milestones Progress

- **Stated Goal**: ROADMAP.md documents 7 milestones toward RFC 6716 compliance.
- **Current State**: Milestone 1 (range coder) is now implemented. Milestones 2-7 remain.
- **Impact**: Packets are not yet interoperable with standard Opus decoders. This is clearly documented but represents the largest functional gap.
- **Closing the Gap**:
  1. ~~Milestone 1 (Range coder): Implement `RangeEncoder`/`RangeDecoder` per RFC 6716 §4.1.~~ ✅ COMPLETE
  2. Milestone 2 (CELT): Implement MDCT, band energy, PVQ for 48 kHz path.
  3. Milestone 3 (SILK): Implement LPC, pitch prediction, excitation coding for 8/16 kHz.
  4. Milestone 4 (Hybrid): Split 24 kHz into SILK + CELT bands.
  5. Milestone 5 (Variable durations): Support 2.5–60 ms frames, multi-frame packets.
  6. Milestone 6 (Standard Decoder): Decode libopus packets.
  7. Milestone 7 (Conformance): Pass official Opus test vectors.

---

## Gap Summary by Priority

| Gap | Severity | Effort | ROADMAP Alignment | Status |
|-----|----------|--------|-------------------|--------|
| ~~Decoder.Decode discards samples~~ | ~~High~~ | ~~Low~~ | — | ✅ RESOLVED |
| SetBitrate no-op | Medium | Low | Milestone 2e | Open |
| ~~Decoder ignores configuration~~ | ~~Medium~~ | ~~Low~~ | Milestone 6 | ✅ RESOLVED |
| No Application type parameter | Low | Low | pion/opus compat | Open |
| No complexity control | Low | Low | pion/opus compat | Open |
| No bandwidth control | Low | Low | pion/opus compat | Open |
| ~~High memory allocation~~ | ~~Medium~~ | ~~Medium~~ | Performance | ✅ RESOLVED |
| ROADMAP milestones | — | High | Milestones 2-7 | Milestone 1 ✅ |

---

*Updated: 2026-03-22 | Milestone 1 (Range Coder) completed*
