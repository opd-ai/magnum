# Implementation Gaps — 2026-03-23

This document identifies gaps between the project's stated goals and current implementation. Gaps are ordered by impact on users seeking to adopt magnum.

---

## Range Coder Bit-Exactness Not Verified

- **Stated Goal**: ROADMAP Milestone 1 states "Verify bit-exact output against the reference C implementation (`opus/celt/entenc.c`, `entdec.c`) for a shared set of test vectors."
- **Current State**: `range_coder.go:1-203` passes internal round-trip tests (`range_coder_test.go`) but has not been validated against libopus output. The success criteria "output bytes match the reference implementation byte-for-byte" is not confirmed.
- **Impact**: Future CELT/SILK integration (Milestones 2-3) depends on bit-exact range coding. If the current implementation diverges from RFC 6716 §4.1, encoded packets will be incompatible even when codec logic is added.
- **Closing the Gap**:
  1. Extract test vectors from libopus by instrumenting `ec_enc`/`ec_dec` calls
  2. Add `TestRangeCoderBitExact` comparing byte output against known vectors
  3. Fix any divergences in normalize/finalize logic
  4. Validation: `go test -v -run TestRangeCoderBitExact ./...`

---

## SetBitrate Is Non-Functional

- **Stated Goal**: README API table documents `SetBitrate(bitrate int)` — "Set target bitrate (bps, clamped to 6000–510000)."
- **Current State**: `encoder.go:71-84` stores the bitrate value with proper clamping but the flate compressor uses `flate.DefaultCompression` (line 48). The `e.bitrate` field is never read after being set.
- **Impact**: Users calling `SetBitrate(32000)` vs `SetBitrate(128000)` observe identical packet sizes. The API implies active control but delivers none.
- **Closing the Gap**:
  1. Short-term: Update README API table to say "Set target bitrate (stored for future CELT/SILK integration; currently unused)"
  2. Long-term (Milestone 2e): Map bitrate to flate compression level as interim measure, or implement CELT bit allocation
  3. Validation: Encode frames at different bitrates, assert identical output (confirms documented limitation)

---

## pion/opus API Divergence: Application Parameter

- **Stated Goal**: README states the encoder follows "pion/opus API patterns."
- **Current State**: pion/opus `NewEncoder` (and libopus) accepts an Application type parameter (VoIP, Audio, LowDelay). magnum's `NewEncoder(sampleRate, channels int)` omits this parameter.
- **Impact**: Code written for pion/opus cannot be migrated to magnum without signature changes. Future mode selection (SILK vs CELT preference) will require this parameter.
- **Closing the Gap**:
  1. Define `type Application int` with constants `ApplicationVoIP`, `ApplicationAudio`, `ApplicationLowDelay`
  2. Add `NewEncoderWithApplication(sampleRate, channels int, app Application) (*Encoder, error)`
  3. Keep `NewEncoder` as convenience wrapper defaulting to `ApplicationAudio`
  4. Store `app` in Encoder struct for future codec selection
  5. Validation: `go build ./...`

---

## pion/opus API Divergence: Decoder Signature

- **Stated Goal**: Follow pion/opus API patterns for decoder.
- **Current State**: pion/opus `NewDecoder()` takes no arguments. magnum `NewDecoder(sampleRate, channels int)` requires configuration upfront.
- **Impact**: Different instantiation pattern; migration requires code changes. However, magnum's approach enables validation of packet metadata against decoder configuration (detecting mismatches early).
- **Closing the Gap**:
  1. This is a deliberate design choice offering stricter validation
  2. Document this divergence explicitly in `NewDecoder` godoc
  3. Consider adding `NewDecoderAuto()` that infers config from first packet (optional)
  4. Validation: Review godoc with `go doc magnum.NewDecoder`

---

## Decoder Memory Allocation Per Packet

