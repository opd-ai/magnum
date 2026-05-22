# UNIVERSAL BUG AUDIT (END-TO-END) — 2026-05-22

## Project Profile

**Purpose**: Pure-Go Opus encoder/decoder (RFC 6716) implementing SILK (8/16 kHz), CELT (24/48 kHz), and Hybrid (24 kHz) codec paths with multi-frame packet support, VAD, DTX, LBRR, and PLC.

**Target Users**: Go developers embedding Opus audio encoding/decoding (e.g., WebRTC via pion/opus patterns).

**Deployment Model**: Library — imported as a Go package, no CLI, no network I/O.

**Critical Paths**:
1. Encoder: PCM → Opus packet (single-frame and multi-frame, mono and stereo)
2. Decoder: Opus packet → PCM (single-frame and multi-frame, mono and stereo)
3. Range coder: Arithmetic coding for SILK/CELT symbols
4. SILK frame: LPC, NLSF, pitch, excitation, gain coding
5. CELT frame: MDCT, PVQ, band energy, spreading

## Audit Scope

- **Packages audited**: 1 (`github.com/opd-ai/magnum`)
- **Files inspected**: 26 source files (all non-test `.go` files)
- **Total functions**: 405 (143 functions + 262 methods)
- **Total lines (source)**: 5,348 (excl. tests)
- **Go version**: 1.24.0
- **Dependencies**: None (pure stdlib)

## Coverage Log

| Package | 3b Logic | 3c Nil | 3d Errors | 3e Resources | 3f Concurrency | 3g Security | 3h Aliasing | 3i Init | 3j API | 3k Perf |
|---------|----------|--------|-----------|--------------|----------------|-------------|-------------|---------|--------|---------|
| magnum  | ✅       | ✅     | ✅        | ✅           | ✅             | ✅          | ✅          | ✅      | ✅     | ✅      |

## Goal-Achievement Summary

| Stated Goal | Status | Blocking Findings |
|-------------|--------|-------------------|
| RFC 6716-compliant SILK (8/16 kHz) encoding | ⚠️ | F-5 (pitch always fails), F-12 (LBRR desync), F-13 (gain decode mismatch), F-14 (excitation position truncation) |
| RFC 6716-compliant CELT (24/48 kHz) encoding | ⚠️ | F-8 (encode/decode bit order mismatch), F-9 (PVQ truncation), F-10 (Bytes() corruption) |
| Hybrid mode (24 kHz) | ⚠️ | F-18 (stereo broken), F-19 (reset incomplete) |
| Stereo encoding/decoding | ❌ | F-3 (stereo CELT payload split), F-4 (multi-frame stereo → mono), F-6 (no delimiter in stereo payloads), F-7 (stereo multi-frame collapse), F-17 (stereo PLC half-length) |
| Multi-frame packets (codes 1,2,3) | ⚠️ | F-2 (DecodeAlloc rejects multi-frame), F-8b (code-3 VBR layout wrong) |
| Range coder (§4.1) | ⚠️ | F-10 (Bytes() not idempotent) |
| PLC (packet loss concealment) | ⚠️ | F-1 (DecodeAlloc never primes PLC), F-16 (SILK decode never updates PLC) |
| VAD, DTX, LBRR | ⚠️ | F-12 (LBRR presence bit never consumed in decode) |
| Packets validated against libopus | ✅ | (mono single-frame paths work) |

## Findings

### CRITICAL

- [x] **F-8: CELT encode/decode bit order mismatch** — `celt_frame.go:157-160,370-390` — Logic — Encoder writes PVQ data before fine energy bits; decoder reads fine energy before PVQ. Non-silence CELT frames are parsed with shifted bit positions, producing garbage output. **Remediation**: Align encoder write order to match decoder read order (fine energy first, then PVQ). Validate: `go test -race -run TestCELT ./...`

- [x] **F-10: RangeEncoder.Bytes() mutates state on every call** — `range_coder.go:86-92` — API contract — Each call to `Bytes()` appends 4 flush bytes to the internal buffer. Multiple calls (which happen in `celt_frame.go:223,522` and `silk_frame.go:189`) corrupt the encoded stream and produce invalid packets. **Remediation**: Add a `finalized bool` guard; only flush once. Validate: `go test -race -run TestRangeEncoder ./...`

