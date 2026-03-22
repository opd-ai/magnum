# Implementation Plan: API Quality and Usability Enhancements

## Project Context
- **What it does**: A minimal, pure-Go Opus-compatible audio encoder following pion/opus API patterns, using flate compression (not RFC 6716 SILK/CELT).
- **Current goal**: Improve API quality and usability before embarking on RFC 6716 codec implementation (Milestone 1+).
- **Estimated Scope**: Small

## Goal-Achievement Status

| Stated Goal | Current Status | This Plan Addresses |
|-------------|----------------|---------------------|
| Pure-Go implementation (zero CGO) | ✅ Achieved | No |
| pion/opus API compatibility | ✅ Achieved | Yes (Decoder type symmetry) |
| RFC 6716 §3.1 TOC header compliance | ✅ Achieved | Yes (decode validation) |
| Multi-rate support (8k/16k/24k/48k) | ✅ Achieved | No |
| Mono/stereo support | ✅ Achieved | Yes (decode channel info) |
| 20 ms frame encoding | ✅ Achieved | No |
| SetBitrate API | ⚠️ Stored but unused | Yes (documentation) |
| Round-trip Decode | ✅ Achieved | Yes (improvements) |
| Sentinel errors for errors.Is | ✅ Achieved | Yes (error wrapping) |
| End-of-stream flush | ❌ Not exposed | Yes |
| Decoder type (pion/opus symmetry) | ❌ Not implemented | Yes |
| Performance benchmarks | ❌ Not implemented | Yes |

## Metrics Summary

- **Complexity hotspots on goal-critical paths**: 1 function above threshold (`Decode` at 10.9 overall complexity, cyclomatic 8)
- **Duplication ratio**: 0%
- **Doc coverage**: 100%
- **Package coupling**: 0 (single package, no external dependencies)

### Anti-patterns Flagged
| Type | Severity | File:Line | Impact |
|------|----------|-----------|--------|
| Bare error return | High | encoder.go:117 | Debugging difficult |
| Bare error return | High | encoder.go:120 | Debugging difficult |
| Bare error return | High | encoder.go:123 | Debugging difficult |
| Memory allocation in loop | Medium | frame.go:44,49,55 | Minor performance impact |

## Implementation Steps

### Step 1: Wrap compression errors with context
- **Deliverable**: Modify `encodeFrame()` in `encoder.go:116-124` to wrap errors from `flate.NewWriter()`, `w.Write()`, and `w.Close()` with `fmt.Errorf("magnum: encode frame: %w", err)`. Add `"fmt"` import.
- **Dependencies**: None
- **Goal Impact**: Improves error handling; supports "sentinel errors for errors.Is" goal by preserving error chain.
- **Acceptance**: `go-stats-generator` reports 0 `bare_error_return` anti-patterns in encoder.go.
- **Validation**: `go-stats-generator analyze . --skip-tests --format json --sections patterns | jq '.patterns.anti_patterns.performance_antipatterns | map(select(.type == "bare_error_return" and .file | contains("encoder.go"))) | length'` → `0`

### Step 2: Add Flush method to Encoder
- **Deliverable**: Add exported `(*Encoder).Flush() ([]byte, error)` method in `encoder.go` that calls `fb.flush()` and encodes the zero-padded partial frame. Returns `nil, nil` if no partial frame buffered.
- **Dependencies**: None
- **Goal Impact**: Closes "No end-of-stream flush" gap; prevents loss of final partial frame.
- **Acceptance**: New test `TestEncoderFlush` passes; doc coverage remains 100%.
- **Validation**: `go test -v -run TestEncoderFlush ./... && go-stats-generator analyze . --skip-tests --format json --sections documentation | jq '.documentation.coverage.functions'` → `100`

### Step 3: Parse and validate TOC header in Decode
- **Deliverable**: Modify `Decode()` in `encoder.go` to parse the TOC byte using `tocHeader` methods, validate `frameCode == frameCodeOneFrame`, and return a new `ErrUnsupportedFrameCode` if multi-frame packets are encountered. Add new sentinel error to `errors.go`.
- **Dependencies**: None
- **Goal Impact**: Improves "RFC 6716 §3.1 TOC header compliance" by validating packet structure; hardens decoder against invalid input.
- **Acceptance**: New test `TestDecodeInvalidFrameCode` passes; new sentinel error is exported.
- **Validation**: `go test -v -run TestDecodeInvalidFrameCode ./... && grep -q 'ErrUnsupportedFrameCode' errors.go`

### Step 4: Add DecodeWithInfo variant returning stereo flag
- **Deliverable**: Add `DecodeWithInfo(packet []byte) (samples []int16, stereo bool, err error)` function in `encoder.go`. Refactor existing `Decode()` to call `DecodeWithInfo` internally.
- **Dependencies**: Step 3 (TOC parsing)
- **Goal Impact**: Closes "Stereo decode ambiguity" gap; enables callers to verify channel count at decode time.
- **Acceptance**: New test `TestDecodeWithInfoReturnsStereoFlag` passes for both mono and stereo packets.
- **Validation**: `go test -v -run TestDecodeWithInfoReturnsStereoFlag ./...`

