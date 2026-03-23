# Implementation Plan: Complete ROADMAP Milestone 1 (Range Coder Bit-Exactness)

## Project Context
- **What it does**: A minimal, pure-Go Opus-compatible audio encoder following pion/opus API patterns, using flate compression as a placeholder for SILK/CELT codecs.
- **Current goal**: Verify range coder bit-exactness against libopus reference implementation (final uncompleted task of Milestone 1)
- **Estimated Scope**: Small

## Goal-Achievement Status
| Stated Goal | Current Status | This Plan Addresses |
|-------------|---------------|---------------------|
| Pure Go, zero CGO | ✅ Achieved | No |
| RFC 6716 TOC header compliance | ✅ Achieved | No |
| pion/opus API patterns | ⚠️ Partial (missing Application param) | Yes (Step 3) |
| Range coder implementation (M1) | ⚠️ Implemented but not bit-exact verified | Yes (Steps 1-2) |
| SetBitrate functional | ⚠️ Stored but unused (documented) | Yes (Step 4) |
| CELT codec (M2) | ❌ Not implemented | No (future milestone) |
| SILK codec (M3) | ❌ Not implemented | No (future milestone) |
| libopus interoperability | ❌ Blocked by M2-M7 | No (future milestone) |

## Metrics Summary
- Complexity hotspots on goal-critical paths: **0** functions above threshold (highest: `Decoder.Decode` at 9.6 overall)
- Duplication ratio: **0%**
- Doc coverage: **100%** (all exported symbols documented)
- Package coupling: Single-package architecture, no coupling issues

## Research Findings

### Competitive Landscape
- **pion/opus**: Most active pure-Go Opus project; currently offers SILK-only decoder, no CELT
- **thesyncim/gopus/celt**: Basic/incomplete CELT implementation
- **No complete pure-Go Opus codec exists** — magnum can differentiate by achieving full RFC 6716 compliance

### Range Coder Validation Resources
- Official test vectors: https://opus-codec.org/testvectors/
- Reference implementation: libopus `celt/entenc.c` and `celt/entdec.c`
- RFC 6716 Appendix contains bit-exact reference C code

### pion/opus API Conventions
- `NewEncoder(sampleRate, channels, application)` with Application constants (VoIP=2048, Audio=2049, LowDelay=2051)
- magnum's current `NewEncoder(sampleRate, channels)` deviates from this pattern

---

## Implementation Steps

### Step 1: Extract Range Coder Test Vectors from libopus
- **Deliverable**: New file `range_coder_vectors_test.go` containing test vectors extracted from libopus reference implementation
- **Dependencies**: None
- **Goal Impact**: Completes the final task of ROADMAP Milestone 1
- **Acceptance**: Test file compiles and defines at least 5 distinct test vectors covering:
  - Simple symbol sequences
  - Edge cases (single bit, maximum frequency)
  - Multi-symbol sequences with varying distributions
  - Raw bits encoding/decoding
  - LogP probability encoding/decoding
- **Validation**: `go build ./...`
- **Implementation Notes**:
  1. Create test vectors by running libopus `ec_enc`/`ec_dec` with known inputs and capturing output bytes
  2. Document the libopus version and exact commands used to generate vectors
  3. Include both encoded bytes and expected decoded symbols for round-trip verification

### Step 2: Verify Range Coder Bit-Exactness
- **Deliverable**: New test function `TestRangeCoderBitExact` in `range_coder_test.go` that compares magnum output against libopus vectors
- **Dependencies**: Step 1 (test vectors must exist)
- **Goal Impact**: Completes ROADMAP Milestone 1 success criteria: "output bytes match the reference implementation byte-for-byte"
- **Acceptance**: 
  - `TestRangeCoderBitExact` passes comparing magnum range coder output to libopus reference bytes
  - If divergences found, fix `range_coder.go` normalize/finalize logic until byte-exact
- **Validation**: `go test -v -run TestRangeCoderBitExact ./...`
- **Implementation Notes**:
  1. Encode symbols using `RangeEncoder`, compare `.Bytes()` output to reference
  2. Decode reference bytes using `RangeDecoder`, verify symbols match expected
  3. Any byte mismatch indicates RFC 6716 §4.1 divergence requiring fix
  4. Focus on normalize thresholds and finalization byte flushing

