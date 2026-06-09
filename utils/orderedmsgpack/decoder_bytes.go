package orderedmsgpack

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/iancoleman/orderedmap"
	"github.com/shamaton/msgpack/v3"
)

const maxDecodeDepth = 512

type byteDecoder struct {
	data []byte
	off  int
}

func decodeOrderedMapBytes(data []byte) (*orderedmap.OrderedMap, error) {
	d := byteDecoder{data: data}
	val, err := d.decodeValue(0)
	if err != nil {
		return nil, err
	}
	if d.off != len(data) {
		return nil, fmt.Errorf("trailing bytes at offset %d", d.off)
	}
	om, ok := val.(*orderedmap.OrderedMap)
	if !ok {
		return nil, fmt.Errorf("decoded value is %T, not *orderedmap.OrderedMap", val)
	}
	om.SetEscapeHTML(false)
	return om, nil
}

func (d *byteDecoder) decodeValue(depth int) (any, error) {
	if depth > maxDecodeDepth {
		return nil, fmt.Errorf("maximum depth %d exceeded", maxDecodeDepth)
	}
	b, err := d.readByte()
	if err != nil {
		return nil, fmt.Errorf("read type byte: %w", err)
	}

	if b <= msgpackFixPosIntMax {
		return int64(b), nil
	}
	if b >= msgpackFixNegIntMin {
		return int64(int8(b)), nil
	}
	if b >= msgpackFixMapMin && b <= msgpackFixMapMax {
		return d.decodeMap(int(b&0x0f), depth+1)
	}
	if b >= msgpackFixArrMin && b <= msgpackFixArrMax {
		return d.decodeArray(int(b&0x0f), depth+1)
	}
	if b >= msgpackFixStrMin && b <= msgpackFixStrMax {
		return d.decodeString(int(b & 0x1f))
	}

	switch b {
	case msgpackNil:
		return nil, nil
	case msgpackFalse:
		return false, nil
	case msgpackTrue:
		return true, nil
	case msgpackBin8:
		n, err := d.readUint8()
		if err != nil {
			return nil, err
		}
		return d.readBytesCopy(int(n))
	case msgpackBin16:
		n, err := d.readUint16()
		if err != nil {
			return nil, err
		}
		return d.readBytesCopy(int(n))
	case msgpackBin32:
		n, err := d.readUint32()
		if err != nil {
			return nil, err
		}
		return d.readBytesCopy(int(n))
	case msgpackExt8:
		n, err := d.readUint8()
		if err != nil {
			return nil, err
		}
		return d.decodeExtPayload(int(n))
	case msgpackExt16:
		n, err := d.readUint16()
		if err != nil {
			return nil, err
		}
		return d.decodeExtPayload(int(n))
	case msgpackExt32:
		n, err := d.readUint32()
		if err != nil {
			return nil, err
		}
		return d.decodeExtPayload(int(n))
	case msgpackFloat32:
		bits, err := d.readUint32()
		if err != nil {
			return nil, err
		}
		return float64(math.Float32frombits(bits)), nil
	case msgpackFloat64:
		bits, err := d.readUint64()
		if err != nil {
			return nil, err
		}
		return math.Float64frombits(bits), nil
	case msgpackUint8:
		v, err := d.readUint8()
		return int64(v), err
	case msgpackUint16:
		v, err := d.readUint16()
		return int64(v), err
	case msgpackUint32:
		v, err := d.readUint32()
		return int64(v), err
	case msgpackUint64:
		v, err := d.readUint64()
		return v, err
	case msgpackInt8:
		v, err := d.readUint8()
		return int64(int8(v)), err
	case msgpackInt16:
		v, err := d.readUint16()
		return int64(int16(v)), err
	case msgpackInt32:
		v, err := d.readUint32()
		return int64(int32(v)), err
	case msgpackInt64:
		v, err := d.readUint64()
		return int64(v), err
	case msgpackFixExt1:
		return d.decodeExtPayload(1)
	case msgpackFixExt2:
		return d.decodeExtPayload(2)
	case msgpackFixExt4:
		return d.decodeExtPayload(4)
	case msgpackFixExt8:
		return d.decodeExtPayload(8)
	case msgpackFixExt16:
		return d.decodeExtPayload(16)
	case msgpackStr8:
		n, err := d.readUint8()
		if err != nil {
			return nil, err
		}
		return d.decodeString(int(n))
	case msgpackStr16:
		n, err := d.readUint16()
		if err != nil {
			return nil, err
		}
		return d.decodeString(int(n))
	case msgpackStr32:
		n, err := d.readUint32()
		if err != nil {
			return nil, err
		}
		return d.decodeString(int(n))
	case msgpackArray16:
		n, err := d.readUint16()
		if err != nil {
			return nil, err
		}
		return d.decodeArray(int(n), depth+1)
	case msgpackArray32:
		n, err := d.readUint32()
		if err != nil {
			return nil, err
		}
		return d.decodeArray(int(n), depth+1)
	case msgpackMap16:
		n, err := d.readUint16()
		if err != nil {
			return nil, err
		}
		return d.decodeMap(int(n), depth+1)
	case msgpackMap32:
		n, err := d.readUint32()
		if err != nil {
			return nil, err
		}
		return d.decodeMap(int(n), depth+1)
	default:
		return nil, fmt.Errorf("unsupported msgpack type byte: 0x%02x", b)
	}
}

