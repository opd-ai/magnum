# Implementation Gaps — 2026-03-23

This document identifies gaps between the project's stated goals and current implementation. Gaps are ordered by impact on users seeking to adopt magnum.

---

## Range Coder Bit-Exactness Not Verified Against libopus

- **Stated Goal**: ROADMAP Milestone 1 states "Verify bit-exact output against the reference C implementation (`opus/celt/entenc.c`, `entdec.c`) for a shared set of test vectors."
- **Current State**: `range_coder.go:1-204` passes extensive internal round-trip tests (`range_coder_test.go`, `range_coder_vectors_test.go`) including 471 test cases covering uniform, skewed, large ft, and mixed encoding sequences. However, the test vectors were derived from RFC 6716 mathematical properties, not extracted from actual libopus byte output. The success criteria "output bytes match the reference implementation byte-for-byte" is not confirmed.
- **Impact**: Future CELT/SILK integration (Milestones 2-3) depends on bit-exact range coding. If the current implementation diverges from libopus in edge cases (normalization timing, finalization byte ordering), encoded packets will be incompatible even when codec logic is added. This is **blocking for roadmap progress**.
- **Closing the Gap**:
  1. Instrument libopus `ec_enc`/`ec_dec` functions to capture input symbols and output bytes
  2. Generate test vectors: `{symbols: [{fl, fh, ft}...], expected_bytes: [...]}`
  3. Add `TestRangeCoderBitExactLibopus` comparing magnum output byte-for-byte against captured vectors
  4. Fix any divergences in normalize/finalize logic (likely in `Bytes()` flush sequence)
  5. Validation: `go test -v -run TestRangeCoderBitExactLibopus ./...`

---

## Decoder Memory Allocation Per Packet

- **Stated Goal**: Efficient audio processing for streaming applications; pion/opus API compatibility.
- **Current State**: `decoder.go:222` allocates `make([]int16, len(raw)/2)` on every decode call. Benchmark shows 47,496 B/op and 13 allocs/op for decode vs 3,608 B/op and 3 allocs/op for encode. The decoder also allocates via `io.ReadAll` at `decoder.go:205`.
- **Impact**: High-throughput applications (real-time audio, voice chat servers processing multiple streams) may experience GC pressure. The encoder received allocation optimization (reusable `outputBuf`, `flateW`, `rawPCM`); the decoder did not receive equivalent treatment.
- **Closing the Gap**:
  1. Add `rawBuffer []byte` field to `Decoder` struct for `io.ReadAll` target
  2. Reuse existing buffer when capacity sufficient; grow only when needed
  3. Document that `Decoder.Decode(packet, out)` with pre-allocated `out` avoids final sample allocation
  4. Consider `sync.Pool` for raw byte buffer if decoder instances are short-lived
  5. Validation: `go test -bench=BenchmarkDecode -benchmem ./...` — target ≤5 allocs/op

---

## frameBuffer Queue Unbounded Growth

- **Stated Goal**: Robust frame buffering for streaming audio.
- **Current State**: `frame.go:56` appends completed frames to `fb.ready` without any upper bound. If a caller feeds samples faster than they consume frames via `next()`, memory grows without limit.
- **Impact**: In streaming scenarios with backpressure issues (e.g., encoder faster than network), memory could grow unboundedly. Current mitigation: initial `ready` capacity of 4 hints at expected usage, but no enforcement.
- **Closing the Gap**:
  1. Add optional `maxQueueDepth` parameter to `newFrameBuffer`
  2. When queue full, either: (a) block/return error, or (b) drop oldest frame with warning
  3. For backwards compatibility, default to unbounded (current behavior) when 0
  4. Validation: Add test feeding 1000+ frames without draining; verify behavior matches config

---

## ROADMAP Milestones 2-7 Not Implemented

- **Stated Goal**: ROADMAP.md documents 7 milestones toward RFC 6716 compliance and libopus interoperability.
- **Current State**: Only Milestone 1 (Range Coder) is implemented. Milestones 2-7 remain open:
  - Milestone 2: CELT encoder (MDCT, band energy, PVQ) — ❌ not started
  - Milestone 3: SILK encoder (LPC, pitch, excitation) — ❌ not started
  - Milestone 4: Hybrid mode (SILK + CELT) — ❌ not started
  - Milestone 5: Variable frame durations, multi-frame packets — ❌ not started
  - Milestone 6: Standard Decoder (libopus packet decoding) — ❌ not started
  - Milestone 7: Conformance testing, official test vectors — ❌ not started
