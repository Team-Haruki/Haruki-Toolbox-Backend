package streamjson

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
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

	if b <= 0x7f {
		_, err = fmt.Fprintf(w, "%d", int64(b))
		return err
	}

	if b >= 0xe0 {
		_, err = fmt.Fprintf(w, "%d", int64(int8(b)))
		return err
	}

	if b >= 0x80 && b <= 0x8f {
		return writeMap(r, w, int(b&0x0f))
	}

	if b >= 0x90 && b <= 0x9f {
		return writeArray(r, w, int(b&0x0f))
	}

	if b >= 0xa0 && b <= 0xbf {
		return writeString(r, w, int(b&0x1f))
	}

	switch b {
	case 0xc0:
		_, err = w.Write([]byte("null"))
		return err
	case 0xc2:
		_, err = w.Write([]byte("false"))
		return err
	case 0xc3:
		_, err = w.Write([]byte("true"))
		return err

	case 0xc4:
		n, err := readUint8(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n))
	case 0xc5:
		n, err := readUint16(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n))
	case 0xc6:
		n, err := readUint32(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n))

	case 0xc7:
		n, err := readUint8(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n)+1)
	case 0xc8:
		n, err := readUint16(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n)+1)
	case 0xc9:
		n, err := readUint32(r)
		if err != nil {
			return err
		}
		return skipAndWriteNull(r, w, int(n)+1)

	case 0xca:
		bits, err := readUint32(r)
		if err != nil {
			return err
		}
		f := float64(math.Float32frombits(bits))
		return writeFloat(w, f)

	case 0xcb:
		bits, err := readUint64(r)
		if err != nil {
			return err
		}
		f := math.Float64frombits(bits)
		return writeFloat(w, f)

	case 0xcc:
		v, err := readUint8(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", v)
		return err

	case 0xcd:
		v, err := readUint16(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", v)
		return err

	case 0xce:
		v, err := readUint32(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", v)
		return err

	case 0xcf:
		v, err := readUint64(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", v)
		return err

	case 0xd0:
		v, err := readUint8(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", int8(v))
		return err

	case 0xd1:
		v, err := readUint16(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", int16(v))
		return err

	case 0xd2:
		v, err := readUint32(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", int32(v))
		return err

	case 0xd3:
		v, err := readUint64(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%d", int64(v))
		return err

	case 0xd4:
		return skipAndWriteNull(r, w, 1+1)
	case 0xd5:
		return skipAndWriteNull(r, w, 1+2)
	case 0xd6:
		return skipAndWriteNull(r, w, 1+4)
	case 0xd7:
		return skipAndWriteNull(r, w, 1+8)
	case 0xd8:
		return skipAndWriteNull(r, w, 1+16)

	case 0xd9:
		n, err := readUint8(r)
		if err != nil {
			return err
		}
		return writeString(r, w, int(n))

	case 0xda:
		n, err := readUint16(r)
		if err != nil {
			return err
		}
		return writeString(r, w, int(n))

	case 0xdb:
		n, err := readUint32(r)
		if err != nil {
			return err
		}
		return writeString(r, w, int(n))

	case 0xdc:
		n, err := readUint16(r)
		if err != nil {
			return err
		}
		return writeArray(r, w, int(n))

	case 0xdd:
		n, err := readUint32(r)
		if err != nil {
			return err
		}
		return writeArray(r, w, int(n))

	case 0xde:
		n, err := readUint16(r)
		if err != nil {
			return err
		}
		return writeMap(r, w, int(n))

	case 0xdf:
		n, err := readUint32(r)
		if err != nil {
			return err
		}
		return writeMap(r, w, int(n))
	}

	return fmt.Errorf("unsupported msgpack type byte: 0x%02x", b)
}

func writeMap(r *bytes.Reader, w io.Writer, n int) error {
	if _, err := w.Write([]byte{'{'}); err != nil {
		return err
	}
	for i := range n {
		if i > 0 {
			if _, err := w.Write([]byte{','}); err != nil {
				return err
			}
		}

		keyBytes, err := readMsgpackString(r)
		if err != nil {
			return fmt.Errorf("map key %d: %w", i, err)
		}

		if err := writeJSONString(w, keyBytes); err != nil {
			return err
		}
		if _, err := w.Write([]byte{':'}); err != nil {
			return err
		}

		if err := writeValue(r, w); err != nil {
			return fmt.Errorf("map value for key %q: %w", string(keyBytes), err)
		}
	}
	_, err := w.Write([]byte{'}'})
	return err
}

func writeArray(r *bytes.Reader, w io.Writer, n int) error {
	if _, err := w.Write([]byte{'['}); err != nil {
		return err
	}
	for i := range n {
		if i > 0 {
			if _, err := w.Write([]byte{','}); err != nil {
				return err
			}
		}
		if err := writeValue(r, w); err != nil {
			return fmt.Errorf("array element %d: %w", i, err)
		}
	}
	_, err := w.Write([]byte{']'})
	return err
}

func readMsgpackString(r *bytes.Reader) ([]byte, error) {
	b, err := r.ReadByte()
	if err != nil {
		return nil, err
	}

	var n int
	if b >= 0xa0 && b <= 0xbf {
		n = int(b & 0x1f)
	} else {
		switch b {
		case 0xd9:
			v, err := readUint8(r)
			if err != nil {
				return nil, err
			}
			n = int(v)
		case 0xda:
			v, err := readUint16(r)
			if err != nil {
				return nil, err
			}
			n = int(v)
		case 0xdb:
			v, err := readUint32(r)
			if err != nil {
				return nil, err
			}
			n = int(v)
		default:

			if err := r.UnreadByte(); err != nil {
				return nil, err
			}

			return nil, fmt.Errorf("non-string map key type: 0x%02x", b)
		}
	}

	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeString(r *bytes.Reader, w io.Writer, n int) error {
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	return writeJSONString(w, buf)
}

func writeJSONString(w io.Writer, s []byte) error {
	if _, err := w.Write([]byte{'"'}); err != nil {
		return err
	}
	for _, c := range s {
		switch c {
		case '"':
			if _, err := w.Write([]byte(`\"`)); err != nil {
				return err
			}
		case '\\':
			if _, err := w.Write([]byte(`\\`)); err != nil {
				return err
			}
		case '\n':
			if _, err := w.Write([]byte(`\n`)); err != nil {
				return err
			}
		case '\r':
			if _, err := w.Write([]byte(`\r`)); err != nil {
				return err
			}
		case '\t':
			if _, err := w.Write([]byte(`\t`)); err != nil {
				return err
			}
		default:
			if c < 0x20 {
				if _, err := fmt.Fprintf(w, `\u%04x`, c); err != nil {
					return err
				}
			} else {
				if _, err := w.Write([]byte{c}); err != nil {
					return err
				}
			}
		}
	}
	_, err := w.Write([]byte{'"'})
	return err
}

func writeFloat(w io.Writer, f float64) error {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		_, err := w.Write([]byte("null"))
		return err
	}
	_, err := w.Write([]byte(strconv.FormatFloat(f, 'f', -1, 64)))
	return err
}

func skipAndWriteNull(r *bytes.Reader, w io.Writer, n int) error {
	if _, err := r.Seek(int64(n), io.SeekCurrent); err != nil {
		return err
	}
	_, err := w.Write([]byte("null"))
	return err
}

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
