// Package msgpackcodec shares checked MessagePack byte reads between structural
// validation, ordered decoding and streaming JSON output. OrderedMap storage
// lives in the independent orderedmap package.
//
// WriteJSON uses the upload depth limit and strict JSON object keys. DecodeOrdered
// preserves the legacy ordered contract: non-string keys are stringified,
// duplicate keys replace their first position, and extensions become copied
// payload bytes without their type tag. These are distinct output policies.
//
// Unmarshal into other Go types delegates to the existing MessagePack library;
// callers must ValidateMaxDepth before using that compatibility path with
// untrusted input. Reader compatibility APIs read a complete document and must
// be supplied a caller-bounded reader. MarshalWrite buffers before writing.
package msgpackcodec
