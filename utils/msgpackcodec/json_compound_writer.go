package msgpackcodec

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
)

func writeMap(r *jsonReader, w *jsontext.Encoder, n int, objectName string) error {
	if err := w.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	var derivedString string
	var hasDerivedString bool
	var suppliedString jsontext.Value
	for i := range n {
		keyBytes, err := readMsgpackString(r)
		if err != nil {
			return fmt.Errorf("map key %d: %w", i, err)
		}
		key := string(keyBytes)
		if r.rule.ObjectName != "" && objectName == r.rule.ObjectName && key == r.rule.Target {
			if suppliedString != nil {
				return fmt.Errorf("duplicate %s", r.rule.Target)
			}
			var supplied bytes.Buffer
			if err := writeValueInObject(r, jsontext.NewEncoder(&supplied), ""); err != nil {
				return err
			}
			suppliedString = jsontext.Value(bytes.Clone(bytes.TrimSpace(supplied.Bytes())))
			continue
		}
		if err := w.WriteToken(jsontext.String(key)); err != nil {
			return err
		}
		childObjectName := ""
		if key == r.rule.ObjectName {
			childObjectName = key
		}
		if r.rule.ObjectName != "" && objectName == r.rule.ObjectName && key == r.rule.Source {
			var value bytes.Buffer
			if err := writeValueInObject(r, jsontext.NewEncoder(&value), childObjectName); err != nil {
				return err
			}
			raw := bytes.TrimSpace(value.Bytes())
			if err := w.WriteValue(raw); err != nil {
				return err
			}
			derivedString, hasDerivedString = jsonScalarAsString(string(raw))
		} else if err := writeValueInObject(r, w, childObjectName); err != nil {
			return err
		}
	}
	if hasDerivedString {
		if err := writeJSONString(w, []byte(r.rule.Target)); err != nil {
			return err
		}
		if err := writeJSONString(w, []byte(derivedString)); err != nil {
			return err
		}
	}
	if !hasDerivedString && suppliedString != nil {
		if err := writeJSONString(w, []byte(r.rule.Target)); err != nil {
			return err
		}
		if err := w.WriteValue(suppliedString); err != nil {
			return err
		}
	}

	return w.WriteToken(jsontext.EndObject)
}

func writeArray(r *jsonReader, w *jsontext.Encoder, n int) error {
	if err := w.WriteToken(jsontext.BeginArray); err != nil {
		return err
	}
	for i := range n {
		if err := writeValue(r, w); err != nil {
			return fmt.Errorf("array element %d: %w", i, err)
		}
	}
	return w.WriteToken(jsontext.EndArray)
}

func jsonScalarAsString(raw string) (string, bool) {
	value := jsontext.Value(raw)
	switch value.Kind() {
	case '"':
		var str string
		if err := json.Unmarshal(value, &str); err != nil || str == "" {
			return "", false
		}
		return str, true
	case '0':
		return raw, true
	default:
		return "", false
	}
}

func readMsgpackString(r *jsonReader) ([]byte, error) {
	b, err := r.readByte()
	if err != nil {
		return nil, err
	}

	var n int
	if b >= msgpackFixStrMin && b <= msgpackFixStrMax {
		n = int(b & 0x1f)
	} else {
		switch b {
		case msgpackStr8:
			v, err := r.readUint8()
			if err != nil {
				return nil, err
			}
			n = int(v)
		case msgpackStr16:
			v, err := r.readUint16()
			if err != nil {
				return nil, err
			}
			n = int(v)
		case msgpackStr32:
			v, err := r.readUint32()
			if err != nil {
				return nil, err
			}
			n = int(v)
		default:
			if err := r.unreadByte(); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("non-string map key type: 0x%02x", b)
		}
	}

	if n < 0 || n > r.remaining() {
		return nil, fmt.Errorf("string length %d exceeds remaining bytes %d", n, r.remaining())
	}
	return r.take(n)
}

func writeString(r *jsonReader, w *jsontext.Encoder, n int) error {
	if n < 0 || n > r.remaining() {
		return fmt.Errorf("string length %d exceeds remaining bytes %d", n, r.remaining())
	}
	buf, err := r.take(n)
	if err != nil {
		return err
	}
	return writeJSONString(w, buf)
}