- [x] **F-3: Stereo CELT single-frame decode assumes equal channel sizes** — `decoder.go:527-545` — Logic — Stereo CELT single-frame decode blindly splits the payload in half. Per-channel CELT payloads are variable-length; unequal sizes desynchronize both channel decoders, corrupting output. **Remediation**: Encode an explicit channel-split length prefix or use M/S coding per RFC 6716 §4.3.1. Validate: encode stereo CELT with unequal-energy channels and verify round-trip.

### HIGH

- [x] **F-1: DecodeAlloc never updates PLC state** — `decoder.go:808-827` — Logic — `DecodeAlloc` decodes successfully but never calls `updatePLCState`. Subsequent `DecodePLC` uses stale/empty history and outputs silence instead of concealment. **Remediation**: Call `updatePLCState(out)` before returning from `DecodeAlloc`. Validate: `go test -race -run TestPLC ./...`

- [x] **F-2: DecodeAlloc rejects multi-frame CELT packets** — `decoder.go:831-848` — Logic — `DecodeAlloc` only handles single-frame code-0 CELT packets; valid code-1/2/3 packets are rejected. **Remediation**: Route multi-frame CELT through `decodeCELTArbitraryFrames` in `DecodeAlloc`. Validate: encode multi-frame CELT, decode with `DecodeAlloc`.

- [ ] **F-4: Multi-frame CELT stereo decodes as mono-duplicated** — `decoder.go:595-610,613-737` — Logic — Multi-frame CELT decode paths (`decodeCELTArbitraryFrames`) decode each frame as mono and duplicate samples into L/R. Stereo channel separation is lost. **Remediation**: Implement per-channel decode for stereo multi-frame paths. Validate: round-trip stereo multi-frame CELT.

- [x] **F-5: Pitch estimation always returns nil for valid SILK frames** — `pitch.go:109-113` — Logic — `maxLag*2` at 16 kHz = 576 > 320 (20ms frame); at 8 kHz = 288 > 160. The guard rejects every valid 20ms SILK frame, so pitch estimation is never performed, disabling voiced/LTP paths. **Remediation**: Change threshold to `maxLag + minLag` or use overlapping analysis with buffered history. Validate: `go test -race -run TestPitch ./...`

- [x] **F-6: Stereo SILK/CELT single-frame packets lack channel delimiter** — `encoder.go:621-625,693-696` — Logic/Framing — Stereo single-frame packets concatenate two variable-length channel payloads with no length prefix. The decoder cannot determine where channel 1 ends and channel 2 begins except by assuming equal lengths (which is wrong for CELT). **Remediation**: Prepend a 2-byte length for channel 1 payload, or use joint stereo coding. Validate: encode stereo with asymmetric content, verify decode.

- [ ] **F-7: Multi-frame stereo encodes collapse to mono** — `encoder.go:748-752,823-833,903-949` — Logic/Data loss — `EncodeTwoFrames`/`EncodeMultipleFrames` route stereo through `encodeSILKPayload`/`encodeCELTPayload`, which average L+R to mono. Anti-phase stereo content collapses to silence. **Remediation**: Encode channels independently or use joint stereo. Validate: encode anti-phase stereo, verify non-silence output.

- [ ] **F-8b: Code-3 VBR CELT multi-frame layout incorrect** — `encoder.go:841-873` — Logic — Code-3 CELT packets interleave `[len][frame][len][frame]...` but RFC 6716 §3.2.5 specifies all M-1 lengths first, then all frame data contiguously. Decode silently misparses with >2 varying-size frames. **Remediation**: Write all lengths first, then all frame payloads. Validate: encode 3+ VBR CELT frames, decode with `opusdec`.

- [ ] **F-9: PVQ index/K truncation for large bands** — `celt_frame.go:260-272,417-428` — Logic/Overflow — `k` is encoded in 5 bits (max 31) but `SelectK()` returns up to 128; PVQ indices are stored as `uint32` but can exceed 32 bits for large K values. Large-band PVQ codewords cannot round-trip. **Remediation**: Use variable-length K encoding and big-integer PVQ indices per RFC 6716 §5.3.4. Validate: encode high-energy fullband frames, verify round-trip.

