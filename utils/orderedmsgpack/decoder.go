package orderedmsgpack

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/iancoleman/orderedmap"
	"github.com/shamaton/msgpack/v2"
)

// decodeValue reads a single msgpack value from the reader and returns it as a Go value.
// Maps are decoded as *orderedmap.OrderedMap to preserve key insertion order.
// Arrays are decoded as []any. Scalars are decoded as their native Go types.
func decodeValue(r *bytes.Reader) (any, error) {
	b, err := r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("read type byte: %w", err)
	}

	// positive fixint: 0x00 - 0x7f
	if b <= 0x7f {
		return int64(b), nil
	}
	// negative fixint: 0xe0 - 0xff
	if b >= 0xe0 {
		return int64(int8(b)), nil
	}
	// fixmap: 0x80 - 0x8f
	if b >= 0x80 && b <= 0x8f {
		n := int(b & 0x0f)
		return decodeMap(r, n)
	}
	// fixarray: 0x90 - 0x9f
	if b >= 0x90 && b <= 0x9f {
		n := int(b & 0x0f)
		return decodeArray(r, n)
	}
	// fixstr: 0xa0 - 0xbf
	if b >= 0xa0 && b <= 0xbf {
		n := int(b & 0x1f)
		return decodeString(r, n)
	}

	switch b {
	// nil
	case 0xc0:
		return nil, nil
	// (never used: 0xc1)
	// false
	case 0xc2:
		return false, nil
	// true
	case 0xc3:
		return true, nil

	// bin8
	case 0xc4:
		n, err := readUint8(r)
		if err != nil {
			return nil, err
		}
		return readBytes(r, int(n))
	// bin16
	case 0xc5:
		n, err := readUint16(r)
		if err != nil {
			return nil, err
		}
		return readBytes(r, int(n))
	// bin32
	case 0xc6:
		n, err := readUint32(r)
		if err != nil {
			return nil, err
		}
		return readBytes(r, int(n))

	// ext8
	case 0xc7:
		return decodeExt8(r)
	// ext16
	case 0xc8:
		return decodeExt16(r)
	// ext32
	case 0xc9:
		return decodeExt32(r)

	// float32
	case 0xca:
		bits, err := readUint32(r)
		if err != nil {
			return nil, err
		}
		return float64(math.Float32frombits(bits)), nil
	// float64
	case 0xcb:
		bits, err := readUint64(r)
		if err != nil {
			return nil, err
		}
		return math.Float64frombits(bits), nil

	// uint8
	case 0xcc:
		v, err := readUint8(r)
		return int64(v), err
	// uint16
	case 0xcd:
		v, err := readUint16(r)
		return int64(v), err
	// uint32
	case 0xce:
		v, err := readUint32(r)
		return int64(v), err
	// uint64
	case 0xcf:
		v, err := readUint64(r)
		return v, err // uint64 stays as uint64 to avoid overflow

	// int8
	case 0xd0:
		v, err := readUint8(r)
		return int64(int8(v)), err
	// int16
	case 0xd1:
		v, err := readUint16(r)
		return int64(int16(v)), err
	// int32
	case 0xd2:
		v, err := readUint32(r)
		return int64(int32(v)), err
	// int64
	case 0xd3:
		v, err := readUint64(r)
		return int64(v), err

	// fixext1
	case 0xd4:
		return decodeFixExt(r, 1)
	// fixext2
	case 0xd5:
		return decodeFixExt(r, 2)
	// fixext4
	case 0xd6:
		return decodeFixExt(r, 4)
	// fixext8
	case 0xd7:
		return decodeFixExt(r, 8)
	// fixext16
	case 0xd8:
		return decodeFixExt(r, 16)

	// str8
	case 0xd9:
		n, err := readUint8(r)
		if err != nil {
			return nil, err
		}
		return decodeString(r, int(n))
	// str16
	case 0xda:
		n, err := readUint16(r)
		if err != nil {
			return nil, err
		}
		return decodeString(r, int(n))
	// str32
	case 0xdb:
		n, err := readUint32(r)
		if err != nil {
			return nil, err
		}
		return decodeString(r, int(n))

	// array16
	case 0xdc:
		n, err := readUint16(r)
		if err != nil {
			return nil, err
		}
		return decodeArray(r, int(n))
	// array32
	case 0xdd:
		n, err := readUint32(r)
		if err != nil {
			return nil, err
		}
		return decodeArray(r, int(n))

	// map16
	case 0xde:
		n, err := readUint16(r)
		if err != nil {
			return nil, err
		}
		return decodeMap(r, int(n))
	// map32
	case 0xdf:
		n, err := readUint32(r)
		if err != nil {
			return nil, err
		}
		return decodeMap(r, int(n))
	}

	return nil, fmt.Errorf("unsupported msgpack type byte: 0x%02x", b)
}