- **Impact**: Packets are **not interoperable** with standard Opus decoders. This is **the primary limitation** but is clearly documented in README ("Not RFC 6716 compliant"). Users adopting magnum understand this constraint.
- **Closing the Gap**: Follow ROADMAP.md milestone plan in order:
  1. Complete Milestone 1 (verify range coder bit-exactness — see Gap #1)
  2. Implement Milestone 2 (CELT) for 48 kHz path → achieves libopus interop for fullband
  3. Implement Milestone 3 (SILK) for 8/16 kHz → achieves narrowband/wideband interop
  4. Milestones 4-7 as specified in ROADMAP.md
  5. Validation per milestone: `opusdec` / `opus_demo` / official test vectors

---

## SetBitrate, SetComplexity, SetBandwidth Are No-ops

- **Stated Goal**: README API table documents these methods for pion/opus API compatibility.
- **Current State**: All three methods correctly store values but have no effect on encoding output. This is **explicitly documented** in code comments (`encoder.go:143-144`, `encoder.go:164-165`, `encoder.go:185-186`) and README ("Stored for future CELT/SILK integration; currently unused").
- **Impact**: Users calling `SetBitrate(32000)` vs `SetBitrate(128000)` observe identical packet sizes. However, this is **not a gap** — the behavior is correctly documented. The API surface exists for forward compatibility.
- **Closing the Gap**: No immediate action needed. When Milestone 2 (CELT) is implemented:
  1. Wire `SetBitrate` to CELT bit allocation table
  2. Wire `SetComplexity` to CELT search depth parameters
  3. Wire `SetBandwidth` to band cutoff selection
  4. Update documentation to reflect active usage
  5. Validation: Encode same audio at different bitrates; verify different packet sizes

---

## pion/opus Decoder API Divergence (By Design)

- **Stated Goal**: Follow pion/opus API patterns.
- **Current State**: pion/opus `NewDecoder()` takes no arguments; magnum `NewDecoder(sampleRate, channels int)` requires configuration upfront. This is a **deliberate design choice** documented in `decoder.go:37-43`.
- **Impact**: Code written for pion/opus cannot migrate to magnum without API changes. However, magnum's approach enables early validation — a 16 kHz decoder receiving a 48 kHz packet gets `ErrSampleRateMismatch` instead of silent corruption.
- **Closing the Gap**: This is **by design**, not a defect. Options:
  1. Document the divergence in README API table (currently implicit)
  2. Optionally add `NewDecoderAuto()` that infers config from first packet (more complex)
  3. Validation: Review godoc with `go doc magnum.NewDecoder`

---

## Gap Summary by Priority

| Gap | Severity | Effort | ROADMAP | Status |
|-----|----------|--------|---------|--------|
| Range coder bit-exactness | High | Medium | Milestone 1 | Open |
| Decoder memory allocation | Medium | Medium | — | Open |
| frameBuffer unbounded growth | Medium | Low | — | Open |
| ROADMAP Milestones 2-7 | — | High | Milestones 2-7 | Open (roadmap) |
| SetBitrate/Complexity/Bandwidth no-ops | — | — | Milestone 2+ | By Design |
| Decoder API divergence | — | — | — | By Design |

---

## Recommendations

### Immediate (before v1.0 release)

1. Add libopus-derived test vectors for range coder bit-exactness verification
2. Reduce decoder allocations by adding buffer reuse
3. Document frameBuffer queue behavior; consider optional bounds

### Short-term (Milestone 2)

1. Implement CELT encoder for 48 kHz fullband path
2. Achieve libopus interoperability for high-quality audio
3. Wire SetBitrate to CELT bit allocation

### Long-term (Milestones 3-7)

1. Complete SILK encoder for narrowband/wideband
2. Implement hybrid mode, variable frame durations
3. Pass official Opus conformance test suite

---

*Generated by functional audit on 2026-03-23*
