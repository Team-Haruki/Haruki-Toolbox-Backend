package filtering

import (
	"reflect"
	"testing"
)

func TestParseCSVValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "empty input",
			raw:  "",
			want: nil,
		},
		{
			name: "whitespace input",
			raw:  "   ",
			want: nil,
		},
		{
			name: "trim and deduplicate",
			raw:  " jp, en, jp ,tw,,en ",
			want: []string{"jp", "en", "tw"},
		},
		{
			name: "single value",
			raw:  "manual",
			want: []string{"manual"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseCSVValues(tc.raw)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseCSVValues(%q) = %#v, want %#v", tc.raw, got, tc.want)
			}
		})
	}
}
