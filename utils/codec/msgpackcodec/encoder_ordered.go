package msgpackcodec

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/orderedmap"
	"github.com/shamaton/msgpack/v3"
)

// appendOrdered preserves insertion order for the tree shapes returned by the
// byte decoder. Other Go values retain the msgpack library's encoding rules.
func appendOrdered(dst []byte, value any, depth int) ([]byte, error) {
	if depth > maxDecodeDepth {
		return nil, fmt.Errorf("msgpack nesting exceeds %d", maxDecodeDepth)
	}
	switch v := value.(type) {
	case *orderedmap.OrderedMap:
		if v == nil {
			return append(dst, msgpackNil), nil
		}
		dst, err := appendContainerHeader(dst, v.Len(), true)
		if err != nil {
			return nil, err
		}
		for key, value := range v.All() {
			encodedKey, err := msgpack.Marshal(key)
			if err != nil {
				return nil, err
			}
			dst = append(dst, encodedKey...)
			dst, err = appendOrdered(dst, value, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return dst, nil
	case orderedmap.OrderedMap:
		return appendOrdered(dst, &v, depth)
	case []any:
		if v == nil {
			return append(dst, msgpackNil), nil
		}
		dst, err := appendContainerHeader(dst, len(v), false)
		if err != nil {
			return nil, err
		}
		for _, value := range v {
			dst, err = appendOrdered(dst, value, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return dst, nil
	case map[string]any:
		if v == nil {
			return append(dst, msgpackNil), nil
		}
		dst, err := appendContainerHeader(dst, len(v), true)
		if err != nil {
			return nil, err
		}
		for key, value := range v {
			encodedKey, err := msgpack.Marshal(key)
			if err != nil {
				return nil, err
			}
			dst = append(dst, encodedKey...)
			dst, err = appendOrdered(dst, value, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return dst, nil
	default:
		encoded, err := msgpack.Marshal(value)
		return append(dst, encoded...), err
	}
}

func appendContainerHeader(dst []byte, n int, object bool) ([]byte, error) {
	if n < 0 || uint64(n) > math.MaxUint32 {
		return nil, fmt.Errorf("msgpack container too large")
	}
	fix, code16, code32 := byte(msgpackFixArrMin), byte(msgpackArray16), byte(msgpackArray32)
	if object {
		fix, code16, code32 = msgpackFixMapMin, msgpackMap16, msgpackMap32
	}
	switch {
	case n < 16:
		return append(dst, fix|byte(n)), nil
	case n <= math.MaxUint16:
		return binary.BigEndian.AppendUint16(append(dst, code16), uint16(n)), nil
	default:
		return binary.BigEndian.AppendUint32(append(dst, code32), uint32(n)), nil
	}
}
