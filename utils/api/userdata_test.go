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
