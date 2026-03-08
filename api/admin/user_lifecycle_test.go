package admin

import "testing"

func TestBuildSoftDeleteBanReason(t *testing.T) {
	got := buildSoftDeleteBanReason(nil)
	if got != softDeleteBanReasonPrefix {
		t.Fatalf("got = %q, want %q", got, softDeleteBanReasonPrefix)
	}

	raw := "  test reason  "
	got = buildSoftDeleteBanReason(&raw)
	if got != softDeleteBanReasonPrefix+" test reason" {
		t.Fatalf("got = %q, want %q", got, softDeleteBanReasonPrefix+" test reason")
	}
}

func TestValidateAdminPasswordInput(t *testing.T) {
	if err := validateAdminPasswordInput("1234567"); err == nil {
		t.Fatalf("expected short password to fail")
	}
	if err := validateAdminPasswordInput("12345678"); err != nil {
		t.Fatalf("expected valid password to pass, got: %v", err)
	}
	tooLong := make([]byte, 73)
	for i := range tooLong {
		tooLong[i] = 'a'
	}
	if err := validateAdminPasswordInput(string(tooLong)); err == nil {
		t.Fatalf("expected too long password to fail")
	}
}

func TestGenerateTemporaryPassword(t *testing.T) {
	value, err := generateTemporaryPassword()
	if err != nil {
		t.Fatalf("generateTemporaryPassword returned error: %v", err)
	}
	if len(value) < 10 {
		t.Fatalf("temporary password too short: %q", value)
	}
	if value[:4] != "Tmp-" {
		t.Fatalf("temporary password prefix mismatch: %q", value)
	}
}
