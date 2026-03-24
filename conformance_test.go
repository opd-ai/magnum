package magnum

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestConformance tests the decoder against official RFC 6716 test vectors.
// The test vectors are located in testdata/opus_testvectors/ and contain
// .bit files (encoded packets) and .dec files (reference decoded output).
//
// Note: This is a validation test that checks whether the magnum decoder
// can correctly parse and decode packets from the official test vectors.
// Full bitstream conformance requires the CELT/SILK decoders to produce
// bit-exact output matching the reference decoder, which is tracked as
// a separate milestone.
func TestConformance(t *testing.T) {
	vectorDir := filepath.Join("testdata", "opus_testvectors")
	if _, err := os.Stat(vectorDir); os.IsNotExist(err) {
		t.Skip("Test vectors not found; run: cd testdata && curl -LO https://opus-codec.org/static/testvectors/opus_testvectors.tar.gz && tar xzf opus_testvectors.tar.gz")
	}

	// Test vectors 01-12 cover mono and stereo content
	testVectors := []struct {
		name     string
		file     string
		channels int
	}{
		{"testvector01", "testvector01.bit", 2}, // stereo
		{"testvector02", "testvector02.bit", 2},
		{"testvector03", "testvector03.bit", 2},
		{"testvector04", "testvector04.bit", 2},
		{"testvector05", "testvector05.bit", 2},
		{"testvector06", "testvector06.bit", 2},
		{"testvector07", "testvector07.bit", 2},
		{"testvector08", "testvector08.bit", 2},
		{"testvector09", "testvector09.bit", 2},
		{"testvector10", "testvector10.bit", 2},
		{"testvector11", "testvector11.bit", 2},
		{"testvector12", "testvector12.bit", 2},
	}

	for _, tv := range testVectors {
		t.Run(tv.name, func(t *testing.T) {
			bitPath := filepath.Join(vectorDir, tv.file)
			packets, err := readOpusTestVectorPackets(bitPath)
			if err != nil {
				t.Fatalf("Failed to read test vector %s: %v", tv.file, err)
			}

			if len(packets) == 0 {
				t.Fatalf("No packets found in %s", tv.file)
			}

			// Log summary
			t.Logf("Loaded %d packets from %s", len(packets), tv.file)

			// Verify we can parse all packets
			parsedCount := 0
			errorCount := 0
			for i, pkt := range packets {
				if len(pkt.data) == 0 {
					continue // Skip empty packets (silence)
				}
				info, err := ParseTOCByte(pkt.data[0])
				if err != nil {
					if i < 5 {
						t.Logf("Packet %d: parse error: %v", i, err)
					}
					errorCount++
					continue
				}
				parsedCount++
				if i < 3 {
					t.Logf("Packet %d: config=%d (%s), stereo=%v, code=%d, len=%d",
						i, info.Configuration, configName(info.Configuration),
						info.Stereo, info.FrameCode, len(pkt.data))
				}
			}
			t.Logf("Successfully parsed %d/%d packets (%d errors)",
				parsedCount, len(packets), errorCount)

			if parsedCount == 0 {
				t.Errorf("Failed to parse any packets")
			}
		})
	}
}

// TestConformanceDecodePackets verifies that the magnum decoder can decode
// packets from the official test vectors. This test focuses on packet
// parsing and basic decode functionality.
func TestConformanceDecodePackets(t *testing.T) {
	vectorDir := filepath.Join("testdata", "opus_testvectors")
	if _, err := os.Stat(vectorDir); os.IsNotExist(err) {
		t.Skip("Test vectors not found")
	}

	// Use testvector01 as the primary test case
	bitPath := filepath.Join(vectorDir, "testvector01.bit")
	packets, err := readOpusTestVectorPackets(bitPath)
	if err != nil {
		t.Fatalf("Failed to read test vector: %v", err)
	}

	// Create decoder at 48 kHz stereo (testvector01 uses CELT fullband stereo)
	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Enable CELT for RFC 6716 compliant decoding
	if err := dec.EnableCELT(); err != nil {
		t.Fatalf("Failed to enable CELT: %v", err)
	}

	// Try to decode first few non-empty packets
	decodedCount := 0
	for i, pkt := range packets {
		if len(pkt.data) == 0 {
			continue
		}

		// Parse TOC to understand packet structure
		if len(pkt.data) < 1 {
			continue
		}
		info, err := ParseTOCByte(pkt.data[0])
		if err != nil {
			continue
		}

		// For this test, we focus on single-frame CELT packets
		if info.FrameCode != 0 && info.FrameCode != 1 {
			// Multi-frame packets (code 2/3) require more complex handling
			continue
		}

		// Attempt decode
		out := make([]int16, 1920) // 20ms stereo @ 48kHz
		_, decErr := dec.Decode(pkt.data, out)
		if decErr != nil {
			if i < 10 {
				t.Logf("Packet %d decode: %v (config=%d, code=%d)",
					i, decErr, info.Configuration, info.FrameCode)
			}
			continue
		}
		decodedCount++
		if decodedCount >= 10 {
			break // Sample first 10 successful decodes
		}
	}

	t.Logf("Successfully decoded %d packets from testvector01", decodedCount)
}

