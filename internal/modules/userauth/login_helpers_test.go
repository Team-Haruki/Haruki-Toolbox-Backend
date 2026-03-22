package userauth

import "testing"

func TestNormalizeAuditRole(t *testing.T) {
	t.Parallel()

	if got := normalizeAuditRole(""); got != "user" {
		t.Fatalf("normalizeAuditRole(\"\") = %q, want %q", got, "user")
	}
	if got := normalizeAuditRole("   "); got != "user" {
		t.Fatalf("normalizeAuditRole(spaces) = %q, want %q", got, "user")
	}
	if got := normalizeAuditRole("ADMIN"); got != "admin" {
		t.Fatalf("normalizeAuditRole(ADMIN) = %q, want %q", got, "admin")
	}
	if got := normalizeAuditRole(" super_admin "); got != "super_admin" {
		t.Fatalf("normalizeAuditRole(super_admin) = %q, want %q", got, "super_admin")
	}
}

func TestIsAdminAuditRole(t *testing.T) {
	t.Parallel()

	if !isAdminAuditRole("admin") {
		t.Fatalf("admin should be admin audit role")
	}
	if !isAdminAuditRole("super_admin") {
		t.Fatalf("super_admin should be admin audit role")
	}
	if isAdminAuditRole("user") {
		t.Fatalf("user should not be admin audit role")
	}
}
