# Implementation Plan: RFC 6716 Range Coder Bit-Exactness & Foundation Hardening

## Project Context
- **What it does**: A minimal, pure-Go Opus-compatible audio encoder that wraps 20 ms PCM frames in Opus TOC-header packets using flate compression (not SILK/CELT) for round-trip testing.
- **Current goal**: Complete ROADMAP Milestone 1 by verifying range coder bit-exactness against libopus, then address foundation gaps blocking Milestone 2 (CELT encoder).
- **Estimated Scope**: Small

## Goal-Achievement Status
| Stated Goal | Current Status | This Plan Addresses |
|-------------|---------------|---------------------|
| RFC 6716 §4.1 range coder implementation | ⚠️ Implemented but not bit-exact verified | Yes |
| Range coder bit-exact against libopus | ❌ Test vectors derived from RFC spec, not actual libopus output | Yes |
| Decoder memory allocation optimization | ❌ 47,496 B/op vs encoder's 3,608 B/op | Yes |
| frameBuffer unbounded growth handling | ⚠️ No upper bound on ready queue | Yes |
| CELT encoder (Milestone 2) | ❌ Not started | No (blocked by above) |
| SILK encoder (Milestone 3) | ❌ Not started | No |

## Metrics Summary
- **Complexity hotspots on goal-critical paths**: 3 functions above threshold
  - `decodeInternal` (decoder.go): cyclomatic 9, overall 12.2 — highest complexity
  - `Decode` (decoder.go): cyclomatic 7, overall 9.6
  - `DecodeAlloc` (decoder.go): cyclomatic 5, overall 7.0
- **Duplication ratio**: 0% (no clones detected)
- **Doc coverage**: 100% (package, function, type, method)
- **Package coupling**: 0 external dependencies; cohesion score 1.83

## Research Findings

### Competitive Landscape
- **pion/opus**: More mature pure-Go Opus implementation with active community. Magnum differentiates by being simpler (reference implementation) and having API compatibility as explicit goal.
- **kazzmir/opus-go**: Transcompiles libopus C to Go. More interoperable but less idiomatic.
- No open issues or community discussions on magnum yet — project is new/niche.

### Dependencies
- **go.mod**: No external dependencies (stdlib only with `go 1.24.0`)
- No known vulnerabilities or deprecations to plan for

## Implementation Steps

### Step 1: Add libopus-Derived Test Vectors for Range Coder
- **Deliverable**: New test file `range_coder_libopus_test.go` containing test vectors captured from libopus `ec_enc`/`ec_dec` functions with exact byte-for-byte expected output
- **Dependencies**: None
- **Goal Impact**: Completes ROADMAP Milestone 1 success criteria: "output bytes match the reference implementation byte-for-byte"
- **Acceptance**: All `TestRangeCoderBitExactLibopus*` tests pass; divergences from libopus (if any) are documented as acceptable deviations with RFC 6716 justification
- **Validation**: `go test -v -run 'TestRangeCoderBitExact' ./...`

### Step 2: Fix Range Coder Normalization Divergences (if found)
- **Deliverable**: Updates to `range_coder.go` `normalize()` and `Bytes()` functions to match libopus byte ordering, if Step 1 reveals divergences
- **Dependencies**: Step 1 (test vectors identify specific failures)
- **Goal Impact**: Ensures future CELT/SILK integration (Milestones 2-3) produces interoperable packets
- **Acceptance**: All libopus-derived test vectors pass byte-for-byte; existing internal round-trip tests continue to pass
- **Validation**: `go test -v -run 'TestRangeCoder' ./... && go-stats-generator analyze . --skip-tests --format json --sections functions | jq '.functions[] | select(.name | test("normalize|Bytes")) | {name, cyclomatic: .complexity.cyclomatic}'`

### Step 3: Add Buffer Reuse to Decoder
- **Deliverable**: Add `rawBuffer []byte` field to `Decoder` struct in `decoder.go`; modify `decodeInternal` to reuse buffer when capacity sufficient; grow only when needed
- **Dependencies**: None
- **Goal Impact**: Reduces decoder allocations from 47,496 B/op toward encoder's 3,608 B/op benchmark; enables high-throughput real-time audio processing
- **Acceptance**: `BenchmarkDecode` shows ≤10,000 B/op (50%+ reduction) and ≤8 allocs/op
- **Validation**: `go test -bench=BenchmarkDecode -benchmem ./... | grep -E 'B/op|allocs/op'`