### Step 3: Add Application Parameter for pion/opus API Compatibility
- **Deliverable**: 
  - New type `Application int` with constants `ApplicationVoIP`, `ApplicationAudio`, `ApplicationLowDelay` in `encoder.go`
  - New constructor `NewEncoderWithApplication(sampleRate, channels int, app Application) (*Encoder, error)`
  - Update `NewEncoder` to call `NewEncoderWithApplication` with default `ApplicationAudio`
  - Store `app` in `Encoder` struct for future SILK/CELT mode selection
- **Dependencies**: None
- **Goal Impact**: Advances pion/opus API compatibility (GAPS.md item "No Application parameter")
- **Acceptance**: 
  - All three Application constants defined and exported
  - `NewEncoderWithApplication` validates parameters and stores application
  - `NewEncoder` behavior unchanged (backward compatible)
  - Documentation updated in README API table
- **Validation**: `go build ./... && go test ./...`

### Step 4: Document SetBitrate Limitation in API Table
- **Deliverable**: Update README.md API table entry for `SetBitrate` to clarify it is a placeholder
- **Dependencies**: None
- **Goal Impact**: Addresses GAPS.md item "SetBitrate Is Non-Functional" — users will have accurate expectations
- **Acceptance**: README API table entry for `SetBitrate` includes note: "stored for future CELT/SILK integration; currently unused"
- **Validation**: `grep -A2 "SetBitrate" README.md` shows updated description

### Step 5: Add Placeholder Methods for Complexity and Bandwidth Control
- **Deliverable**: 
  - `(*Encoder).SetComplexity(complexity int)` — stores value (clamped 0-10) for future use
  - `type Bandwidth int` with constants `BandwidthNarrowband`, `BandwidthMediumband`, `BandwidthWideband`, `BandwidthSuperwideband`, `BandwidthFullband`, `BandwidthAuto`
  - `(*Encoder).SetBandwidth(bandwidth Bandwidth)` — stores value for future use
  - Document both methods in README API table with placeholder notes
- **Dependencies**: Step 3 (should follow Application parameter pattern)
- **Goal Impact**: Addresses GAPS.md items "Missing Complexity Control" and "Missing Bandwidth Control"
- **Acceptance**: 
  - Methods compile and store values without error
  - Methods documented in README API table with notes indicating placeholder status
  - `Encoder` struct includes `complexity` and `bandwidth` fields
- **Validation**: `go build ./... && go test ./...`

### Step 6: Document Decoder Signature Divergence
- **Deliverable**: Update `NewDecoder` godoc comment to explicitly document the design choice of requiring configuration upfront (vs pion/opus parameterless constructor)
- **Dependencies**: None
- **Goal Impact**: Addresses GAPS.md item "pion/opus API Divergence: Decoder Signature"
- **Acceptance**: `go doc magnum.NewDecoder` shows clear explanation of design rationale
- **Validation**: `go doc github.com/opd-ai/magnum.NewDecoder`

---

## Scope Assessment Rationale

| Metric | Observed Value | Threshold Assessment |
|--------|----------------|---------------------|
| Functions above complexity 9.0 | 0 | Small (<5) |
| Duplication ratio | 0% | Small (<3%) |
| Doc coverage gap | 0% | Small (<10%) |

**Overall Assessment**: Small scope. The codebase is well-structured with low complexity and excellent documentation. The main technical challenge is Step 2 (range coder bit-exactness) which may require algorithmic fixes if divergences are found.

---

## Success Criteria for This Plan

1. ROADMAP Milestone 1 marked complete (range coder verified bit-exact)
2. pion/opus API compatibility improved (Application parameter, documented Decoder divergence)
3. GAPS.md items addressed: SetBitrate documentation, Complexity/Bandwidth placeholders
4. All existing tests continue to pass
5. No new HIGH or CRITICAL findings in future audit

---

## Next Milestone (Not In Scope)

After this plan, the next priority is **ROADMAP Milestone 2: CELT Encoder** for 48 kHz fullband audio. This is a Large scope effort requiring:
- MDCT implementation (2a)
- Band energy encoding (2b)
- PVQ spectral coding (2c)
- Frame assembly and bitrate control (2d-2e)
- Integration replacing flate compression (2f)

---

*Generated: 2026-03-23 | Based on go-stats-generator analysis and project documentation*
