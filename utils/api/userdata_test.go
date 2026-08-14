package api

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"testing"
)

func TestBuildUserDataFromDBUserIncludesRole(t *testing.T) {
	dbUser := &postgresql.User{
		Name:           "tester",
		ID:             "10001",
		Role:           "admin",
		AllowCnMysekai: true,
	}

	ud := NewUserDataBuilder("https://assets.example.test").BuildFromDBUser(dbUser, nil)
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

	ud := NewUserDataBuilder("https://assets.example.test").BuildFromDBUserWithEmailVerified(dbUser, nil, &emailVerified)
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

	ud := NewUserDataBuilder("https://assets.example.test").BuildFromDBUser(dbUser, nil)
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

	ud := NewUserDataBuilder("https://assets.example.test").BuildFromDBUserWithEmailVerified(dbUser, nil, &emailVerified)
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

func TestUserDataBuilderPreservesAvatarURLJoining(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "regular base URL",
			baseURL: "https://assets.example.test",
			want:    "https://assets.example.test/avatars/avatar.png",
		},
		{
			name:    "trailing slash remains visible",
			baseURL: "https://assets.example.test/",
			want:    "https://assets.example.test//avatars/avatar.png",
		},
		{
			name: "empty base URL",
			want: "/avatars/avatar.png",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			avatarPath := "avatar.png"
			dbUser := &postgresql.User{AvatarPath: &avatarPath}
			userData := NewUserDataBuilder(test.baseURL).BuildFromDBUser(dbUser, nil)
			if userData.AvatarPath == nil {
				t.Fatal("AvatarPath should not be nil")
			}
			if got := *userData.AvatarPath; got != test.want {
				t.Fatalf("AvatarPath = %q, want %q", got, test.want)
			}
		})
	}
}

func TestUserDataBuilderLeavesMissingAvatarEmpty(t *testing.T) {
	userData := NewUserDataBuilder("https://assets.example.test").BuildFromDBUser(&postgresql.User{}, nil)
	if userData.AvatarPath == nil {
		t.Fatal("AvatarPath should not be nil")
	}
	if got := *userData.AvatarPath; got != "" {
		t.Fatalf("AvatarPath = %q, want empty", got)
	}
}
