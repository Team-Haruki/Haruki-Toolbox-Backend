package streamjson

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strconv"
)

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
		_, err := w.Write([]byte(jsonLiteralNull))
		return err
	}
	_, err := w.Write([]byte(strconv.FormatFloat(f, 'f', -1, 64)))
	return err
}

func skipAndWriteNull(r *bytes.Reader, w io.Writer, n int) error {
	if _, err := r.Seek(int64(n), io.SeekCurrent); err != nil {
		return err
	}
	_, err := w.Write([]byte(jsonLiteralNull))
	return err
}
