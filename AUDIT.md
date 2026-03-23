# AUDIT — 2026-03-23

## Project Goals

**magnum** is a minimal, pure-Go Opus-compatible audio encoder following [pion/opus](https://github.com/pion/opus) API patterns. According to the README:

1. **Pure Go implementation** — zero CGO, no external dependencies beyond stdlib
2. **Opus TOC header compliance** — RFC 6716 §3.1 compliant packet structure
3. **pion/opus API compatibility** — follow the established patterns from pion/opus
4. **Multi-sample-rate support** — 8, 16, 24, 48 kHz
5. **Mono and stereo support** — 1 or 2 channels with interleaved PCM
6. **Encode/decode round-trip** — lossless reconstruction via included Decode function
7. **Encoder type with buffering** — accumulate samples and emit 20ms frames
8. **Decoder type** — structured decoder with configuration validation
9. **Range Coder implementation** — RFC 6716 §4.1 entropy coding infrastructure
10. **Sentinel errors** — exported errors for `errors.Is` branching

The project explicitly disclaims RFC 6716 payload compliance (uses flate, not SILK/CELT) and documents this in the README "Limitations" section.

## Goal-Achievement Summary

| Goal | Status | Evidence |
|------|--------|----------|
| Pure Go, zero CGO | ✅ Achieved | `go.mod:1-3` — no external deps; all stdlib imports |
| RFC 6716 TOC header | ✅ Achieved | `bitstream.go:118-153` — correct bit layout per §3.1 |
| pion/opus API patterns | ⚠️ Partial | `NewEncoder` lacks Application param; `NewDecoder` lacks config |
| Multi-sample-rate (8/16/24/48k) | ✅ Achieved | `bitstream.go:12-19`, tested in `encoder_test.go:11-28` |
| Mono/stereo support | ✅ Achieved | `encoder.go:37-39`, `encoder_test.go:239-263` |
| Encode/decode round-trip | ✅ Achieved | `encoder_test.go:267-299` — verified sample-exact |
| Encoder with buffering | ✅ Achieved | `frame.go:12-93` — correct frame accumulation |
| Decoder type | ✅ Achieved | `decoder.go:19-144` — validates channels and sample rate |
| Range Coder (RFC 6716 §4.1) | ✅ Achieved | `range_coder.go:1-203` — round-trip tests pass |
| Sentinel errors | ✅ Achieved | `errors.go:1-40` — 8 exported error sentinels |
| SetBitrate functional | ⚠️ Partial | `encoder.go:71-84` — stored but unused (documented) |

## Findings

### CRITICAL

_No critical findings. All documented features are functional._

### HIGH

- [ ] **Range coder not verified against reference implementation** — `range_coder.go:1-203` — ROADMAP Milestone 1 marks "Verify bit-exact output against reference C implementation" as incomplete. The round-trip tests pass internally but RFC 6716 §4.1 bit-exactness is not confirmed against libopus `entenc.c`/`entdec.c`. — **Remediation:** Add test vectors extracted from libopus encoding known symbols; compare magnum output byte-for-byte. Validate: `go test -v -run TestRangeCoderBitExact ./...`

### MEDIUM

- [ ] **pion/opus API divergence: NewEncoder signature** — `encoder.go:33` — pion/opus `NewEncoder` accepts `(sampleRate, channels, Application)`. magnum omits the Application parameter, breaking drop-in API compatibility. — **Remediation:** Add `type Application int` with constants (VoIP, Audio, LowDelay); add `NewEncoderWithApplication(sampleRate, channels int, app Application)` constructor; `NewEncoder` defaults to Audio. Validate: `go build ./...`

- [ ] **pion/opus API divergence: NewDecoder signature** — `decoder.go:40` — pion/opus `NewDecoder()` takes no arguments; magnum requires `(sampleRate, channels int)`. Different design philosophy but noted for API compatibility. — **Remediation:** Document this design choice explicitly in Decoder godoc as intentional (configuration-upfront model). Validate: review godoc output.

- [ ] **SetBitrate is documented but non-functional** — `encoder.go:71-84` — README API table says "Set target bitrate (bps, clamped to 6000–510000)" but the field is never read. While documented in Limitations, the API table does not indicate it's a no-op. — **Remediation:** Add `// NOTE:` comment to API table entry explicitly stating "stored but not yet used". Validate: `grep -n "SetBitrate" README.md`

- [ ] **Decoder memory allocation on every packet** — `decoder.go:214` — `decodeInternal` allocates `make([]int16, len(raw)/2)` on every call. Unlike Encoder (which reuses buffers), Decoder has no buffer reuse strategy. — **Remediation:** Add optional pre-allocated buffer to `decodeInternal` or document that `Decoder.Decode(packet, out)` with sufficient `out` slice avoids the allocation. Validate: `go test -bench=BenchmarkDecode -benchmem ./...`

- [ ] **Magic numbers in bitstream.go sampleRateForConfig** — `bitstream.go:96-115` — Configuration ranges (0-3, 4-7, etc.) are hardcoded without RFC section references. — **Remediation:** Add inline comment `// RFC 6716 Table 2` above the switch and reference each range to the table. Validate: code review.

### LOW

- [ ] **File cohesion: errors.go flagged as generic name** — `errors.go:1` — go-stats-generator flags `errors.go` as a generic filename. This is a common Go idiom but the analyzer suggests consolidating errors with their related code. — **Remediation:** No action required; this follows standard Go library conventions (e.g., `net/http/errors.go`). Mark as acknowledged pattern.

- [ ] **Unused frame code constants** — `bitstream.go:43-49` — `frameCodeTwoEqualFrames`, `frameCodeTwoDifferentFrames`, `frameCodeArbitraryFrames` are defined but never used. Flagged as dead code by go-stats-generator. — **Remediation:** Add `// Reserved for ROADMAP Milestone 5` comment to each constant. Validate: `go vet ./...`

- [ ] **Package name vs directory mismatch** — `bitstream.go:8` — go-stats-generator reports directory name may differ from package. The package is `magnum` in the `magnum` directory (via go.mod module path), which is correct. — **Remediation:** No action required; false positive from analyzer checking directory basename.

- [ ] **Exported constants for internal use** — `bitstream.go:54-66` — `ConfigurationSILKNB20ms`, etc. are exported but primarily useful internally. Public for potential future use. — **Remediation:** Keep exported for API completeness; document in README API table (already done).

- [ ] **3 NOTE annotations in codebase** — `encoder.go:68-70`, undocumented elsewhere — go-stats-generator found 3 NOTE comments. These are informational and appropriate. — **Remediation:** No action required; NOTE annotations are well-placed.

- [ ] **decodeInternal complexity** — `decoder.go:176-219` — Cyclomatic complexity 9, overall 12.2 (highest in codebase). Still under 15 threshold but worth monitoring. — **Remediation:** Consider extracting TOC parsing into a helper if complexity grows. Current state acceptable.

## Metrics Snapshot

| Metric | Value |
|--------|-------|
| Total Lines of Code | 328 |
| Total Functions | 12 |
| Total Methods | 27 |
| Total Structs | 5 |
| Average Function Length | 9.9 lines |
| Average Complexity | 3.4 |
| High Complexity (>10) | 0 functions |
| Documentation Coverage | 100% |
| Test Coverage (estimated) | High (25 test functions) |
| Encode 48k mono (B/op) | 3,608 B/op, 3 allocs |
| Decode 48k mono (B/op) | 47,496 B/op, 13 allocs |
| Range Coder (B/op) | 0 B/op, 0 allocs |

## Test Results

```
$ go test -race ./...
ok  	github.com/opd-ai/magnum	(cached)

$ go vet ./...
(no output — clean)

$ go build ./...
(no output — clean)
```

## Conclusion

magnum achieves its stated goals as a **simplified reference implementation**. The core encode/decode path is functional, well-tested, and correctly implements the RFC 6716 TOC header structure. The Range Coder (Milestone 1) is implemented and passes internal round-trip tests.

The main gaps are:
1. Range coder bit-exactness against libopus not verified
2. pion/opus API divergence (Application parameter, Decoder signature)
3. SetBitrate is non-functional (documented limitation)

These are acknowledged in the project's own documentation and ROADMAP, and do not represent undisclosed deficiencies.

---
*Generated by functional audit on 2026-03-23*