// opusTestPacket represents a single packet from an Opus test vector file.
type opusTestPacket struct {
	length     uint32 // Packet length in bytes
	finalRange uint32 // Encoder final range state (for verification)
	data       []byte // Raw packet data
}

// readOpusTestVectorPackets reads packets from an opus_demo format .bit file.
// The format is: [4-byte BE length][4-byte BE final_range][packet data]...
func readOpusTestVectorPackets(path string) ([]opusTestPacket, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var packets []opusTestPacket
	header := make([]byte, 8)

	for {
		// Read 4-byte big-endian length
		_, err := io.ReadFull(f, header[:4])
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		length := binary.BigEndian.Uint32(header[:4])

		// Read 4-byte big-endian final_range
		_, err = io.ReadFull(f, header[4:8])
		if err != nil {
			return nil, err
		}
		finalRange := binary.BigEndian.Uint32(header[4:8])

		// Read packet data
		data := make([]byte, length)
		if length > 0 {
			_, err = io.ReadFull(f, data)
			if err != nil {
				return nil, err
			}
		}

		packets = append(packets, opusTestPacket{
			length:     length,
			finalRange: finalRange,
			data:       data,
		})
	}

	return packets, nil
}

// TOCInfo contains parsed information from an Opus TOC byte.
type TOCInfo struct {
	Configuration int  // Opus configuration (0-31)
	Stereo        bool // True if stereo
	FrameCode     int  // Frame count code (0-3)
}

// ParseTOCByte parses an Opus Table of Contents byte per RFC 6716 §3.1.
func ParseTOCByte(toc byte) (TOCInfo, error) {
	config := int(toc >> 3)      // bits 7-3
	stereo := (toc>>2)&1 == 1    // bit 2
	frameCode := int(toc & 0x03) // bits 1-0
	return TOCInfo{
		Configuration: config,
		Stereo:        stereo,
		FrameCode:     frameCode,
	}, nil
}

// configName returns a human-readable name for an Opus configuration.
func configName(config int) string {
	switch {
	case config <= 3:
		return "SILK NB"
	case config <= 7:
		return "SILK MB"
	case config <= 11:
		return "SILK WB"
	case config <= 13:
		return "Hybrid SWB"
	case config <= 15:
		return "Hybrid FB"
	case config <= 19:
		return "CELT NB"
	case config <= 23:
		return "CELT WB"
	case config <= 27:
		return "CELT SWB"
	default:
		return "CELT FB"
	}
}

