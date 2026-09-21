package orderedmap

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
)

// MarshalJSONTo writes members directly to the enclosing v2 stream. It avoids
// serializing every member to an intermediate buffer and preserves key order.
func (m OrderedMap) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, e := range m.entries {
		if err := enc.WriteToken(jsontext.String(e.key)); err != nil {
			return err
		}
		if err := json.MarshalEncode(enc, e.value); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}
