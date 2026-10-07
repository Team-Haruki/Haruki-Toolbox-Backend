package oauth2

import (
	"context"
	"encoding/json/jsontext"
	"testing"
)

// ext is consent session data, not ours to trust: an unexpected shape must
// never fail bearer authentication, only drop the label.
func TestIntrospectionDeviceLabelIsLenient(t *testing.T) {
	cases := map[string]string{
		``:                                  "",
		`null`:                              "",
		`{}`:                                "",
		`"text"`:                            "",
		`[1,2]`:                             "",
		`{"device_label":null}`:             "",
		`{"device_label":42}`:               "",
		`{"device_label":{"x":1}}`:          "",
		`{"device_label":" Home server "}`:  "Home server",
		`{"other":1,"device_label":"Desk"}`: "Desk",
	}
	for ext, want := range cases {
		if got := introspectionDeviceLabel(jsontext.Value(ext)); got != want {
			t.Errorf("introspectionDeviceLabel(%s) = %q, want %q", ext, got, want)
		}
	}
}

// Without the user database a subject would resolve to itself, and without a
// client check a disabled client would pass, so both are required.
func TestIntrospectAccessTokenRequiresDatabaseAndClientCheck(t *testing.T) {
	checker := func(context.Context, string) (bool, error) { return true, nil }
	if _, err := IntrospectAccessToken(context.Background(), NewHydraConfig(HydraConfigOptions{}), nil, "ory_at_x", checker); err == nil {
		t.Fatal("introspection without a database answered")
	}
}
