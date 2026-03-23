# AUDIT — 2026-03-23

## Project Goals

**magnum** is a minimal, pure-Go Opus-compatible audio encoder designed to follow [pion/opus](https://github.com/pion/opus) API patterns. The project explicitly states it is a **simplified reference implementation** that:

1. Wraps 20 ms PCM frames in valid Opus TOC-header packets (RFC 6716 §3.1)
2. Compresses payload with Go's `compress/flate` (not SILK/CELT)
3. Provides round-trip encode/decode within its own ecosystem
4. Supports sample rates: 8000, 16000, 24000, 48000 Hz
5. Supports mono (1 channel) and stereo (2 channels)
6. Implements RFC 6716 §4.1 range coder for future SILK/CELT integration
7. Follows pion/opus API patterns for future migration path
8. Provides zero-dependency pure-Go implementation (no CGO)

**Explicit Non-Goals** (clearly documented):
- RFC 6716 compliance (SILK/CELT codecs not implemented)
- Interoperability with libopus or standard Opus decoders
- Variable frame durations or multi-frame packets
- PLC (packet loss concealment) or FEC (forward error correction)

---

## Goal-Achievement Summary

| Goal | Status | Evidence |
|------|--------|----------|
| TOC header generation per RFC 6716 §3.1 | ✅ Achieved | `bitstream.go:134-156` — correct bit layout for config, stereo, frame code |
| TOC configuration matched to sample rate | ✅ Achieved | `bitstream.go:70-80` — maps rates to SILK NB/WB and CELT SWB/FB configs |
| Interleaved PCM frame buffering (mono/stereo) | ✅ Achieved | `frame.go:26-93` — 20 ms frames, correct sample counts verified in tests |
| Encode/Decode API functional | ✅ Achieved | `encoder.go:207-216`, `decoder.go:164-167` — round-trip verified by 28 passing tests |
| Decoder type with validation | ✅ Achieved | `decoder.go:44-55` — validates sample rate, channels; returns typed errors |
| SetBitrate API (stored, documented as unused) | ✅ Achieved | `encoder.go:145-158` — stores with clamping, doc states "currently unused" |
| SetComplexity API (stored, documented as unused) | ✅ Achieved | `encoder.go:166-175` — stores 0-10 with clamping, correctly documented |
| SetBandwidth API (stored, documented as unused) | ✅ Achieved | `encoder.go:187-189` — stores Bandwidth value, correctly documented |
| Application mode API (pion/opus pattern) | ✅ Achieved | `encoder.go:86-131` — `NewEncoderWithApplication` matches pion/opus pattern |
| Range coder implementation (RFC 6716 §4.1) | ✅ Achieved | `range_coder.go:1-204` — passes extensive round-trip tests |
| Supported sample rates validated | ✅ Achieved | `bitstream.go:22-28` — 8k/16k/24k/48k only; others rejected with typed error |
| Mono/stereo validation | ✅ Achieved | `encoder.go:103-105` — channels must be 1 or 2 |
| Pure Go, zero CGO | ✅ Achieved | `go.mod:1-3` — only standard library dependencies |
| Sentinel errors for error branching | ✅ Achieved | `errors.go:1-40` — 8 exported sentinel errors |
| Channel mismatch detection | ✅ Achieved | `decoder.go:83-90` — returns `ErrChannelMismatch` |
| Sample rate mismatch detection | ✅ Achieved | `decoder.go:93-96` — returns `ErrSampleRateMismatch` |
| Zip-bomb mitigation | ✅ Achieved | `decoder.go:17`, `decoder.go:201-216` — 64 KiB limit enforced |
| Documentation coverage | ✅ Achieved | 100% doc coverage per go-stats-generator |

---

## Findings

### CRITICAL

*(None identified — all documented features work correctly)*

### HIGH

- [ ] **Range coder bit-exactness unverified against libopus** — `range_coder.go:1-204` — Internal round-trip tests pass, but ROADMAP Milestone 1 states "Verify bit-exact output against the reference C implementation." The `range_coder_vectors_test.go` tests are derived from RFC 6716 mathematical properties, not actual libopus output bytes. — **Remediation:** Extract test vectors by instrumenting libopus `ec_enc`/`ec_dec` calls, add `TestRangeCoderBitExactLibopus` comparing byte-for-byte output. Validation: `go test -v -run TestRangeCoderBitExactLibopus ./...`

### MEDIUM

- [ ] **Decoder allocates 47,496 B/op vs Encoder's 3,608 B/op** — `decoder.go:222` — `make([]int16, len(raw)/2)` allocates on every decode. Benchmark shows 13 allocs/op for decode vs 3 allocs/op for encode. High-throughput real-time audio may experience GC pressure. — **Remediation:** Add internal buffer pool for `decodeInternal` raw byte slice; document that `Decoder.Decode(packet, out)` with pre-sized `out` avoids sample allocation. Validation: `go test -bench=BenchmarkDecode -benchmem ./...` should show reduced B/op.

- [ ] **frameBuffer.ready slice grows unboundedly** — `frame.go:56` — `fb.ready = append(fb.ready, frame)` can grow if caller feeds data faster than consuming via `next()`. No cap or warning mechanism. — **Remediation:** Add optional max queue depth parameter to `newFrameBuffer`; return error or drop oldest when exceeded. Validation: Create test feeding 1000 frames without calling `next()`, verify memory bounded.

- [ ] **Magic numbers in bitstream.go configuration switch** — `bitstream.go:100-115` — Configuration thresholds (3, 7, 11, 15, 19, 23, 27, 31) are magic numbers without named constants. — **Remediation:** Define named constants for configuration ranges per RFC 6716 Table 2 (e.g., `configSILKNBMax = 3`, `configSILKMBMax = 7`). Validation: `go build ./...`

### LOW

- [ ] **Package name/directory mismatch flagged by go-stats-generator** — `bitstream.go:8` — Package `magnum` in directory `magnum` triggers `directory_mismatch` due to go.mod module path `github.com/opd-ai/magnum`. This is a false positive; the naming is correct. — **Remediation:** No action needed; document as acknowledged false positive in code style guide.

- [ ] **Unused frame codes defined but not implemented** — `bitstream.go:42-49` — `frameCodeTwoEqualFrames`, `frameCodeTwoDifferentFrames`, `frameCodeArbitraryFrames` are defined with TODO comments for Milestone 5. Decode rejects them with `ErrUnsupportedFrameCode`. — **Remediation:** No action needed now; constants correctly reserved for roadmap. Validation: Ensure decode tests cover rejection (already present: `encoder_test.go:439-452`).

- [ ] **errors.go flagged as generic filename** — `errors.go:1` — go-stats-generator suggests non-generic name. This is idiomatic Go; package-scoped sentinel errors in `errors.go` is standard practice. — **Remediation:** No action needed; idiomatic Go pattern.

- [ ] **11 functions flagged as "dead code" are test-only or internal** — Various — Functions like `frameCodeTwoEqualFrames`, `buffered()`, `Remaining()` appear unused in production paths but are either test utilities, reserved for roadmap, or intentionally exported API. — **Remediation:** No action needed; these are not dead code but reserved or test-facing API.

---

## Metrics Snapshot

| Metric | Value |
|--------|-------|
| Total Lines of Code | 344 |
| Total Functions | 13 |
| Total Methods | 32 |
| Total Structs | 5 |
| Average Function Length | 8.8 lines |
| Longest Function | `decodeInternal` (42 lines) |
| Functions > 50 lines | 0 (0.0%) |
| Average Complexity | 3.1 |
| High Complexity (>10) | 0 functions |
| Top Complexity | `decodeInternal` (12.2 overall, 9 cyclomatic) |
| Documentation Coverage | 100% (package, function, type, method) |
| Build Status | ✅ Passing |
| Test Status | ✅ All passing (28 tests) |
| Race Detector | ✅ Clean |
| go vet | ✅ Clean |
| Encode Benchmark | 60,525 ns/op, 3,608 B/op, 3 allocs/op (48kHz mono) |
| Decode Benchmark | 21,268 ns/op, 47,496 B/op, 13 allocs/op |
| Range Encoder Benchmark | 162.3 ns/op, 0 B/op |
| Range Decoder Benchmark | 337.6 ns/op, 0 B/op |

---

## Summary

The project achieves **all stated goals** with high code quality. The implementation correctly:

1. ✅ Generates valid Opus TOC headers per RFC 6716 §3.1
2. ✅ Provides working encode/decode round-trips using flate compression
3. ✅ Follows pion/opus API patterns (`NewEncoder`, `NewEncoderWithApplication`, `Decode`, `DecodeAlloc`)
4. ✅ Implements placeholder APIs for bitrate, complexity, bandwidth (correctly documented as unused)
5. ✅ Implements RFC 6716 §4.1 range coder foundation for future SILK/CELT work
6. ✅ Validates inputs with typed sentinel errors
7. ✅ Protects against zip-bomb attacks with decompression limits
8. ✅ Maintains 100% documentation coverage
9. ✅ Passes all tests including race detector

The **one HIGH severity finding** (range coder bit-exactness) does not affect current functionality but will block Milestone 2+ progress. The codebase is well-positioned for its stated roadmap evolution toward RFC 6716 compliance.

---

*Generated by functional audit on 2026-03-23*
