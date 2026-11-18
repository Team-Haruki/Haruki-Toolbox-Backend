// Package msgpackcodec shares checked MessagePack byte reads between structural
// validation, ordered decoding and streaming JSON output. OrderedMap storage
// lives in the independent orderedmap package.
//
// WriteJSON uses the upload depth limit and strict JSON object keys. DecodeOrdered
// preserves the legacy ordered contract: non-string keys are stringified,
// duplicate keys replace their first position, and extensions become copied
// payload bytes without their type tag. These are distinct output policies.
//
// Unmarshal into other Go types delegates to the existing MessagePack library.
// ValidateMaxDepth checks structure for typed destinations. ValidateGenericDecode
// also checks map keys before decoding uploads into generic values. Both checks
// run before the decoder; neither transfers input ownership. Reader compatibility
// APIs require a caller-bounded reader. MarshalWrite buffers before writing.
package msgpackcodec
