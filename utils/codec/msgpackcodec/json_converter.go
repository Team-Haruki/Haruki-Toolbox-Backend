package msgpackcodec

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
)

// WriteJSON converts one complete MessagePack value to JSON without building
// an object tree. It validates structure and depth before writing. The input
// must remain unchanged until return. JSON semantic errors and writer failures
// may leave partial output. Binary/extensions and non-finite floats emit null;
// object keys must be unique UTF-8 strings. Integer precision is preserved.
func WriteJSON(w io.Writer, data []byte, options JSONOptions) error {
	if err := options.validate(); err != nil {
		return err
	}
	if err := ValidateMaxDepth(data, DefaultMaxUploadDepth); err != nil {
		return fmt.Errorf("validate msgpack: %w", err)
	}
	r := &jsonReader{cursor: cursor{data: data}, rule: options.DerivedStringField}
	return json.MarshalWrite(w, msgpackDocument{r: r})
}

var errNotFixedType = errors.New("not fixed type")

type jsonReader struct {
	cursor
	rule StringFieldRule
}

func writeValue(r *jsonReader, w *jsontext.Encoder) error {
	return writeValueInObject(r, w, "")
}

func writeValueInObject(r *jsonReader, w *jsontext.Encoder, objectName string) error {
	b, err := r.readByte()
	if err != nil {
		return fmt.Errorf("read type byte: %w", err)
	}

	if b <= msgpackFixPosIntMax {
		return w.WriteToken(jsontext.Int(int64(b)))
	}

	if b >= msgpackFixNegIntMin {
		return w.WriteToken(jsontext.Int(int64(int8(b))))
	}

	if err := writeFixedCollectionOrString(r, w, b, objectName); err != errNotFixedType {
		return err
	}

	switch b {
	case msgpackNil:
		return w.WriteToken(jsontext.Null)
	case msgpackFalse:
		return w.WriteToken(jsontext.False)
	case msgpackTrue:
		return w.WriteToken(jsontext.True)

	case msgpackBin8:
		n, err := r.readUint8()
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n))
	case msgpackBin16:
		n, err := r.readUint16()
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n))
	case msgpackBin32:
		n, err := r.readUint32()
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n))

	case msgpackExt8:
		n, err := r.readUint8()
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n)+1)
	case msgpackExt16:
		n, err := r.readUint16()
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n)+1)
	case msgpackExt32:
		n, err := r.readUint32()
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n)+1)

	case msgpackFloat32:
		bits, err := r.readUint32()
		if err != nil {
			return err
		}
		f := float64(math.Float32frombits(bits))
		return writeFloat(w, f)

	case msgpackFloat64:
		bits, err := r.readUint64()
		if err != nil {
			return err
		}
		f := math.Float64frombits(bits)
		return writeFloat(w, f)

	case msgpackUint8:
		v, err := r.readUint8()
		if err != nil {
			return err
		}
		return w.WriteToken(jsontext.Uint(uint64(v)))

	case msgpackUint16:
		v, err := r.readUint16()
		if err != nil {
			return err
		}
		return w.WriteToken(jsontext.Uint(uint64(v)))

	case msgpackUint32:
		v, err := r.readUint32()
		if err != nil {
			return err
		}
		return w.WriteToken(jsontext.Uint(uint64(v)))

	case msgpackUint64:
		v, err := r.readUint64()
		if err != nil {
			return err
		}
		return w.WriteToken(jsontext.Uint(uint64(v)))

	case msgpackInt8:
		v, err := r.readUint8()
		if err != nil {
			return err
		}
		return w.WriteToken(jsontext.Int(int64(int8(v))))

	case msgpackInt16:
		v, err := r.readUint16()
		if err != nil {
			return err
		}
		return w.WriteToken(jsontext.Int(int64(int16(v))))

	case msgpackInt32:
		v, err := r.readUint32()
		if err != nil {
			return err
		}
		return w.WriteToken(jsontext.Int(int64(int32(v))))

	case msgpackInt64:
		v, err := r.readUint64()
		if err != nil {
			return err
		}
		return w.WriteToken(jsontext.Int(int64(int64(v))))

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
		n, err := r.readUint8()
		if err != nil {
			return err
		}
		return writeString(r, w, int(n))

	case msgpackStr16:
		n, err := r.readUint16()
		if err != nil {
			return err
		}
		return writeString(r, w, int(n))

	case msgpackStr32:
		n, err := r.readUint32()
		if err != nil {
			return err
		}
		return writeString(r, w, int(n))

	case msgpackArray16:
		n, err := r.readUint16()
		if err != nil {
			return err
		}
		return writeArray(r, w, int(n))

	case msgpackArray32:
		n, err := r.readUint32()
		if err != nil {
			return err
		}
		return writeArray(r, w, int(n))

	case msgpackMap16:
		n, err := r.readUint16()
		if err != nil {
			return err
		}
		return writeMap(r, w, int(n), objectName)

	case msgpackMap32:
		n, err := r.readUint32()
		if err != nil {
			return err
		}
		return writeMap(r, w, int(n), objectName)
	}

	return fmt.Errorf("unsupported msgpack type byte: 0x%02x", b)
}

type msgpackDocument struct{ r *jsonReader }

func (d msgpackDocument) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := writeValue(d.r, enc); err != nil {
		return err
	}
	if d.r.remaining() != 0 {
		return fmt.Errorf("trailing MessagePack bytes")
	}
	return nil
}
