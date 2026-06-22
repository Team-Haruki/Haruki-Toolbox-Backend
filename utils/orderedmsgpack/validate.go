package orderedmsgpack

import "fmt"

// DefaultMaxUploadDepth bounds container nesting for untrusted upload payloads.
// The shamaton msgpack decoder used on the upload path recurses once per nesting
// level with no depth limit, so a deeply nested payload (e.g. a long run of
// fixarray headers) triggers an unrecoverable Go stack-overflow fatal error that
// recover() cannot catch. Pre-validate depth with ValidateMaxDepth before handing
// bytes to that decoder.
const DefaultMaxUploadDepth = 256

// ValidateMaxDepth walks a msgpack byte stream and returns an error if its
// container nesting exceeds maxDepth, if a length header exceeds the remaining
// bytes, or if the bytes are otherwise malformed. It does not allocate the decoded
// values — it only skips them. Its own recursion is bounded by maxDepth, so it
// cannot overflow the stack while guarding against a decoder that would.
func ValidateMaxDepth(data []byte, maxDepth int) error {
	d := byteDecoder{data: data}
	if err := d.validateValue(0, maxDepth); err != nil {
		return err
	}
	if d.off != len(data) {
		return fmt.Errorf("trailing bytes at offset %d", d.off)
	}
	return nil
}

func (d *byteDecoder) validateValue(depth, maxDepth int) error {
	if depth > maxDepth {
		return fmt.Errorf("maximum nesting depth %d exceeded", maxDepth)
	}
	b, err := d.readByte()
	if err != nil {
		return fmt.Errorf("read type byte: %w", err)
	}

	switch {
	case b <= msgpackFixPosIntMax, b >= msgpackFixNegIntMin:
		return nil
	case b >= msgpackFixMapMin && b <= msgpackFixMapMax:
		return d.validateMap(int(b&0x0f), depth+1, maxDepth)
	case b >= msgpackFixArrMin && b <= msgpackFixArrMax:
		return d.validateArray(int(b&0x0f), depth+1, maxDepth)
	case b >= msgpackFixStrMin && b <= msgpackFixStrMax:
		_, err := d.reserve(int(b & 0x1f))
		return err
	}

	switch b {
	case msgpackNil, msgpackFalse, msgpackTrue:
		return nil
	case msgpackUint8, msgpackInt8:
		_, err := d.reserve(1)
		return err
	case msgpackUint16, msgpackInt16:
		_, err := d.reserve(2)
		return err
	case msgpackFloat32, msgpackUint32, msgpackInt32:
		_, err := d.reserve(4)
		return err
	case msgpackFloat64, msgpackUint64, msgpackInt64:
		_, err := d.reserve(8)
		return err
	case msgpackBin8, msgpackStr8:
		n, err := d.readUint8()
		if err != nil {
			return err
		}
		_, err = d.reserve(int(n))
		return err
	case msgpackBin16, msgpackStr16:
		n, err := d.readUint16()
		if err != nil {
			return err
		}
		_, err = d.reserve(int(n))
		return err
	case msgpackBin32, msgpackStr32:
		n, err := d.readUint32()
		if err != nil {
			return err
		}
		_, err = d.reserve(int(n))
		return err
	case msgpackExt8:
		n, err := d.readUint8()
		if err != nil {
			return err
		}
		_, err = d.reserve(int(n) + 1) // +1 for the ext type byte
		return err
	case msgpackExt16:
		n, err := d.readUint16()
		if err != nil {
			return err
		}
		_, err = d.reserve(int(n) + 1)
		return err
	case msgpackExt32:
		n, err := d.readUint32()
		if err != nil {
			return err
		}
		_, err = d.reserve(int(n) + 1)
		return err
	case msgpackFixExt1:
		_, err := d.reserve(1 + 1)
		return err
	case msgpackFixExt2:
		_, err := d.reserve(2 + 1)
		return err
	case msgpackFixExt4:
		_, err := d.reserve(4 + 1)
		return err
	case msgpackFixExt8:
		_, err := d.reserve(8 + 1)
		return err
	case msgpackFixExt16:
		_, err := d.reserve(16 + 1)
		return err
	case msgpackArray16:
		n, err := d.readUint16()
		if err != nil {
			return err
		}
		return d.validateArray(int(n), depth+1, maxDepth)
	case msgpackArray32:
		n, err := d.readUint32()
		if err != nil {
			return err
		}
		return d.validateArray(int(n), depth+1, maxDepth)
	case msgpackMap16:
		n, err := d.readUint16()
		if err != nil {
			return err
		}
		return d.validateMap(int(n), depth+1, maxDepth)
	case msgpackMap32:
		n, err := d.readUint32()
		if err != nil {
			return err
		}
		return d.validateMap(int(n), depth+1, maxDepth)
	default:
		return fmt.Errorf("unsupported msgpack type byte: 0x%02x", b)
	}
}

func (d *byteDecoder) validateArray(n, depth, maxDepth int) error {
	// Each element consumes at least one byte, so a valid array of n elements
	// needs at least n remaining bytes; reject impossible lengths cheaply.
	if n < 0 || n > len(d.data)-d.off {
		return fmt.Errorf("array length %d exceeds remaining bytes %d", n, len(d.data)-d.off)
	}
	for range n {
		if err := d.validateValue(depth, maxDepth); err != nil {
			return err
		}
	}
	return nil
}

func (d *byteDecoder) validateMap(n, depth, maxDepth int) error {
	if n < 0 || n > len(d.data)-d.off {
		return fmt.Errorf("map length %d exceeds remaining bytes %d", n, len(d.data)-d.off)
	}
	for range n {
		if err := d.validateValue(depth, maxDepth); err != nil { // key
			return err
		}
		if err := d.validateValue(depth, maxDepth); err != nil { // value
			return err
		}
	}
	return nil
}
