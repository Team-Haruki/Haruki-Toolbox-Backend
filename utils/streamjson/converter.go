package streamjson

import (
	"bytes"
	"fmt"
	"io"
	"math"
)

func Convert(msgpackData []byte, w io.Writer) error {
	r := bytes.NewReader(msgpackData)
	return writeValue(r, w)
}

func writeValue(r *bytes.Reader, w io.Writer) error {
	b, err := r.ReadByte()
	if err != nil {
		return fmt.Errorf("read type byte: %w", err)
	}

	if b <= msgpackFixPosIntMax {
		_, err = fmt.Fprintf(w, "%d", int64(b))
		return err
	}

	if b >= msgpackFixNegIntMin {
		_, err = fmt.Fprintf(w, "%d", int64(int8(b)))
		return err
	}

	if err := writeFixedCollectionOrString(r, w, b); err != errNotFixedType {
		return err
	}

	switch b {
	case msgpackNil:
		_, err = w.Write([]byte(jsonLiteralNull))
		return err
	case msgpackFalse:
		_, err = w.Write([]byte(jsonLiteralFalse))
		return err
	case msgpackTrue:
		_, err = w.Write([]byte(jsonLiteralTrue))
		return err

	case msgpackBin8:
		n, err := readUint8(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n))
	case msgpackBin16:
		n, err := readUint16(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n))
	case msgpackBin32:
		n, err := readUint32(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n))

	case msgpackExt8:
		n, err := readUint8(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n)+1)
	case msgpackExt16:
		n, err := readUint16(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n)+1)
	case msgpackExt32:
		n, err := readUint32(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n)+1)

	case msgpackFloat32:
		bits, err := readUint32(r)
		if err != nil {
			return err
		}
		f := float64(math.Float32frombits(bits))
		return writeFloat(w, f)

	case msgpackFloat64:
		bits, err := readUint64(r)
		if err != nil {
			return err
		}
		f := math.Float64frombits(bits)
		return writeFloat(w, f)

	case msgpackUint8:
		v, err := readUint8(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", v)
		return err

	case msgpackUint16:
		v, err := readUint16(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", v)
		return err

	case msgpackUint32:
		v, err := readUint32(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", v)
		return err

	case msgpackUint64:
		v, err := readUint64(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", v)
		return err

	case msgpackInt8:
		v, err := readUint8(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", int8(v))
		return err

	case msgpackInt16:
		v, err := readUint16(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", int16(v))
		return err

	case msgpackInt32:
		v, err := readUint32(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", int32(v))
		return err

	case msgpackInt64:
		v, err := readUint64(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", int64(v))
		return err

	case msgpackFixExt1:
		return skipAndWriteNull(r, w, 1+1)
	case msgpackFixExt2:
		return skipAndWriteNull(r, w, 1+2)
	case msgpackFixExt4:
		return skipAndWriteNull(r, w, 1+4)
	case msgpackFixExt8:
		return skipAndWriteNull(r, w, 1+8)
	case msgpackFixExt16:
		return skipAndWriteNull(r, w, 1+16)

	case msgpackStr8:
		n, err := readUint8(r)
		if err != nil {
			return err
		}
		return writeString(r, w, int(n))

	case msgpackStr16:
		n, err := readUint16(r)
		if err != nil {
			return err
		}
		return writeString(r, w, int(n))

	case msgpackStr32:
		n, err := readUint32(r)
		if err != nil {
			return err
		}
		return writeString(r, w, int(n))

	case msgpackArray16:
		n, err := readUint16(r)
		if err != nil {
			return err
		}
		return writeArray(r, w, int(n))

	case msgpackArray32:
		n, err := readUint32(r)
		if err != nil {
			return err
		}
		return writeArray(r, w, int(n))

	case msgpackMap16:
		n, err := readUint16(r)
		if err != nil {
			return err
		}
		return writeMap(r, w, int(n))

	case msgpackMap32:
		n, err := readUint32(r)
		if err != nil {
			return err
		}
		return writeMap(r, w, int(n))
	}

	return fmt.Errorf("unsupported msgpack type byte: 0x%02x", b)
}
