package upload

import (
	harukiUtils "haruki-suite/utils"
	"testing"
	"time"
)

func TestParseIOSProxyPathInt(t *testing.T) {
	t.Parallel()

	t.Run("parse decimal id", func(t *testing.T) {
		t.Parallel()
		got, err := parseIOSProxyPathInt("08")
		if err != nil {
			t.Fatalf("parseIOSProxyPathInt returned error: %v", err)
		}
		if got != 8 {
			t.Fatalf("value = %d, want 8", got)
		}
	})

	t.Run("reject invalid id", func(t *testing.T) {
		t.Parallel()
		if _, err := parseIOSProxyPathInt("abc"); err == nil {
			t.Fatalf("expected parse error")
		}
	})
}

func TestCloneDataChunks(t *testing.T) {
	t.Parallel()

	now := time.Now()
	original := []harukiUtils.DataChunk{{
		ChunkIndex: 1,
		Data:       []byte{1, 2, 3},
		Time:       now,
	}}

	cloned := cloneDataChunks(original)
	if len(cloned) != 1 {
		t.Fatalf("len(cloned) = %d, want 1", len(cloned))
	}
	if &cloned[0] == &original[0] {
		t.Fatalf("chunk struct should be copied")
	}
	if len(cloned[0].Data) != 3 {
		t.Fatalf("len(cloned[0].Data) = %d, want 3", len(cloned[0].Data))
	}

	cloned[0].Data[0] = 9
	if original[0].Data[0] != 1 {
		t.Fatalf("modifying clone should not mutate original data")
	}
}