- **Stated Goal**: Efficient audio processing for streaming applications.
- **Current State**: `decoder.go:214` allocates `make([]int16, len(raw)/2)` on every decode call. Benchmark shows 47,496 B/op and 13 allocs per decode vs 3,608 B/op and 3 allocs per encode.
- **Impact**: High-throughput applications (real-time audio) may see GC pressure. Encoder optimized allocations; decoder did not receive same treatment.
- **Closing the Gap**:
  1. When `Decoder.Decode(packet, out)` is called with sufficient `out` buffer, samples are copied into it — this path already avoids the extra allocation
  2. Document this optimization path in `Decoder.Decode` godoc
  3. Consider adding buffer pool for `decodeInternal` raw byte slice
  4. Validation: `go test -bench=BenchmarkDecode -benchmem ./...` — verify B/op when out buffer is pre-allocated

---

## Missing Complexity Control

- **Stated Goal**: pion/opus-compatible API patterns for audio encoding.
- **Current State**: pion/opus offers `SetComplexity(complexity int)` to trade CPU for quality. magnum has no equivalent method.
- **Impact**: Users cannot tune encoding performance vs quality. Less critical for current flate-based implementation but will matter for CELT.
- **Closing the Gap**:
  1. Add `SetComplexity(complexity int)` as no-op placeholder with doc comment
  2. Store value for future CELT integration
  3. Validation: `go build ./...`

---

## Missing Bandwidth Control

- **Stated Goal**: pion/opus-compatible API patterns.
- **Current State**: pion/opus offers bandwidth control (auto, narrowband, wideband, etc.). magnum derives bandwidth implicitly from sample rate at encoder creation.
- **Impact**: Cannot force lower bandwidth for constrained channels without changing sample rate.
- **Closing the Gap**:
  1. Add `type Bandwidth int` with constants matching pion/opus
  2. Add `SetBandwidth(bandwidth Bandwidth)` as placeholder
  3. Document that bandwidth is currently derived from sample rate
  4. Validation: `go build ./...`

---

## ROADMAP Milestones 2-7 Not Implemented

- **Stated Goal**: ROADMAP.md documents 7 milestones toward RFC 6716 compliance and libopus interoperability.
- **Current State**: Only Milestone 1 (Range Coder) is complete. Milestones 2-7 (CELT, SILK, Hybrid, Variable durations, Standard Decoder, Conformance) are not implemented.
- **Impact**: Packets are **not interoperable** with standard Opus decoders. This is clearly documented but represents the largest functional gap for users expecting Opus compatibility.
- **Closing the Gap**:
  1. Milestone 2: Implement CELT (MDCT, band energy, PVQ) for 48 kHz path
  2. Milestone 3: Implement SILK (LPC, pitch, excitation) for 8/16 kHz
  3. Milestone 4: Implement Hybrid mode for 24 kHz
  4. Milestone 5: Support variable frame durations and multi-frame packets
  5. Milestone 6: Expose Decoder that handles libopus packets
  6. Milestone 7: Pass official Opus test vectors
  7. Each milestone has detailed tasks in ROADMAP.md

---

## Gap Summary by Priority

| Gap | Severity | Effort | ROADMAP Alignment | Status |
|-----|----------|--------|-------------------|--------|
| Range coder bit-exactness | High | Medium | Milestone 1 (partial) | Open |
| SetBitrate non-functional | Medium | Low | Milestone 2e | Open |
| No Application parameter | Medium | Low | pion/opus compat | Open |
| Decoder signature divergence | Low | Low | pion/opus compat | By Design |
| Decoder memory allocation | Low | Medium | Performance | Partial (workaround exists) |
| No complexity control | Low | Low | pion/opus compat | Open |
| No bandwidth control | Low | Low | pion/opus compat | Open |
| ROADMAP Milestones 2-7 | — | High | Milestones 2-7 | Open |

---

## Recommendations

1. **Immediate** (before v1.0 release):
   - Add range coder bit-exact test vectors
   - Update README to clarify SetBitrate is a placeholder
   - Add placeholder methods for Complexity and Bandwidth

2. **Short-term** (Milestone 2):
   - Implement CELT encoder for 48 kHz to achieve libopus interoperability
   - Wire SetBitrate to CELT bit allocation

3. **Long-term** (Milestones 3-7):
   - Complete SILK, Hybrid, variable durations
   - Pass official Opus conformance tests

---

*Generated by functional audit on 2026-03-23*
