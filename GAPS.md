# Implementation Gaps — 2026-03-22

This document identifies gaps between the project's stated goals and the current implementation. Gaps are ordered by impact on users.

---

## SetBitrate Has No Effect on Output

- **Stated Goal**: README line 94 states `SetBitrate(bitrate int)` — "Set target bitrate (bps, clamped to 6000–510000)."
- **Current State**: `encoder.go:58-71` stores the bitrate value with proper clamping, but `encodeFrame()` at `encoder.go:101-128` uses `flate.DefaultCompression` unconditionally. The stored bitrate is never read.
- **Impact**: Users who call `SetBitrate()` expecting smaller/larger packets will see no change in output size. This is documented in "Limitations" as "Bitrate hint only" but the API suggests active control.
- **Closing the Gap**: 
  1. Short-term: Add prominent doc comment on `SetBitrate` explaining it's a no-op placeholder.
  2. Long-term (Milestone 2e): Wire `e.bitrate` to CELT bit allocation tables once CELT is implemented.

---

## No Graceful End-of-Stream Handling

- **Stated Goal**: README line 39 states "Pass `nil` to drain any buffered frames without supplying new samples."
- **Current State**: Passing `nil` to `Encode()` only returns frames from the `ready` queue (`encoder.go:84-92`). Partial frames in `fb.samples` are silently discarded. The `flush()` method exists at `frame.go:83-91` but is never called.
- **Impact**: Audio streams that don't align to exact 20 ms boundaries will lose the final partial frame (up to 19.9 ms of audio).
- **Closing the Gap**:
  1. Add `(*Encoder).Flush() ([]byte, error)` that calls `fb.flush()` and encodes the zero-padded frame.
  2. Document that `Flush()` must be called at end-of-stream.
  3. Validate: `go test -run TestEncoderFlush ./...`

---

## Decode Ignores TOC Header Metadata

- **Stated Goal**: README line 95 states `Decode(packet []byte)` — "Decode a packet produced by Encode."
- **Current State**: `Decode()` at `encoder.go:139-174` reads the TOC byte but only uses it to skip to the payload. It does not extract configuration, stereo flag, or frame code. The decoder cannot validate packet structure.
- **Impact**: If a mono packet is accidentally decoded expecting stereo (or vice versa), the sample count will be wrong but no error is raised. Corrupted TOC bytes are not detected.
- **Closing the Gap**:
  1. Parse TOC using existing `tocHeader` methods (`bitstream.go:76-88`).
  2. Validate frame code is `frameCodeOneFrame` (only supported code).
  3. Optionally return stereo flag and config to caller for verification.
  4. Validate: `go test -run TestDecodeInvalidTOC ./...`

---

## Stereo Decode Cannot Verify Channel Count

- **Stated Goal**: README documents stereo support with interleaved samples (lines 56-73).
- **Current State**: `Decode()` returns raw `[]int16` without indicating whether the packet was mono or stereo. Callers must know the channel count a priori.
- **Impact**: If a stereo packet is interpreted as mono, samples will be misaligned (L/R interleaving treated as sequential mono). No runtime error occurs.
- **Closing the Gap**:
  1. Add `DecodeWithInfo(packet []byte) (samples []int16, stereo bool, err error)` variant.
  2. Or modify `Decode` to return `([]int16, bool, error)` where bool indicates stereo (breaking change).
  3. Validate: `go test -run TestDecodeReturnsChannelInfo ./...`

---

## Error Context Lost in Compression Failures

- **Stated Goal**: Errors are documented with sentinel values for `errors.Is` branching (README lines 99-105).
- **Current State**: Internal errors from `flate.NewWriter()`, `w.Write()`, and `w.Close()` at `encoder.go:116-124` are returned unwrapped. Callers cannot distinguish compression failures from other errors.
- **Impact**: Debugging is difficult when flate fails (e.g., write to closed buffer). Error messages lack "magnum:" prefix or operation context.
- **Closing the Gap**:
  1. Wrap errors with context: `fmt.Errorf("magnum: encode frame: %w", err)`.
  2. Optionally define `ErrCompressionFailed` sentinel if specific handling is needed.
  3. Validate: `go test -run TestEncodeErrorWrapping ./...`

---

## No Validation of Input Sample Count

- **Stated Goal**: README documents expected sample counts per frame (lines 76-87 table).
- **Current State**: `Encode()` accepts any `[]int16` slice and buffers it. Callers can pass slices that don't align to frame boundaries indefinitely, with no warning.
- **Impact**: Users may not realize their audio is being buffered rather than encoded. Silent accumulation can cause memory growth if Encode is called without consuming output.
- **Closing the Gap**:
  1. Optional: Add `Encode()` variant that errors if input doesn't match exact frame size.
  2. Document current buffering behavior more prominently in API docs.
  3. Add warning comment if buffer exceeds N frames (e.g., 10 frames = 200ms).

---

## Missing Decoder Type for pion/opus Symmetry

- **Stated Goal**: ROADMAP Milestone 6 describes a `Decoder` type mirroring pion/opus.
- **Current State**: Only a standalone `Decode(packet []byte)` function exists. No stateful `Decoder` struct.
- **Impact**: API asymmetry — users create an `Encoder` struct but call a `Decode` function. Cannot maintain decoder state for future PLC/FEC.
- **Closing the Gap**: 
  1. Add `type Decoder struct` with `NewDecoder(sampleRate, channels int) (*Decoder, error)`.
  2. Add `(*Decoder).Decode(packet []byte, out []int16) (int, error)` method.
  3. Validate: `go test -run TestDecoderRoundTrip ./...`

---

## No Benchmarks for Performance Validation

- **Stated Goal**: Implicit goal of usability for audio encoding workloads.
- **Current State**: No benchmark tests exist in `encoder_test.go`. Cannot measure encoding throughput or memory allocations.
- **Impact**: Cannot validate performance regressions or compare against alternatives. ROADMAP Milestone 7 mentions performance targets.
- **Closing the Gap**:
  1. Add `BenchmarkEncode48kMono`, `BenchmarkEncode48kStereo`, `BenchmarkDecode`.
  2. Measure ns/op and B/op for representative frame sizes.
  3. Validate: `go test -bench=. -benchmem ./...`

---

## Gap Summary by Priority

| Gap | Severity | Effort | ROADMAP Alignment |
|-----|----------|--------|-------------------|
| SetBitrate no-op | Medium | Low | Milestone 2e |
| No end-of-stream flush | Medium | Low | Not in roadmap |
| Decode ignores TOC | Medium | Low | Milestone 6 |
| Stereo decode ambiguity | Low | Low | Milestone 6 |
| Error context missing | Low | Low | General quality |
| No input validation | Low | Low | General quality |
| No Decoder type | Low | Medium | Milestone 6 |
| No benchmarks | Low | Medium | Milestone 7 |

---

*Generated against magnum v0.0.0 (unreleased)*
