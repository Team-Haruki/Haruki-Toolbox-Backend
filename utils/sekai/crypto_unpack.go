package sekai

import (
	"fmt"

	"haruki-suite/utils/orderedmsgpack"

	"github.com/iancoleman/orderedmap"
	"github.com/shamaton/msgpack/v2"
)

func (c *SekaiCryptor) UnpackInto(content []byte, out any) error {
	if out == nil {
		return fmt.Errorf("out must be a non-nil pointer")
	}

	unpadded, pooled, err := c.decryptToPooledMsgpack(content)
	if err != nil {
		return err
	}
	defer releasePooledBytes(pooled)

	switch dst := out.(type) {
	case *orderedmap.OrderedMap:
		om, err := orderedmsgpack.MsgpackToOrderedMap(unpadded)
		if err != nil {
			return fmt.Errorf("ordered decode: %w", err)
		}
		om.SetEscapeHTML(false)
		*dst = *om
	case **orderedmap.OrderedMap:
		om, err := orderedmsgpack.MsgpackToOrderedMap(unpadded)
		if err != nil {
			return fmt.Errorf("ordered (**ptr) decode: %w", err)
		}
		*dst = om
	default:
		if err := msgpack.Unmarshal(unpadded, out); err != nil {
			preview := unpadded
			if len(preview) > 200 {
				preview = preview[:200]
			}
			return fmt.Errorf(
				"msgpack decode (len=%d, target=%T, first200=%x): %w",
				len(unpadded),
				out,
				preview,
				err,
			)
		}
	}

	return nil
}

func (c *SekaiCryptor) Unpack(content []byte) (any, error) {
	var mapResult map[string]any
	if err := c.UnpackInto(content, &mapResult); err == nil {
		sanitizeMapValues(mapResult)
		return mapResult, nil
	}

	var sliceResult []any
	if err := c.UnpackInto(content, &sliceResult); err == nil {
		sanitizeSliceValues(sliceResult)
		return sliceResult, nil
	}

	var anyResult any
	if err := c.UnpackInto(content, &anyResult); err != nil {
		return nil, err
	}
	return convertUnpackResult(anyResult), nil
}

func sanitizeMapValues(m map[string]any) {
	for k, v := range m {
		switch child := v.(type) {
		case map[any]any:
			m[k] = convertUnpackResult(child)
		case map[string]any:
			sanitizeMapValues(child)
		case []any:
			sanitizeSliceValues(child)
		}
	}
}

func sanitizeSliceValues(s []any) {
	for i, v := range s {
		switch child := v.(type) {
		case map[any]any:
			s[i] = convertUnpackResult(child)
		case map[string]any:
			sanitizeMapValues(child)
		case []any:
			sanitizeSliceValues(child)
		}
	}
}

func convertUnpackResult(v any) any {
	switch x := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			if keyStr, ok := k.(string); ok {
				m[keyStr] = convertUnpackResult(val)
			} else if keyBytes, ok := k.([]byte); ok {
				m[string(keyBytes)] = convertUnpackResult(val)
			}
		}
		return m
	case map[string]any:
		sanitizeMapValues(x)
		return x
	case []any:
		sanitizeSliceValues(x)
		return x
	default:
		return v
	}
}

func (c *SekaiCryptor) UnpackOrdered(content []byte) (*orderedmap.OrderedMap, error) {
	result := orderedmap.New()
	result.SetEscapeHTML(false)
	if err := c.UnpackInto(content, result); err != nil {
		return nil, err
	}
	return result, nil
}