- [ ] **F-11: Hybrid TOC never emitted** — `bitstream.go:193-220` — Logic — `configForSampleRateAndDuration()` maps 24/48 kHz exclusively to CELT configurations; hybrid configs (12/13/16/17) are unreachable. Hybrid-mode packets get CELT TOC headers, which confuse decoders expecting hybrid. **Remediation**: Add hybrid config selection when `encoder.hybrid` is active. Validate: encode hybrid, verify TOC byte matches expected config.

- [ ] **F-12: SILK decode never consumes LBRR presence bit** — `silk_frame.go:158,341-364,536-545` — Logic — Encoder writes LBRR flag/length/data when enabled, but decoder never reads even the presence bit. All subsequent field reads are shifted by the LBRR overhead, corrupting the decoded frame. **Remediation**: Decoder must read and skip (or use) LBRR data before proceeding. Validate: enable LBRR, encode, decode, verify output.

- [ ] **F-13: Gain encode/decode mismatch** — `silk_frame.go:419-429,572-577` — Logic — Encoder writes delta-coded gain indices via `GainCoder`; decoder treats each 6-bit value as an absolute gain via `DequantizeGain`. Decoded gains bear no relation to encoded values. **Remediation**: Decoder must apply same delta decoding as encoder. Validate: encode known-gain frame, verify decoded gain matches.

- [ ] **F-14: Excitation position truncated to 5 bits** — `silk_frame.go:289-296,611` — Logic — Excitation pulse positions are forced into 5 bits (0..31), but 20ms SILK subframes are 40 samples (8 kHz) or 80 samples (16 kHz). Pulses at positions >31 are silently moved to wrong locations. **Remediation**: Use ceil(log2(subframeLength)) bits for positions. Validate: encode frame with energy in latter half, verify reconstruction.

- [ ] **F-15: SILK voiced synthesis uses stale LTP history** — `silk_frame.go:649-656` — Logic — Voiced synthesis always reads LTP history from `dec.prevSamples` even when lagged samples are within the current frame. First subframe of a newly-voiced frame uses incorrect history. **Remediation**: Extend history buffer to include current-frame samples for intra-frame LTP. Validate: encode voiced transition, verify smooth reconstruction.

- [ ] **F-16: SILK decoder never updates PLC state** — `silk_frame.go:585-590,680-682` — Logic — Successful SILK decodes never update `dec.plc`; calling `DecodeFrame(nil)` for PLC has no history and returns silence. **Remediation**: Update PLC state from decoded output at end of `DecodeFrame`. Validate: decode SILK frame, then decode nil, verify concealment output.

- [ ] **F-17: Stereo PLC returns half-length output** — `plc.go:106,148,189,244` — API contract — PLC state is allocated as mono (`prevSamples: frameSize`) regardless of channel count. Voiced/unvoiced concealment returns `frameSize` samples instead of `frameSize*channels`. **Remediation**: Multiply allocation and output length by `channels`. Validate: create stereo decoder, trigger PLC, verify output length.

- [ ] **F-18: Hybrid mode broken for stereo** — `hybrid.go:69-71,74,135,384` — API contract — Stereo is accepted at construction but frame sizing, band-split, and combine operations are all mono-only. Encoder expects 480 samples instead of 960; decoder returns mono length. **Remediation**: Scale all frame sizes by channel count in hybrid mode. Validate: encode/decode 24 kHz stereo hybrid, verify output length.

- [ ] **F-19: EnableCELT/EnableSILK ignore SetFrameDuration** — `encoder.go:187-193,238-244` — Logic — These methods hardcode 20ms frame duration instead of using `e.frameDuration`. If `SetFrameDuration` was called first, the codec backend has the wrong frame size. **Remediation**: Use `e.frameDuration` (or `e.frameDurationMs`) when constructing codec instances. Validate: `SetFrameDuration(40); EnableSILK(); Encode(...)` should not panic.