### Step 5: Document SetBitrate no-op status
- **Deliverable**: Add `// NOTE:` comment at `encoder.go:54-57` explaining that `SetBitrate` is a placeholder stored for future codec integration and does not affect current flate-based output.
- **Dependencies**: None
- **Goal Impact**: Clarifies "SetBitrate API" gap; prevents user confusion.
- **Acceptance**: `grep -q "NOTE:" encoder.go`; doc comment explains no-op status.
- **Validation**: `grep -A2 "NOTE:" encoder.go | grep -q "does not affect"`

### Step 6: Pre-allocate ready queue in frameBuffer
- **Deliverable**: Modify `newFrameBuffer()` in `frame.go:26-31` to pre-allocate the `ready` slice with `make([][]int16, 0, 4)` to reduce allocations during typical streaming use.
- **Dependencies**: None
- **Goal Impact**: Addresses "Memory allocation in loop" anti-patterns; improves streaming performance.
- **Acceptance**: `go-stats-generator` reports ≤1 memory_allocation anti-pattern in frame.go (the remaining append in write loop is acceptable with pre-sized partial buffer).
- **Validation**: `go-stats-generator analyze . --skip-tests --format json --sections patterns | jq '[.patterns.anti_patterns.performance_antipatterns[] | select(.file | contains("frame.go") and .type == "memory_allocation")] | length'` → `≤2` (append in loop is unavoidable but ready queue is pre-sized)

### Step 7: Add Decoder type for pion/opus symmetry
- **Deliverable**: Add `type Decoder struct` in new file `decoder.go` with `NewDecoder(sampleRate, channels int) (*Decoder, error)` and `(*Decoder).Decode(packet []byte, out []int16) (int, error)` method. Store expected sample rate and channels for future validation.
- **Dependencies**: Steps 3-4 (DecodeWithInfo)
- **Goal Impact**: Closes "Missing Decoder type" gap; achieves pion/opus API symmetry.
- **Acceptance**: New tests `TestNewDecoder` and `TestDecoderDecode` pass; `go doc` shows Decoder type.
- **Validation**: `go test -v -run 'TestNewDecoder|TestDecoderDecode' ./... && go doc github.com/opd-ai/magnum | grep -q 'type Decoder'`

### Step 8: Add performance benchmarks
- **Deliverable**: Add `BenchmarkEncode48kMono`, `BenchmarkEncode48kStereo`, `BenchmarkDecode` in `encoder_test.go`. Measure ns/op and B/op.
- **Dependencies**: None (can be done in parallel with other steps)
- **Goal Impact**: Closes "No benchmarks" gap; enables performance regression detection per ROADMAP Milestone 7.
- **Acceptance**: `go test -bench=. -benchmem ./...` runs without error and reports meaningful metrics.
- **Validation**: `go test -bench=. -benchmem ./... 2>&1 | grep -E 'Benchmark(Encode|Decode)'`

## Scope Assessment Rationale

| Metric | Value | Threshold Category |
|--------|-------|-------------------|
| Functions above complexity 9.0 | 1 (`Decode` at 10.9) | Small (<5) |
| Duplication ratio | 0% | Small (<3%) |
| Doc coverage gap | 0% | Small (<10%) |
| Anti-patterns (high severity) | 3 bare_error_return | Small (<5) |
| Anti-patterns (medium severity) | 3 memory_allocation | Small (<5) |

All metrics fall within "Small" thresholds. The implementation work is confined to a single package with 4 source files and ~130 lines of code.

## Order Rationale

1. **Step 1** (error wrapping) has no dependencies and improves debuggability immediately.
2. **Step 2** (Flush) is independent and addresses a user-facing gap.
3. **Step 3** (TOC validation) is prerequisite for Step 4.
4. **Step 4** (DecodeWithInfo) builds on TOC parsing.
5. **Step 5** (SetBitrate docs) is independent documentation.
6. **Step 6** (pre-allocation) is independent performance improvement.
7. **Step 7** (Decoder type) depends on DecodeWithInfo being available.
8. **Step 8** (benchmarks) is independent and can be done in parallel.

## Notes

- This plan focuses on pre-Milestone-1 quality improvements. RFC 6716 codec work (range coder, CELT, SILK) is deferred to ROADMAP milestones.
- All steps maintain the project's "zero CGO, zero dependencies" constraint.
- Test coverage (88.9% baseline) should be maintained or improved by each step.

---

*Generated: 2026-03-22*
*Tool: go-stats-generator v1.0.0*
*Baseline: magnum v0.0.0 (unreleased)*
