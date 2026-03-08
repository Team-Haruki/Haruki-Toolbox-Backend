package api

import "testing"

func TestPublicAPIAllowedKeysCopySemantics(t *testing.T) {
	helper := &HarukiToolboxRouterHelpers{}

	input := []string{"a", "b"}
	helper.SetPublicAPIAllowedKeys(input)
	input[0] = "mutated"

	stored := helper.GetPublicAPIAllowedKeys()
	if len(stored) != 2 || stored[0] != "a" || stored[1] != "b" {
		t.Fatalf("stored keys mismatch: %#v", stored)
	}

	stored[1] = "changed"
	again := helper.GetPublicAPIAllowedKeys()
	if len(again) != 2 || again[0] != "a" || again[1] != "b" {
		t.Fatalf("GetPublicAPIAllowedKeys leaked internal slice: %#v", again)
	}
}