// TestConformanceBitExact tests decoder output against reference .dec files.
// This implements ROADMAP item 2.1: extend TestConformance to decode each
// packet and compare output against the corresponding .dec reference PCM.
//
// The test tracks RMS error and max sample difference per codec path to
// identify which paths have the largest deviations from reference output.
// This is a measurement test that documents the current state of conformance.
func TestConformanceBitExact(t *testing.T) {
	vectorDir := filepath.Join("testdata", "opus_testvectors")
	if _, err := os.Stat(vectorDir); os.IsNotExist(err) {
		t.Skip("Test vectors not found; run: cd testdata && curl -LO https://opus-codec.org/static/testvectors/opus_testvectors.tar.gz && tar xzf opus_testvectors.tar.gz")
	}

	// Focus on testvector01 (48 kHz stereo CELT) as primary validation case.
	// All test vectors use 48 kHz stereo in the reference .dec files.
	testVectors := []struct {
		name     string
		bitFile  string
		decFile  string
		channels int
	}{
		{"testvector01", "testvector01.bit", "testvector01.dec", 2},
		{"testvector02", "testvector02.bit", "testvector02.dec", 2},
		{"testvector03", "testvector03.bit", "testvector03.dec", 2},
	}

	for _, tv := range testVectors {
		t.Run(tv.name, func(t *testing.T) {
			bitPath := filepath.Join(vectorDir, tv.bitFile)
			decPath := filepath.Join(vectorDir, tv.decFile)

			// Load encoded packets
			packets, err := readOpusTestVectorPackets(bitPath)
			if err != nil {
				t.Fatalf("Failed to read test vector: %v", err)
			}

			// Load reference decoded PCM (little-endian int16 stereo)
			refPCM, err := readReferencePCM(decPath)
			if err != nil {
				t.Fatalf("Failed to read reference PCM: %v", err)
			}

			// Create decoder at 48 kHz stereo
			dec, err := NewDecoder(48000, 2)
			if err != nil {
				t.Fatalf("Failed to create decoder: %v", err)
			}

			// Enable CELT for RFC 6716 compliant decoding
			if err := dec.EnableCELT(); err != nil {
				t.Fatalf("Failed to enable CELT: %v", err)
			}

			// Decode all packets and compare to reference
			frameSize := 1920 // 48 kHz × 20 ms × 2 channels
			refOffset := 0
			decodedCount := 0
			errorCount := 0
			skippedFrameCode := 0
			var totalSquaredError float64
			var totalSquaredRef float64 // For SNR calculation
			var totalSamples int
			var maxError int16
			configStats := make(map[string]*conformanceStats)

			for i, pkt := range packets {
				if len(pkt.data) == 0 {
					// Silent frame - reference has zeros
					refOffset += frameSize
					continue
				}

				info, err := ParseTOCByte(pkt.data[0])
				if err != nil {
					errorCount++
					refOffset += frameSize
					continue
				}

				codec := configName(info.Configuration)

				// Initialize stats for this codec path
				if configStats[codec] == nil {
					configStats[codec] = &conformanceStats{}
				}
				stats := configStats[codec]

				// Frame code determines how many frames in this packet:
				// 0 = 1 frame, 1 = 2 frames (equal length), 2/3 = variable
				// Reference .dec has decoded output for all frames.
				framesToDecode := 1
				switch info.FrameCode {
				case 0:
					framesToDecode = 1
				case 1:
					framesToDecode = 2
				case 2:
					// Two different-size frames
					framesToDecode = 2
				case 3:
					// Variable frame count - need to parse M byte
					// RFC 6716 §3.2.5: M byte layout is |v|p|     M     |
					// M (frame count) is in bits 0-5
					if len(pkt.data) >= 2 {
						mByte := pkt.data[1]
						framesToDecode = int(mByte & 0x3F)
						if framesToDecode == 0 || framesToDecode > 48 {
							stats.skipped++
							refOffset += frameSize * 2 // Conservative estimate
							skippedFrameCode++
							continue
						}
					} else {
						stats.skipped++
						refOffset += frameSize * 2
						skippedFrameCode++
						continue
					}
				}

				// Attempt decode
				out := make([]int16, frameSize*framesToDecode)
				n, decErr := dec.Decode(pkt.data, out)
				if decErr != nil {
					stats.failed++
					if errorCount < 5 && i < 20 {
						t.Logf("Packet %d decode error: %v (config=%s, code=%d)", i, decErr, codec, info.FrameCode)
					}
					errorCount++
					// Advance reference by expected frame count
					refOffset += frameSize * framesToDecode
					continue
				}

				decodedCount++
				stats.decoded++

				// Compare to reference
				if refOffset+n > len(refPCM) {
					t.Logf("Reference buffer exhausted at packet %d", i)
					break
				}

				// Calculate error metrics for this frame
				for j := 0; j < n; j++ {
					if refOffset+j >= len(refPCM) {
						break
					}
					ref := refPCM[refOffset+j]
					got := out[j]
					diff := int16(int(got) - int(ref))
					if diff < 0 {
						diff = -diff
					}
					if diff > maxError {
						maxError = diff
					}
					if diff > stats.maxError {
						stats.maxError = diff
					}
					totalSquaredError += float64(diff) * float64(diff)
					totalSquaredRef += float64(ref) * float64(ref)
					stats.sumSquaredError += float64(diff) * float64(diff)
					stats.sumSquaredRef += float64(ref) * float64(ref)
					totalSamples++
					stats.samples++
				}

				refOffset += frameSize * framesToDecode
			}

			// Calculate RMS error and SNR
			rmsError := float64(0)
			snrDB := float64(0)
			if totalSamples > 0 {
				rmsError = math.Sqrt(totalSquaredError / float64(totalSamples))
				if totalSquaredError > 0 {
					// SNR = 10 * log10(signal_power / noise_power)
					snrDB = 10 * math.Log10(totalSquaredRef/totalSquaredError)
				}
			}

			t.Logf("Decoded %d/%d packets, errors=%d, skipped(code2/3)=%d",
				decodedCount, len(packets), errorCount, skippedFrameCode)
			if totalSamples > 0 {
				t.Logf("Overall RMS error: %.2f, max error: %d, SNR: %.2f dB", rmsError, maxError, snrDB)
			} else {
				t.Logf("Overall RMS error: 0 (no samples decoded)")
			}

			// Report per-codec statistics
			type codecRanking struct {
				name    string
				rms     float64
				maxErr  int16
				decoded int
				failed  int
			}
			var rankings []codecRanking
			for codec, stats := range configStats {
				if stats.samples > 0 {
					codecRMS := math.Sqrt(stats.sumSquaredError / float64(stats.samples))
					codecSNR := float64(0)
					if stats.sumSquaredError > 0 {
						codecSNR = 10 * math.Log10(stats.sumSquaredRef/stats.sumSquaredError)
					}
					t.Logf("  %s: decoded=%d, failed=%d, skipped=%d, RMS=%.2f, maxErr=%d, SNR=%.2f dB",
						codec, stats.decoded, stats.failed, stats.skipped, codecRMS, stats.maxError, codecSNR)
					rankings = append(rankings, codecRanking{codec, codecRMS, stats.maxError, stats.decoded, stats.failed})
				} else if stats.decoded > 0 || stats.failed > 0 || stats.skipped > 0 {
					t.Logf("  %s: decoded=%d, failed=%d, skipped=%d (no samples compared)",
						codec, stats.decoded, stats.failed, stats.skipped)
					rankings = append(rankings, codecRanking{codec, -1, 0, stats.decoded, stats.failed})
				}
			}

			// Sort by RMS error (highest first) to identify paths needing most work
			sort.Slice(rankings, func(i, j int) bool {
				// Put failed paths (rms == -1) first, then by RMS descending
				if rankings[i].rms < 0 && rankings[j].rms >= 0 {
					return true
				}
				if rankings[i].rms >= 0 && rankings[j].rms < 0 {
					return false
				}
				return rankings[i].rms > rankings[j].rms
			})

			// Report ranking for priority analysis (item 2.3)
			if len(rankings) > 0 {
				t.Logf("Codec path deviation ranking (highest error first):")
				for rank, r := range rankings {
					if r.rms < 0 {
						t.Logf("  %d. %s: NOT DECODED (failed=%d)", rank+1, r.name, r.failed)
					} else {
						t.Logf("  %d. %s: RMS=%.2f, maxErr=%d", rank+1, r.name, r.rms, r.maxErr)
					}
				}
			}

			// This test documents conformance metrics without failing on errors.
			// A future milestone (2.4) will enforce strict error bounds once
			// deviations are addressed in order of impact.
		})
	}
}

// conformanceStats tracks decode statistics per codec path.
type conformanceStats struct {
	decoded         int
	failed          int
	skipped         int
	samples         int
	sumSquaredError float64
	sumSquaredRef   float64 // Sum of squared reference values for SNR
	maxError        int16
}

// readReferencePCM reads a .dec reference file (little-endian int16 stereo PCM).
func readReferencePCM(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// .dec files are raw little-endian int16 samples
	samples := make([]int16, len(data)/2)
	for i := 0; i < len(samples); i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}

	return samples, nil
}