// decodeMap reads n key-value pairs and returns an *orderedmap.OrderedMap.
func decodeMap(r *bytes.Reader, n int) (*orderedmap.OrderedMap, error) {
	om := orderedmap.New()
	om.SetEscapeHTML(false)
	for i := 0; i < n; i++ {
		keyVal, err := decodeValue(r)
		if err != nil {
			return nil, fmt.Errorf("decode map key %d: %w", i, err)
		}
		key, ok := keyVal.(string)
		if !ok {
			key = fmt.Sprintf("%v", keyVal)
		}
		val, err := decodeValue(r)
		if err != nil {
			return nil, fmt.Errorf("decode map value for key %q: %w", key, err)
		}
		om.Set(key, val)
	}
	return om, nil
}

// decodeArray reads n elements and returns a []any.
func decodeArray(r *bytes.Reader, n int) ([]any, error) {
	arr := make([]any, n)
	for i := 0; i < n; i++ {
		val, err := decodeValue(r)
		if err != nil {
			return nil, fmt.Errorf("decode array element %d: %w", i, err)
		}
		arr[i] = val
	}
	return arr, nil
}

func decodeString(r *bytes.Reader, n int) (string, error) {
	buf, err := readBytes(r, n)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// decodeExt8 handles ext8 format: [size:1byte][type:1byte][data:size bytes]
func decodeExt8(r *bytes.Reader) (any, error) {
	size, err := readUint8(r)
	if err != nil {
		return nil, err
	}
	return decodeExtPayload(r, int(size))
}

// decodeExt16 handles ext16 format: [size:2bytes][type:1byte][data:size bytes]
func decodeExt16(r *bytes.Reader) (any, error) {
	size, err := readUint16(r)
	if err != nil {
		return nil, err
	}
	return decodeExtPayload(r, int(size))
}

// decodeExt32 handles ext32 format: [size:4bytes][type:1byte][data:size bytes]
func decodeExt32(r *bytes.Reader) (any, error) {
	size, err := readUint32(r)
	if err != nil {
		return nil, err
	}
	return decodeExtPayload(r, int(size))
}

// decodeFixExt handles fixext formats: [type:1byte][data:N bytes]
func decodeFixExt(r *bytes.Reader, size int) (any, error) {
	return decodeExtPayload(r, size)
}

// decodeExtPayload reads the type byte and data, handling our custom ext code 100 specially.
func decodeExtPayload(r *bytes.Reader, size int) (any, error) {
	typeByte, err := r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("read ext type: %w", err)
	}

	data, err := readBytes(r, size)
	if err != nil {
		return nil, fmt.Errorf("read ext data: %w", err)
	}

	// If this is our custom OrderedMap extension, decode it using the internal format
	if int8(typeByte) == orderedMapExtCode {
		var iom internalOrderedMap
		if err := msgpack.Unmarshal(data, &iom); err != nil {
			return nil, fmt.Errorf("failed to unmarshal ordered map ext data: %w", err)
		}
		om := iom.ToOrderedMap()
		return &om, nil
	}

	// For other ext types, return raw data
	return data, nil
}

// --- binary reading helpers ---

func readUint8(r *bytes.Reader) (uint8, error) {
	b, err := r.ReadByte()
	return b, err
}

func readUint16(r *bytes.Reader) (uint16, error) {
	var buf [2]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(buf[:]), nil
}

func readUint32(r *bytes.Reader) (uint32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(buf[:]), nil
}

func readUint64(r *bytes.Reader) (uint64, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(buf[:]), nil
}

func readBytes(r *bytes.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
