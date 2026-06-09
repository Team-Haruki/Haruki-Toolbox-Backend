package orderedmsgpack

import (
	"fmt"
	"io"

	"github.com/iancoleman/orderedmap"
	"github.com/shamaton/msgpack/v3"
)

func Marshal(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}

func Unmarshal(data []byte, v any) error {
	return msgpack.Unmarshal(data, v)
}

func MarshalWrite(w io.Writer, v any) error {
	return msgpack.MarshalWrite(w, v)
}

func UnmarshalRead(r io.Reader, v any) error {
	return msgpack.UnmarshalRead(r, v)
}

func MsgpackToOrderedMap(b []byte) (*orderedmap.OrderedMap, error) {
	om, err := decodeOrderedMapBytes(b)
	if err != nil {
		return nil, fmt.Errorf("decode msgpack: %w", err)
	}
	return om, nil
}

func MsgpackToOrderedMapFromReader(r io.Reader) (*orderedmap.OrderedMap, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read all: %w", err)
	}
	return MsgpackToOrderedMap(data)
}