- [ ] **F-20: SetFrameDuration doesn't update stereo right-channel codecs** — `encoder.go:377-386` — Logic — `SetFrameDuration` updates `celtEncoder`/`silkEncoder` config but not `celtEncoderR`/`silkEncoderR`. Stereo channel 2 retains old frame size and produces mismatched packets. **Remediation**: Also update `*EncoderR` instances. Validate: stereo encode after `SetFrameDuration` change.

- [x] **F-21: encodeFrameLength allows overlength payloads** — `encoder.go:985-997` — Arithmetic — No rejection for lengths >1275 (Opus maximum per RFC 6716 §3.2.1). Large payloads wrap in the 2-byte encoding, producing malformed packets. **Remediation**: Return error or clamp at 1275. Validate: attempt encode of >1275-byte payload, verify error.

- [x] **F-22: NLSF LPCToNLSF corrupts coefficients in-place** — `nlsf.go:62-65` — Logic — P/Q polynomial construction reuses already-mutated LPC coefficients, producing incorrect NLSF values. **Remediation**: Copy LPC coefficients before building P/Q polynomials. Validate: round-trip LPC→NLSF→LPC, verify reconstruction error < 1e-6.

- [x] **F-23: BandEnd panics for band==NumCELTBands** — `band_energy.go:135-139` — Boundary safety — Guard `band > NumCELTBands` allows `band == 21`; accessing `celtBands[22]` on a 22-element array causes index-out-of-range panic. **Remediation**: Change guard to `band >= NumCELTBands`. Validate: `BandEnd(21)` should return -1.

- [ ] **F-24: CELT decoder returns half-length output** — `celt_frame.go:455-461` — API contract — `DecodeFrame` returns `FrameSize/2` samples instead of `FrameSize`. A 20ms 48 kHz frame (960 samples) decodes to only 480 samples. **Remediation**: Return full `FrameSize` samples from MDCT synthesis. Validate: decode CELT frame, verify `len(output) == frameSize`.

- [ ] **F-25: SetFrameDuration panics with active SILK on 40/60ms** — `encoder.go:377-386` — Logic/Boundary — `SetFrameDuration` mutates `config.FrameSize` but doesn't rebuild SILK internals. SILK subframe logic may access out-of-bounds on non-20ms frames. **Remediation**: Rebuild SILK encoder state when frame duration changes. Validate: `EnableSILK(); SetFrameDuration(40); Encode(...)`.

### MEDIUM

- [x] **F-26: Decoder struct not safe for concurrent use** — `decoder.go:99-121` — Concurrency — `Decoder` reuses mutable internal buffers, readers, and PLC/CELT state with no synchronization. Concurrent calls to `Decode` race on shared state. **Remediation**: Document that `Decoder` is not goroutine-safe, or add a mutex. Validate: `go test -race` with concurrent decode test.

- [ ] **F-27: Spreading TF decision ignored for bands >0** — `spreading.go:171-172,198-199` — Logic — `ApplyTFChange`/`InvertTFChange` always read `tf.TF[0]`; bands 1..N use band-0's TF decision instead of their own. **Remediation**: Index `tf.TF[band]` per band. Validate: encode frame with varying TF, verify per-band application.

- [x] **F-28: PVQ Encode mutates caller's input spectrum** — `pvq.go:172-188` — Data aliasing — `allocatePulses()` normalizes the input `x` slice in place. Callers who retain a reference to the spectrum see corrupted values after encoding. **Remediation**: Copy `x` before normalization. Validate: encode PVQ, verify original slice unchanged.

- [ ] **F-29: Code-3 flate decode ignores padding flag** — `decoder.go:1010-1023` — Logic — Arbitrary-frame flate path reads the M byte but ignores padding flag/length bytes per RFC 6716 §3.2.5. Padded code-3 packets are misparsed. **Remediation**: Parse and skip padding bytes before frame data. Validate: encode padded code-3 packet, decode successfully.

- [x] **F-30: Hybrid Reset() doesn't reset CELT sub-state** — `hybrid.go:235-243,431-440` — API contract — `Reset()` clears SILK state but not CELT encoder/decoder overlap/history. Post-reset frames may contain stale CELT state. **Remediation**: Also reset CELT encoder/decoder state in hybrid `Reset()`. Validate: encode, reset, encode silence, verify no residual audio.

