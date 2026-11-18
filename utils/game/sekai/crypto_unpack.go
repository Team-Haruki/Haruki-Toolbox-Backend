package sekai

import (
	"bytes"
	"fmt"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/msgpackcodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/orderedmap"
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

	switch out.(type) {
	case *orderedmap.OrderedMap, **orderedmap.OrderedMap:
		return msgpackcodec.Unmarshal(unpadded, out)
	default:
		if err := msgpackcodec.ValidateMaxDepth(unpadded, msgpackcodec.DefaultMaxUploadDepth); err != nil {
			return fmt.Errorf("msgpack validation (len=%d, target=%T): %w", len(unpadded), out, err)
		}
		// An arbitrary typed destination can retain slices of the decoder's
		// input even on error. Give it request-owned bytes.
		if err := unmarshalMsgpack(bytes.Clone(unpadded), out); err != nil {
			return fmt.Errorf("msgpack decode (len=%d, target=%T): %w", len(unpadded), out, err)
		}
		return nil
	}
}

func (c *SekaiCryptor) Unpack(content []byte) (any, error) {
	unpadded, pooled, err := c.decryptToPooledMsgpack(content)
	if err != nil {
		return nil, err
	}
	defer releasePooledBytes(pooled)
	return UnpackMsgpack(unpadded)
}

// UnpackMsgpack decodes already-decrypted bytes. The input must stay unchanged
// until the call returns. The decoded result owns its binary values.
func UnpackMsgpack(content []byte) (any, error) {
	if err := msgpackcodec.ValidateGenericDecode(content); err != nil {
		return nil, fmt.Errorf("msgpack validation: %w", err)
	}
	var mapResult map[string]any
	if err := unmarshalMsgpack(content, &mapResult); err == nil {
		sanitizeMapValues(mapResult)
		return mapResult, nil
	}
	var sliceResult []any
	if err := unmarshalMsgpack(content, &sliceResult); err == nil {
		sanitizeSliceValues(sliceResult)
		return sliceResult, nil
	}
	var result any
	if err := unmarshalMsgpack(content, &result); err != nil {
		return nil, fmt.Errorf("msgpack decode: %w", err)
	}
	return convertUnpackResult(result), nil
}

func unmarshalMsgpack(content []byte, out any) (err error) {
	// Structure and depth are checked by the caller before entering the
	// dependency. Contain unexpected decoder panics without logging plaintext.
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("unexpected panic in msgpack decoder")
		}
	}()
	return msgpackcodec.Unmarshal(content, out)
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
		case []byte:
			m[k] = bytes.Clone(child)
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
		case []byte:
			s[i] = bytes.Clone(child)
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
			}
		}
		return m
	case map[string]any:
		sanitizeMapValues(x)
		return x
	case []any:
		sanitizeSliceValues(x)
		return x
	case []byte:
		return bytes.Clone(x)
	default:
		return v
	}
}

func (c *SekaiCryptor) UnpackOrdered(content []byte) (*orderedmap.OrderedMap, error) {
	// Keep the decoded allocation directly instead of allocating and copying a
	// second map header around its entry storage.
	var result *orderedmap.OrderedMap
	if err := c.UnpackInto(content, &result); err != nil {
		return nil, err
	}
	return result, nil
}
