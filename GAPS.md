# Implementation Gaps — 2026-05-22

## Stereo Encoding/Decoding Is Non-Functional

- **Stated Goal**: README documents stereo (2-channel) support with interleaved L/R samples at all sample rates, including usage examples and frame size tables.
- **Current State**: Stereo is accepted at construction time but never properly implemented. The encoder averages L+R to mono for multi-frame paths, concatenates channel payloads without delimiters for single-frame paths, and the decoder either splits payloads in half (incorrect for variable-length codecs) or duplicates mono output to both channels. PLC, CELT, SILK, and Hybrid sub-codecs all operate on mono-length buffers regardless of channel count.
- **Impact**: Any user following the stereo usage example in the README will get corrupted output. Anti-phase stereo content may produce silence. Decoded stereo output is either mono-duplicated or garbage.
- **Closing the Gap**: Implement proper stereo coding — either independent per-channel encoding with a length prefix, or mid/side (M/S) joint stereo per RFC 6716 §4.3.1. Update all internal frame-size calculations to account for channel count. Add stereo-specific test cases that verify channel separation.

## Pitch Prediction Is Effectively Disabled

- **Stated Goal**: README claims "SILK codec — LPC, NLSF, pitch prediction, excitation coding" as a core feature.
- **Current State**: `PitchEstimator.Estimate()` requires `n >= maxLag*2` samples (576 at 16 kHz, 288 at 8 kHz), but 20ms SILK frames provide only 320/160 samples. The guard always rejects, returning `nil`. The encoder falls back to unvoiced excitation coding for all frames, producing lower quality for speech content.
- **Impact**: Voiced speech (the majority of typical speech content) is encoded without pitch prediction or LTP, significantly degrading quality below what RFC 6716 SILK achieves. The pitch-related code exists but is dead code in practice.
- **Closing the Gap**: Either buffer multiple frames for pitch analysis (standard approach: use 5ms look-ahead + previous frame history), or reduce `maxLag` threshold to allow single-frame analysis with reduced pitch range. Validate with a voiced speech sample showing non-zero pitch lag in encoded output.

## LBRR (Low Bit-Rate Redundancy / FEC) Encode-Decode Mismatch

- **Stated Goal**: README lists "LBRR" as a feature for forward error correction.
- **Current State**: The encoder writes LBRR presence bits and redundancy data when enabled. The decoder never reads the LBRR presence bit, causing all subsequent bitstream fields to be shifted by the LBRR overhead. This means LBRR-enabled packets are decoded incorrectly, and the redundancy data cannot be used for error recovery.
- **Impact**: Enabling LBRR on the encoder corrupts all decode output. Users cannot benefit from FEC even in lossy network conditions.
- **Closing the Gap**: Implement LBRR parsing in the SILK frame decoder: read the presence bit, optionally decode the redundancy frame, and advance the bitstream position correctly before reading the main frame data.

## Hybrid Mode Incomplete for Stereo

- **Stated Goal**: README claims "Hybrid mode (24 kHz) — SILK + CELT band-splitting via Butterworth filters" with stereo support implied by the 24 kHz stereo entry in the frame size table.
- **Current State**: Hybrid mode accepts stereo at construction but all internal operations (band splitting, frame sizing, combine) use mono-length buffers. Encoding a 24 kHz stereo frame expects 480 samples instead of the documented 960.
- **Impact**: Users following the frame size table for 24 kHz stereo (960 samples) will get errors or truncated encoding.
- **Closing the Gap**: Scale all hybrid internal frame sizes by channel count. Implement per-channel band-split and combine. Add test case for 24 kHz stereo hybrid round-trip.

## PLC (Packet Loss Concealment) State Never Primed

- **Stated Goal**: README lists "PLC" as a feature for graceful handling of packet loss.
- **Current State**: Neither `DecodeAlloc` nor the SILK frame decoder update PLC state after successful decodes. When packet loss occurs and PLC is invoked, it has no history and outputs silence (or near-silence from uninitialized buffers).
- **Impact**: Packet loss produces audible clicks/silence instead of smooth concealment, defeating the purpose of PLC entirely.
- **Closing the Gap**: Update PLC state (previous samples, pitch, voicing) at the end of every successful decode in both the top-level `Decoder` and the SILK frame decoder. Add a test that decodes a sequence with a dropped packet and verifies non-zero concealment output.

## Multi-Frame Packet Layout Non-Conformant

- **Stated Goal**: README claims "Multi-frame packets — frame codes 1, 2, and 3 (1–48 frames per packet)" following RFC 6716.
- **Current State**: Code-3 VBR CELT packets use an interleaved `[len][frame][len][frame]...` layout instead of the RFC-specified "all M-1 lengths first, then all frame data" layout (§3.2.5). Additionally, frame lengths >1275 bytes are not rejected, violating §3.2.1.
- **Impact**: Multi-frame VBR packets with >2 frames of varying size are malformed and will be rejected by compliant decoders (e.g., libopus). Interoperability claim is limited to single-frame and CBR multi-frame packets.
- **Closing the Gap**: Rewrite code-3 VBR packet assembly to emit all frame lengths first, then frame payloads contiguously. Add a 1275-byte maximum check on individual frame lengths. Validate with `opusdec` on 3+ frame VBR packets.

## Range Encoder Produces Corrupt Output on Multiple Reads

- **Stated Goal**: Range coder implements "RFC 6716 §4.1" arithmetic coding.
- **Current State**: `RangeEncoder.Bytes()` appends 4 flush bytes to the internal buffer on every call without guarding against repeated invocation. The CELT and SILK frame encoders call `Bytes()` multiple times (once for bit counting, once for final output), producing a corrupted/enlarged stream on the second call.
- **Impact**: Encoded CELT/SILK payloads contain extra bytes that shift all subsequent data, producing decode failures or corrupted audio.
- **Closing the Gap**: Add a `finalized` flag to `RangeEncoder`; only flush on the first `Bytes()` call. Alternatively, separate `Len()` (for bit counting) from `Bytes()` (for finalization). Add a test verifying `Bytes()` is idempotent.

## Gain Coding Asymmetry Between Encoder and Decoder

- **Stated Goal**: SILK codec implements gain quantization as part of the encoding pipeline.
- **Current State**: The encoder writes delta-coded gain indices (difference from previous subframe's gain), but the decoder interprets each 6-bit value as an absolute gain index via `DequantizeGain`. The decoded gain values have no relation to the encoded values.
- **Impact**: SILK frame amplitudes are incorrect, producing distorted or excessively loud/quiet audio.
- **Closing the Gap**: Implement delta gain decoding in the SILK frame decoder to match the encoder's delta coding scheme. Add a round-trip test verifying that encoded gain values decode to matching quantized gains.
