package manager

import "testing"

func TestValidateNoMongoOperatorKeysRejectsDotsAndDollarAnywhere(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "leading dollar", value: map[string]any{"$where": "x"}},
		{name: "embedded dollar", value: map[string]any{"profile$token": "x"}},
		{name: "dotted key", value: map[string]any{"profile.name": "x"}},
		{name: "nested array key", value: map[string]any{"items": []any{map[string]any{"safe$unsafe": true}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateNoMongoOperatorKeys(test.value); err == nil {
				t.Fatal("expected unsafe Mongo field name to be rejected")
			}
		})
	}
}

func TestValidateNoMongoOperatorKeysAcceptsSafeNestedData(t *testing.T) {
	t.Parallel()

	value := map[string]any{
		"profile": map[string]any{
			"display_name": "Haruki",
			"items":        []any{map[string]any{"itemId": int64(1)}},
		},
	}
	if err := validateNoMongoOperatorKeys(value); err != nil {
		t.Fatalf("safe data rejected: %v", err)
	}
}
