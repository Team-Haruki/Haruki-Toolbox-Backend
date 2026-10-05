package upload

import "testing"

func TestClientPolicy(t *testing.T) {
	for _, s := range []string{"1.2", "01.2.3", "1.2.3-01", "v3.0.0", "3.0.0+", "3.0.0-"} {
		if _, _, err := ParseClientVersion(s); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	for _, s := range []string{"3.0.0-preview.1+gabcdef", "3.0.0+001", "3.0.0-x-y-z.1"} {
		if _, _, err := ParseClientVersion(s); err != nil {
			t.Fatalf("rejected %s: %v", s, err)
		}
	}
	for _, tc := range []struct {
		channels []string
		minimum  map[string]string
	}{
		{nil, nil}, {[]string{}, map[string]string{}}, {[]string{"stable", "stable"}, map[string]string{"stable": "3.0.0", "preview": "3.0.0-preview"}},
		{[]string{"preview"}, map[string]string{"preview": "3.0.0-beta"}}, {[]string{"stable"}, map[string]string{"stable": "3.0.0+build"}},
	} {
		if _, err := CompileClientPolicy(tc.channels, tc.minimum); err == nil {
			t.Fatal("accepted invalid policy")
		}
	}
	p := DefaultClientPolicy()
	v, _, _ := ParseClientVersion("3.0.0-preview.2+build")
	minimum, ok := p.Minimum("preview")
	if !ok || v.LessThan(minimum) {
		t.Fatal("preview rejected by stable floor")
	}
}
