package nuverserestore

import (
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"
)

func TestRestoreColumnsMapsEnumAndTruncatesToShortestColumn(t *testing.T) {
	t.Parallel()

	rows := RestoreColumns(
		[]CompactColumn{
			{Key: "id", Values: []any{1, 2, 3}},
			{Key: "state", Values: []any{0, 1}},
		},
		map[string][]any{
			"state": {"inactive", "active"},
		},
		CompactOptions{InvalidEnumValue: NullInvalidEnumValue},
	)

	if len(rows) != 2 {
		t.Fatalf("rows length = %d, want 2", len(rows))
	}
	if rows[0][0].Value != 1 || rows[0][1].Value != "inactive" {
		t.Fatalf("first row = %#v", rows[0])
	}
	if rows[1][0].Value != 2 || rows[1][1].Value != "active" {
		t.Fatalf("second row = %#v", rows[1])
	}
}

func TestRestoreEnumColumnInvalidMode(t *testing.T) {
	t.Parallel()

	values := []any{0, 99, "1", jsonvalue.Number("0"), nil}
	enumValues := []any{"inactive", "active"}

	nullInvalid := RestoreEnumColumn(values, enumValues, CompactOptions{
		InvalidEnumValue:     NullInvalidEnumValue,
		ParseStringEnumIndex: true,
	})
	if nullInvalid[0] != "inactive" || nullInvalid[1] != nil ||
		nullInvalid[2] != "active" || nullInvalid[3] != "inactive" || nullInvalid[4] != nil {
		t.Fatalf("null invalid enum restore = %#v", nullInvalid)
	}

	preserveInvalid := RestoreEnumColumn(values, enumValues, CompactOptions{
		InvalidEnumValue:     PreserveInvalidEnumValue,
		ParseStringEnumIndex: true,
	})
	if preserveInvalid[0] != "inactive" || preserveInvalid[1] != 99 ||
		preserveInvalid[2] != "active" || preserveInvalid[3] != "inactive" || preserveInvalid[4] != nil {
		t.Fatalf("preserve invalid enum restore = %#v", preserveInvalid)
	}
}