- [ ] **F-31: SILK encoder Reset() incomplete** — `silk_frame.go:324-337` — API contract — `Reset()` does not reset `gainCoder`, `pitchEstimate`, `ltpAnalyzer`, or `excEncoder`. Post-reset encoding depends on previous stream's state. **Remediation**: Reset all sub-components. Validate: encode, reset, encode new content, verify no cross-stream artifacts.

- [ ] **F-32: CELT stereo accepted but operates mono-only** — `celt_frame.go:63-65,127-129,455-461` — API contract — `NewCELTEncoder`/`NewCELTDecoder` accept `Channels==2` but all encode/decode operations process `FrameSize` (mono length) samples only. **Remediation**: Either reject channels>1 or implement per-channel processing. Validate: stereo CELT encode/decode produces correct sample count.

- [ ] **F-33: SILK stereo accepted but operates mono-only** — `silk_frame.go:89-90,140-143,630` — API contract — Same issue as F-32 for SILK codec. Stereo is accepted but all operations are mono. **Remediation**: Either reject channels>1 at construction or implement stereo. Validate: stereo SILK round-trip.

- [ ] **F-34: CELT encoder TF always non-transient** — `celt_frame.go:209-211,364-365` — Logic — Encoder always writes TF bits as non-transient (`false`), but decoder branches on `isTransient` for TF decoding. If a packet claims transient (which never happens from this encoder, but could from external packets), TF decode uses wrong table. **Remediation**: Implement transient detection or document limitation. Validate: N/A (encoder-only limitation).

### LOW

- [x] **F-35: ApplyHammingWindow divides by zero for length-1 input** — `lpc.go:255-265` — Boundary — `n-1` denominator produces `NaN` for single-sample input. Unlikely in practice (minimum SILK frame is 160 samples). **Remediation**: Guard `if n <= 1 { return copy }`. Validate: `ApplyHammingWindow([]float64{1.0})`.

- [x] **F-36: Multi-frame flate decode allocates fresh flate reader per frame** — `decoder.go:1121-1133,1147-1158` — Performance — Ignores the reusable `flateR` field, creating unnecessary allocations on the decode hot path. **Remediation**: Reuse `d.flateR` with `Reset()`. Validate: benchmark before/after.

- [ ] **F-37: 610 magic numbers throughout codebase** — various files — Maintainability — Numeric constants from RFC 6716 tables are used inline without named constants. Risk of transcription errors and maintenance burden. **Remediation**: Extract frequently-used values to named constants with RFC section references. Validate: `go build ./...`

## Metrics Snapshot

| Metric | Value |
|--------|-------|
| Total functions | 405 |
| Functions above complexity 15 | 10 |
| Avg cyclomatic complexity | 5.1 |
| Doc coverage | 95.0% |
| Duplication ratio | 0.95% |
| Test pass rate | all pass |
| go vet warnings | 0 |
| Race detector | clean |

## False Positives Considered and Rejected

| Candidate | Reason Rejected |
|-----------|----------------|
| Decoder concurrent use panic | Not a bug per se — Go standard library decoders (e.g., `json.Decoder`) are also not goroutine-safe. Reported as MEDIUM documentation issue instead. |
| `math/rand` usage | Not used for security purposes; codec is deterministic signal processing. No security concern. |
| Missing `defer Close()` patterns | No file I/O, network, or database operations in this library. All data is in-memory `[]byte`. |
| Global `celtBands` array modification | Array is declared `var` not `const`, but no code modifies it. Not a real race. |
| `init()` ordering | Single package with no `init()` functions. Not applicable. |
| Slice aliasing in MDCT overlap-add | Overlap buffer is explicitly managed and never exposed to callers. Safe by design. |
| `InsecureSkipVerify` / TLS issues | No network code in this library. Not applicable. |
| SQL injection | No database code. Not applicable. |
| Path traversal | No file system operations beyond test fixtures. Not applicable. |

## Remaining Scope

All packages and files have been audited. No remaining scope.
