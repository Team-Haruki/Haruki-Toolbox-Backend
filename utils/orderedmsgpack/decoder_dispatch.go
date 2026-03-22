package orderedmsgpack

import (
	"bytes"
	"fmt"
	"math"
)

func decodeValue(r *bytes.Reader) (any, error) {
	b, err := r.ReadByte()
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
		return decodeMap(r, int(b&0x0f))
	}
	if b >= msgpackFixArrMin && b <= msgpackFixArrMax {
		return decodeArray(r, int(b&0x0f))
	}
	if b >= msgpackFixStrMin && b <= msgpackFixStrMax {
		return decodeString(r, int(b&0x1f))
	}

	switch b {
	case msgpackNil:
		return nil, nil
	case msgpackFalse:
		return false, nil
	case msgpackTrue:
		return true, nil

	case msgpackBin8:
		n, err := readUint8(r)
		if err != nil {
			return nil, err
		}
		return readBytes(r, int(n))
	case msgpackBin16:
		n, err := readUint16(r)
		if err != nil {
			return nil, err
		}
		return readBytes(r, int(n))
	case msgpackBin32:
		n, err := readUint32(r)
		if err != nil {
			return nil, err
		}
		return readBytes(r, int(n))

	case msgpackExt8:
		return decodeExt8(r)
	case msgpackExt16:
		return decodeExt16(r)
	case msgpackExt32:
		return decodeExt32(r)

	case msgpackFloat32:
		bits, err := readUint32(r)
		if err != nil {
			return nil, err
		}
		return float64(math.Float32frombits(bits)), nil
	case msgpackFloat64:
		bits, err := readUint64(r)
		if err != nil {
			return nil, err
		}
		return math.Float64frombits(bits), nil

	case msgpackUint8:
		v, err := readUint8(r)
		return int64(v), err
	case msgpackUint16:
		v, err := readUint16(r)
		return int64(v), err
	case msgpackUint32:
		v, err := readUint32(r)
		return int64(v), err
	case msgpackUint64:
		v, err := readUint64(r)
		return v, err

	case msgpackInt8:
		v, err := readUint8(r)
		return int64(int8(v)), err
	case msgpackInt16:
		v, err := readUint16(r)
		return int64(int16(v)), err
	case msgpackInt32:
		v, err := readUint32(r)
		return int64(int32(v)), err
	case msgpackInt64:
		v, err := readUint64(r)
		return int64(v), err

	case msgpackFixExt1:
		return decodeFixExt(r, 1)
	case msgpackFixExt2:
		return decodeFixExt(r, 2)
	case msgpackFixExt4:
		return decodeFixExt(r, 4)
	case msgpackFixExt8:
		return decodeFixExt(r, 8)
	case msgpackFixExt16:
		return decodeFixExt(r, 16)

	case msgpackStr8:
		n, err := readUint8(r)
		if err != nil {
			return nil, err
		}
		return decodeString(r, int(n))
	case msgpackStr16:
		n, err := readUint16(r)
		if err != nil {
			return nil, err
		}
		return decodeString(r, int(n))
	case msgpackStr32:
		n, err := readUint32(r)
		if err != nil {
			return nil, err
		}
		return decodeString(r, int(n))

	case msgpackArray16:
		n, err := readUint16(r)
		if err != nil {
			return nil, err
		}
		return decodeArray(r, int(n))
	case msgpackArray32:
		n, err := readUint32(r)
		if err != nil {
			return nil, err
		}
		return decodeArray(r, int(n))
	case msgpackMap16:
		n, err := readUint16(r)
		if err != nil {
			return nil, err
		}
		return decodeMap(r, int(n))
	case msgpackMap32:
		n, err := readUint32(r)
		if err != nil {
			return nil, err
		}
		return decodeMap(r, int(n))
	}

	return nil, fmt.Errorf("unsupported msgpack type byte: 0x%02x", b)
}
