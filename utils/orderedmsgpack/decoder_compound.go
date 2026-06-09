package orderedmsgpack

import (
	"bytes"
	"fmt"

	"github.com/iancoleman/orderedmap"
)

func decodeMap(r *bytes.Reader, n int) (*orderedmap.OrderedMap, error) {
	om := orderedmap.New()
	om.SetEscapeHTML(false)
	for i := range n {
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

func decodeArray(r *bytes.Reader, n int) ([]any, error) {
	arr := make([]any, n)
	for i := range n {
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

func decodeExt8(r *bytes.Reader) (any, error) {
	size, err := readUint8(r)
	if err != nil {
		return nil, err
	}
	return decodeExtPayload(r, int(size))
}

func decodeExt16(r *bytes.Reader) (any, error) {
	size, err := readUint16(r)
	if err != nil {
		return nil, err
	}
	return decodeExtPayload(r, int(size))
}

func decodeExt32(r *bytes.Reader) (any, error) {
	size, err := readUint32(r)
	if err != nil {
		return nil, err
	}
	return decodeExtPayload(r, int(size))
}

func decodeFixExt(r *bytes.Reader, size int) (any, error) {
	return decodeExtPayload(r, size)
}

func decodeExtPayload(r *bytes.Reader, size int) (any, error) {
	_, err := r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("read ext type: %w", err)
	}

	data, err := readBytes(r, size)
	if err != nil {
		return nil, fmt.Errorf("read ext data: %w", err)
	}

	return data, nil
}
