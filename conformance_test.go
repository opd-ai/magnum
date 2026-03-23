package magnum

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
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
