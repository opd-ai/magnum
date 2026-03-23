package magnum

// RFC 6716 §4.1 / §5.1 Range Coder Implementation
//
// The range coder is the foundational entropy coding layer used by both
// SILK and CELT codecs in Opus. It encodes symbols using an arithmetic
// coding scheme where each symbol's probability is expressed as a range
// [fl, fh) out of a total ft.
//
// This is a simplified implementation that uses a consistent state representation
// for both encoder and decoder to ensure round-trip compatibility. The encoder
// outputs bytes that the decoder can read back correctly.

// RangeEncoder implements an arithmetic range encoder.
// It encodes symbols using cumulative frequency ranges.
type RangeEncoder struct {
	// low is the low end of the current range.
	low uint64
	// rng is the size of the current range.
	rng uint64
	// buffer holds the encoded bytes.
	buffer []byte
}

// NewRangeEncoder creates a new range encoder ready for encoding symbols.
func NewRangeEncoder() *RangeEncoder {
	return &RangeEncoder{
		low:    0,
		rng:    0x100000000, // 2^32 for precision
		buffer: make([]byte, 0, 256),
	}
}

// Encode encodes a symbol with cumulative frequency [fl, fh) out of total ft.
// fl is the cumulative frequency of all symbols before this one.
// fh is the cumulative frequency including this symbol (fl + symbol_freq).
// ft is the total frequency of all symbols.
func (e *RangeEncoder) Encode(fl, fh, ft uint32) {
	// Narrow the range based on the symbol's interval
	r := e.rng / uint64(ft)
	e.low += r * uint64(fl)
	e.rng = r * uint64(fh-fl)
	e.normalize()
}

// EncodeLogP encodes a binary symbol with probability 1/2^logp for symbol "1".
// val is 0 or 1, logp is the log2 of the denominator (1 to 16).
func (e *RangeEncoder) EncodeLogP(val int, logp uint) {
	r := e.rng >> logp
	if val != 0 {
		e.low += e.rng - r
		e.rng = r
	} else {
		e.rng -= r
	}
	e.normalize()
}

// EncodeBits encodes raw bits directly without probability modeling.
// value is the value to encode, bits is the number of bits (1 to 25).
func (e *RangeEncoder) EncodeBits(value, bits uint32) {
	for bits > 0 {
		chunk := bits
		if chunk > 8 {
			chunk = 8
		}
		ft := uint32(1) << chunk
		mask := ft - 1
		fl := (value >> (bits - chunk)) & mask
		e.Encode(fl, fl+1, ft)
		bits -= chunk
	}
}

// normalize outputs bytes when the range becomes too narrow.
func (e *RangeEncoder) normalize() {
	for e.rng <= 0x00FFFFFF {
		// Output the top byte of low
		e.buffer = append(e.buffer, byte(e.low>>24))
		e.low = (e.low << 8) & 0xFFFFFFFF
		e.rng <<= 8
	}
}

// Bytes finalizes the encoding and returns the encoded byte stream.
func (e *RangeEncoder) Bytes() []byte {
	// Flush remaining bytes (output 4 more bytes to ensure complete state)
	for i := 0; i < 4; i++ {
		e.buffer = append(e.buffer, byte(e.low>>24))
		e.low = (e.low << 8) & 0xFFFFFFFF
	}
	return e.buffer
}

// Reset resets the encoder for reuse.
func (e *RangeEncoder) Reset() {
	e.low = 0
	e.rng = 0x100000000
	e.buffer = e.buffer[:0]
}

// RangeDecoder implements an arithmetic range decoder.
// It decodes symbols using cumulative frequency ranges.
type RangeDecoder struct {
	// code is the current coded value read from the bitstream.
	code uint64
	// rng is the size of the current range.
	rng uint64
	// data is the encoded byte stream.
	data []byte
	// pos is the current read position in data.
	pos int
}

// NewRangeDecoder creates a new range decoder for the given encoded data.
func NewRangeDecoder(data []byte) *RangeDecoder {
	d := &RangeDecoder{
		rng:  0x100000000, // 2^32 for precision
		data: data,
		pos:  0,
		code: 0,
	}
	// Initialize code from the first 4 bytes (or fewer if data is short).
	for i := 0; i < 4; i++ {
		d.code <<= 8
		if d.pos < len(d.data) {
			d.code |= uint64(d.data[d.pos])
			d.pos++
		}
	}
	return d
}

// Decode decodes a symbol given the total frequency ft.
// Returns the cumulative frequency fs such that fl <= fs < fh.
// Caller must call Update with [fl, fh) for the identified symbol.
func (d *RangeDecoder) Decode(ft uint32) uint32 {
	r := d.rng / uint64(ft)
	fs := uint32(d.code / r)
	if fs >= ft {
		fs = ft - 1
	}
	return fs
}

// Update updates the decoder state after determining the symbol's [fl, fh) range.
func (d *RangeDecoder) Update(fl, fh, ft uint32) {
	r := d.rng / uint64(ft)
	d.code -= r * uint64(fl)
	d.rng = r * uint64(fh-fl)
	d.normalize()
}

// DecodeLogP decodes a binary symbol with probability 1/2^logp for "1".
// Returns 0 or 1.
func (d *RangeDecoder) DecodeLogP(logp uint) int {
	r := d.rng >> logp
	threshold := d.rng - r

	if d.code >= threshold {
		d.code -= threshold
		d.rng = r
		d.normalize()
		return 1
	}
	d.rng = threshold
	d.normalize()
	return 0
}

// DecodeBits decodes raw bits directly without probability modeling.
func (d *RangeDecoder) DecodeBits(bits uint32) uint32 {
	var value uint32
	for bits > 0 {
		chunk := bits
		if chunk > 8 {
			chunk = 8
		}
		ft := uint32(1) << chunk
		fs := d.Decode(ft)
		d.Update(fs, fs+1, ft)
		value = (value << chunk) | fs
		bits -= chunk
	}
	return value
}

// normalize reads more bytes to maintain range precision.
func (d *RangeDecoder) normalize() {
	for d.rng <= 0x00FFFFFF {
		d.rng <<= 8
		d.code <<= 8
		if d.pos < len(d.data) {
			d.code |= uint64(d.data[d.pos])
			d.pos++
		}
	}
}

// Remaining returns the number of unread bytes in the input.
func (d *RangeDecoder) Remaining() int {
	return len(d.data) - d.pos
}

// BytesConsumed returns the number of bytes consumed from the input so far.
// This is useful for hybrid mode to determine where SILK data ends.
func (d *RangeDecoder) BytesConsumed() int {
	return d.pos
}
