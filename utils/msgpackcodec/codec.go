package msgpackcodec

import (
	"fmt"
	"io"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/orderedmap"
	"github.com/shamaton/msgpack/v3"
)

// Marshal encodes a value, preserving OrderedMap insertion order. Ordinary Go
// maps retain the legacy encoder behavior and do not promise stable ordering.
func Marshal(v any) ([]byte, error) {
	return appendOrdered(nil, v, 0)
}

// Unmarshal supports ordered destinations and the legacy generic decoder.
// For untrusted generic destinations, call ValidateMaxDepth before Unmarshal.
func Unmarshal(data []byte, v any) error {
	switch out := v.(type) {
	case *orderedmap.OrderedMap:
		if out == nil {
			return fmt.Errorf("nil ordered map destination")
		}
		decoded, err := DecodeOrdered(data)
		if err != nil {
			return err
		}
		*out = *decoded
		return nil
	case **orderedmap.OrderedMap:
		if out == nil {
			return fmt.Errorf("nil ordered map destination")
		}
		decoded, err := DecodeOrdered(data)
		if err != nil {
			return err
		}
		*out = decoded
		return nil
	default:
		return msgpack.Unmarshal(data, v)
	}
}

// MarshalWrite buffers a complete encoding before writing it to w.
func MarshalWrite(w io.Writer, v any) error {
	data, err := Marshal(v)
	if err != nil {
		return err
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}

// UnmarshalRead retains the reader compatibility path. The caller must bound r
// and validate untrusted generic input before using the underlying decoder.
func UnmarshalRead(r io.Reader, v any) error {
	switch v.(type) {
	case *orderedmap.OrderedMap, **orderedmap.OrderedMap:
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		return Unmarshal(data, v)
	default:
		return msgpack.UnmarshalRead(r, v)
	}
}

// DecodeOrdered decodes exactly one top-level map with a depth limit of 512.
// It owns decoded string/binary storage, stringifies non-string keys, and keeps
// the first insertion position when a duplicate key replaces its value.
func DecodeOrdered(b []byte) (*orderedmap.OrderedMap, error) {
	om, err := decodeOrderedMapBytes(b)
	if err != nil {
		return nil, fmt.Errorf("decode msgpack: %w", err)
	}
	return om, nil
}

// DecodeOrderedRead reads the complete input before ordered decoding. The
// caller must provide a bounded reader; this API is not incremental decoding.
func DecodeOrderedRead(r io.Reader) (*orderedmap.OrderedMap, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read all: %w", err)
	}
	return DecodeOrdered(data)
}
