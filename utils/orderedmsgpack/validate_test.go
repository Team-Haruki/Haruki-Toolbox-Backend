package orderedmsgpack

import (
	"strings"
	"testing"
)

func TestValidateMaxDepthRejectsDeepNesting(t *testing.T) {
	// A long run of fixarray-of-1 headers is exactly the payload that drives the
	// shamaton decoder into a fatal stack overflow. It must be rejected here.
	data := make([]byte, 5000)
	for i := range data {
		data[i] = msgpackFixArrMin | 0x01 // 0x91
	}
	data = append(data, msgpackNil)

	if err := ValidateMaxDepth(data, DefaultMaxUploadDepth); err == nil {
		t.Fatalf("expected deeply nested payload to be rejected")
	} else if !strings.Contains(err.Error(), "depth") {
		t.Fatalf("expected depth error, got: %v", err)
	}
}

func TestValidateMaxDepthAcceptsNormalPayload(t *testing.T) {
	// {"a": [1, 2], "b": "x"} — a realistic shallow structure.
	data := []byte{
		msgpackFixMapMin | 0x02,
		msgpackFixStrMin | 0x01, 'a',
		msgpackFixArrMin | 0x02, 0x01, 0x02,
		msgpackFixStrMin | 0x01, 'b',
		msgpackFixStrMin | 0x01, 'x',
	}
	if err := ValidateMaxDepth(data, DefaultMaxUploadDepth); err != nil {
		t.Fatalf("expected normal payload to validate, got: %v", err)
	}
}

func TestValidateMaxDepthRejectsOversizedArrayHeader(t *testing.T) {
	// array32 claiming ~4 billion elements with no payload must be rejected
	// before any allocation.
	data := []byte{msgpackArray32, 0xff, 0xff, 0xff, 0xff}
	if err := ValidateMaxDepth(data, DefaultMaxUploadDepth); err == nil {
		t.Fatalf("expected oversized array header to be rejected")
	}
}

func TestValidateMaxDepthRejectsTrailingBytes(t *testing.T) {
	data := []byte{msgpackNil, msgpackNil}
	if err := ValidateMaxDepth(data, DefaultMaxUploadDepth); err == nil {
		t.Fatalf("expected trailing-bytes payload to be rejected")
	}
}
