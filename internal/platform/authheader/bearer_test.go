package authheader

import "testing"

func TestExtractBearerToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		header    string
		wantToken string
		wantOK    bool
	}{
		{name: "standard", header: "Bearer abc.def", wantToken: "abc.def", wantOK: true},
		{name: "lowercase scheme", header: "bearer xyz", wantToken: "xyz", wantOK: true},
		{name: "mixed case scheme", header: "BeArEr tok", wantToken: "tok", wantOK: true},
		{name: "extra spaces", header: "  Bearer    token123   ", wantToken: "token123", wantOK: true},
		{name: "tab separator", header: "Bearer\tabc", wantToken: "abc", wantOK: true},
		{name: "empty", header: "", wantOK: false},
		{name: "wrong scheme", header: "Basic abc", wantOK: false},
		{name: "missing token", header: "Bearer", wantOK: false},
		{name: "too many parts", header: "Bearer one two", wantOK: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotToken, gotOK := ExtractBearerToken(tt.header)
			if gotOK != tt.wantOK {
				t.Fatalf("ExtractBearerToken ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotToken != tt.wantToken {
				t.Fatalf("ExtractBearerToken token = %q, want %q", gotToken, tt.wantToken)
			}
		})
	}
}
