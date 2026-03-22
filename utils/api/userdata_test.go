package api

import (
	"haruki-suite/utils/database/postgresql"
	"testing"
)

func TestBuildUserDataFromDBUserIncludesRole(t *testing.T) {
	dbUser := &postgresql.User{
		Name:           "tester",
		ID:             "10001",
		Role:           "admin",
		AllowCnMysekai: true,
	}

	ud := BuildUserDataFromDBUser(dbUser, nil)
	if ud.Role == nil {
		t.Fatalf("Role should not be nil")
	}
	if *ud.Role != "admin" {
		t.Fatalf("Role = %q, want %q", *ud.Role, "admin")
	}
}

func TestBuildUserDataFromDBUserWithEmailVerifiedUsesOverride(t *testing.T) {
	dbUser := &postgresql.User{
		Name:             "tester",
		ID:               "10002",
		Email:            "tester@example.com",
		KratosIdentityID: strPtr("kratos-identity-1"),
	}
	emailVerified := true

	ud := BuildUserDataFromDBUserWithEmailVerified(dbUser, nil, &emailVerified)
	if ud.EmailInfo == nil {
		t.Fatalf("EmailInfo should not be nil")
	}
	if !ud.EmailInfo.Verified {
		t.Fatalf("EmailInfo.Verified = %v, want %v", ud.EmailInfo.Verified, true)
	}
}

func TestBuildUserDataFromDBUserKratosFallbackEmailVerifiedTrue(t *testing.T) {
	dbUser := &postgresql.User{
		Name:             "tester",
		ID:               "10003",
		Email:            "tester@example.com",
		KratosIdentityID: strPtr("kratos-identity-2"),
	}

	ud := BuildUserDataFromDBUser(dbUser, nil)
	if ud.EmailInfo == nil {
		t.Fatalf("EmailInfo should not be nil")
	}
	if !ud.EmailInfo.Verified {
		t.Fatalf("EmailInfo.Verified = %v, want %v", ud.EmailInfo.Verified, true)
	}
}

func TestBuildUserDataFromDBUserWithEmailVerifiedUsesFalseOverride(t *testing.T) {
	dbUser := &postgresql.User{
		Name:             "tester",
		ID:               "10004",
		Email:            "tester@example.com",
		KratosIdentityID: strPtr("kratos-identity-3"),
	}
	emailVerified := false

	ud := BuildUserDataFromDBUserWithEmailVerified(dbUser, nil, &emailVerified)
	if ud.EmailInfo == nil {
		t.Fatalf("EmailInfo should not be nil")
	}
	if ud.EmailInfo.Verified {
		t.Fatalf("EmailInfo.Verified = %v, want %v", ud.EmailInfo.Verified, false)
	}
}

func strPtr(value string) *string {
	return &value
}
