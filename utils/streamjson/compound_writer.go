package streamjson

import (
	"bytes"
	"fmt"
	"io"
)

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
	if b >= msgpackFixStrMin && b <= msgpackFixStrMax {
		n = int(b & 0x1f)
	} else {
		switch b {
		case msgpackStr8:
			v, err := readUint8(r)
			if err != nil {
				return nil, err
			}
			n = int(v)
		case msgpackStr16:
			v, err := readUint16(r)
			if err != nil {
				return nil, err
			}
			n = int(v)
		case msgpackStr32:
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