func (d *byteDecoder) decodeMap(n int, depth int) (*orderedmap.OrderedMap, error) {
	om := orderedmap.New()
	om.SetEscapeHTML(false)
	for i := range n {
		key, err := d.decodeMapKey(depth)
		if err != nil {
			return nil, fmt.Errorf("decode map key %d: %w", i, err)
		}
		val, err := d.decodeValue(depth)
		if err != nil {
			return nil, fmt.Errorf("decode map value for key %q: %w", key, err)
		}
		om.Set(key, val)
	}
	return om, nil
}

func (d *byteDecoder) decodeMapKey(depth int) (string, error) {
	b, err := d.readByte()
	if err != nil {
		return "", fmt.Errorf("read key type byte: %w", err)
	}
	if b >= msgpackFixStrMin && b <= msgpackFixStrMax {
		return d.decodeString(int(b & 0x1f))
	}
	switch b {
	case msgpackStr8:
		n, err := d.readUint8()
		if err != nil {
			return "", err
		}
		return d.decodeString(int(n))
	case msgpackStr16:
		n, err := d.readUint16()
		if err != nil {
			return "", err
		}
		return d.decodeString(int(n))
	case msgpackStr32:
		n, err := d.readUint32()
		if err != nil {
			return "", err
		}
		return d.decodeString(int(n))
	default:
		d.off--
		keyVal, err := d.decodeValue(depth)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%v", keyVal), nil
	}
}

func (d *byteDecoder) decodeArray(n int, depth int) ([]any, error) {
	arr := make([]any, n)
	for i := range n {
		val, err := d.decodeValue(depth)
		if err != nil {
			return nil, fmt.Errorf("decode array element %d: %w", i, err)
		}
		arr[i] = val
	}
	return arr, nil
}

func (d *byteDecoder) decodeString(n int) (string, error) {
	start, err := d.reserve(n)
	if err != nil {
		return "", err
	}
	return string(d.data[start:d.off]), nil
}

func (d *byteDecoder) decodeExtPayload(size int) (any, error) {
	typeByte, err := d.readByte()
	if err != nil {
		return nil, fmt.Errorf("read ext type: %w", err)
	}
	start, err := d.reserve(size)
	if err != nil {
		return nil, fmt.Errorf("read ext data: %w", err)
	}
	payload := d.data[start:d.off]
	if int8(typeByte) == orderedMapExtCode {
		var iom internalOrderedMap
		if err := msgpack.Unmarshal(payload, &iom); err != nil {
			return nil, fmt.Errorf("failed to unmarshal ordered map ext data: %w", err)
		}
		om := iom.ToOrderedMap()
		return &om, nil
	}
	out := make([]byte, len(payload))
	copy(out, payload)
	return out, nil
}

func (d *byteDecoder) readByte() (byte, error) {
	if d.off >= len(d.data) {
		return 0, fmt.Errorf("unexpected EOF at offset %d", d.off)
	}
	b := d.data[d.off]
	d.off++
	return b, nil
}

func (d *byteDecoder) readUint8() (uint8, error) {
	b, err := d.readByte()
	return uint8(b), err
}

func (d *byteDecoder) readUint16() (uint16, error) {
	start, err := d.reserve(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(d.data[start:d.off]), nil
}

func (d *byteDecoder) readUint32() (uint32, error) {
	start, err := d.reserve(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(d.data[start:d.off]), nil
}

func (d *byteDecoder) readUint64() (uint64, error) {
	start, err := d.reserve(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(d.data[start:d.off]), nil
}

func (d *byteDecoder) readBytesCopy(n int) ([]byte, error) {
	start, err := d.reserve(n)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, d.data[start:d.off])
	return out, nil
}

func (d *byteDecoder) reserve(n int) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("negative length %d", n)
	}
	if len(d.data)-d.off < n {
		return 0, fmt.Errorf("unexpected EOF at offset %d: need %d bytes, have %d", d.off, n, len(d.data)-d.off)
	}
	start := d.off
	d.off += n
	return start, nil
}