### Step 4: Add Optional Bounds to frameBuffer Ready Queue
- **Deliverable**: Add `maxQueueDepth int` field to `frameBuffer` struct in `frame.go`; modify `write()` to return error when queue full (if maxQueueDepth > 0); update `newFrameBuffer` to accept optional limit; default to 0 (unbounded) for backward compatibility
- **Dependencies**: None
- **Goal Impact**: Prevents unbounded memory growth in streaming scenarios with backpressure issues
- **Acceptance**: New test `TestFrameBufferQueueLimit` verifies behavior with 1000+ frames; existing tests pass unchanged
- **Validation**: `go test -v -run 'TestFrameBuffer' ./...`

### Step 5: Optimize Range Coder Buffer Pre-allocation
- **Deliverable**: Modify `RangeEncoder.normalize()` and `Bytes()` in `range_coder.go` to pre-allocate buffer capacity based on estimated encoded size, eliminating the 2 `append()` in loop anti-patterns flagged by go-stats-generator
- **Dependencies**: Step 2 (ensure changes don't break bit-exactness)
- **Goal Impact**: Reduces encoder allocations for future CELT/SILK integration where range coder is performance-critical
- **Acceptance**: `BenchmarkRangeEncoder` maintains 0 B/op; go-stats-generator no longer flags `range_coder.go:79` and `range_coder.go:89` as performance anti-patterns
- **Validation**: `go test -bench=BenchmarkRangeEncoder -benchmem ./... && go-stats-generator analyze . --skip-tests --format json --sections patterns | jq '.patterns.anti_patterns.performance_antipatterns | map(select(.file | contains("range_coder")))' | grep -c memory_allocation` should output 0

### Step 6: Add Error Context Wrapping to Decoder
- **Deliverable**: Wrap error returns in `Decode()` and `DecodeAlloc()` (decoder.go:79, decoder.go:122) with `fmt.Errorf("magnum: decode: %w", err)` for consistent error chain
- **Dependencies**: None
- **Goal Impact**: Improves debugging for users; aligns with encoder pattern (`fmt.Errorf("magnum: encode frame: %w", err)`)
- **Acceptance**: go-stats-generator no longer flags decoder.go:79 and decoder.go:122 as `bare_error_return` anti-patterns; error messages include context when tested
- **Validation**: `go-stats-generator analyze . --skip-tests --format json --sections patterns | jq '.patterns.anti_patterns.performance_antipatterns | map(select(.type == "bare_error_return" and (.file | contains("decoder"))))' | grep -c bare_error_return` should output 0

## Validation Summary

After all steps complete, run full validation suite:

```bash
# All tests pass
go test -v ./...

# Race detector clean
go test -race ./...

# Benchmarks show improved allocations
go test -bench=. -benchmem ./...

# No performance anti-patterns remaining
go-stats-generator analyze . --skip-tests --format json --sections patterns | jq '.patterns.anti_patterns.performance_antipatterns | length' # should be 0 or only acknowledged patterns

# Documentation coverage maintained
go-stats-generator analyze . --skip-tests --format json --sections documentation | jq '.documentation.coverage.overall' # should be 100
```

## Default Thresholds for Scope Assessment
| Metric | Small | Medium | Large | This Project |
|--------|-------|--------|-------|--------------|
| Functions above complexity 9.0 | <5 | 5–15 | >15 | 1 (decodeInternal: 12.2) ✅ Small |
| Duplication ratio | <3% | 3–10% | >10% | 0% ✅ Small |
| Doc coverage gap | <10% | 10–25% | >25% | 0% ✅ Small |

## Notes

- Steps 1-2 are blocking for ROADMAP Milestone 2 (CELT encoder) — they ensure the foundational range coder is bit-exact with libopus before building codec logic on top.
- Steps 3-6 are independent quality improvements that can be parallelized.
- All changes maintain zero external dependencies and CGO-free status per ROADMAP architecture notes.
- Frame code constants (`frameCodeTwoEqualFrames`, etc.) defined but rejected at decode are intentional placeholders for Milestone 5 — do not remove.
- The `directory_mismatch` warning in go-stats-generator is a false positive; package `magnum` in directory `magnum` is correct.

---

*Generated by go-stats-generator metrics analysis on 2026-03-23*
