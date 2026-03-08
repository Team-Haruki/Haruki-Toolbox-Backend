package admin

import (
	"strconv"
	"testing"
)

func TestSanitizeBatchUserIDs(t *testing.T) {
	t.Run("trim and dedupe", func(t *testing.T) {
		got, err := sanitizeBatchUserIDs([]string{" 1001 ", "1002", "1001", " "})
		if err != nil {
			t.Fatalf("sanitizeBatchUserIDs returned error: %v", err)
		}
		if len(got) != 2 || got[0] != "1001" || got[1] != "1002" {
			t.Fatalf("unexpected result: %#v", got)
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		if _, err := sanitizeBatchUserIDs([]string{" ", ""}); err == nil {
			t.Fatalf("expected error for empty userIds")
		}
	})

	t.Run("too many rejected", func(t *testing.T) {
		values := make([]string, 0, maxBatchUserOperationCount+1)
		for i := 0; i < maxBatchUserOperationCount+1; i++ {
			values = append(values, strconv.Itoa(100000+i))
		}
		if _, err := sanitizeBatchUserIDs(values); err == nil {
			t.Fatalf("expected error for too many userIds")
		}
	})
}
