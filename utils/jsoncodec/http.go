// Package jsoncodec defines the JSON v2 policy at HTTP and cache boundaries.
package jsoncodec

import json "encoding/json/v2"

// Marshal keeps the established API representation of absent collections.
// Fields use explicit omitzero/omitempty tags to define their wire contract.
func Marshal(value any) ([]byte, error) {
	return json.Marshal(value, json.FormatNilSliceAsNull(true), json.FormatNilMapAsNull(true))
}

// Unmarshal uses v2's strict syntax and case-sensitive field names.
func Unmarshal(data []byte, out any) error { return json.Unmarshal(data, out) }
