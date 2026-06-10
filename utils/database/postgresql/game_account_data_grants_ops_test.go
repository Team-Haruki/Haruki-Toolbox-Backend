package postgresql_test

import (
	"context"
	"testing"
	"time"

	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	_ "github.com/mattn/go-sqlite3"
)

func createGrantTestUser(t *testing.T, client *dbManager.Client, id string, banned bool) {
	t.Helper()
	builder := client.User.Create().
		SetID(id).
		SetName(id).
		SetEmail(id + "@example.com")
	if banned {
		builder.SetBanned(true)
	}
	if _, err := builder.Save(context.Background()); err != nil {
		t.Fatalf("create user %s returned error: %v", id, err)
	}
}

func TestGameAccountDataGrantAccessAndCleanup(t *testing.T) {
	t.Parallel()

	client := enttest.Open(t, "sqlite3", "file:game-account-data-grants-test?mode=memory&cache=shared&_fk=1")
	defer func() {
		_ = client.Close()
	}()

	createGrantTestUser(t, client, "owner", false)
	createGrantTestUser(t, client, "grantee", false)
	createGrantTestUser(t, client, "banned", true)

	if _, err := client.GameAccountBinding.Create().
		SetServer("jp").
		SetGameUserID("123").
		SetVerified(true).
		SetUserID("owner").
		Save(context.Background()); err != nil {
		t.Fatalf("create binding returned error: %v", err)
	}

	now := time.Date(2026, time.June, 10, 12, 0, 0, 0, time.UTC)
	ownerAccess, err := client.CanAccessGameAccountData(context.Background(), "owner", "jp", "123", "profile", now)
	if err != nil {
		t.Fatalf("owner CanAccessGameAccountData returned error: %v", err)
	}
	if ownerAccess == nil || !ownerAccess.Allowed || ownerAccess.ViaGrant {
		t.Fatalf("unexpected owner access: %+v", ownerAccess)
	}

	if _, err := client.UpsertGameAccountDataGrant(context.Background(), "owner", "grantee", "jp", "123", "suite", now.Add(time.Hour)); err != nil {
		t.Fatalf("UpsertGameAccountDataGrant returned error: %v", err)
	}
	granteeAccess, err := client.CanAccessGameAccountData(context.Background(), "grantee", "jp", "123", "suite", now)
	if err != nil {
		t.Fatalf("grantee CanAccessGameAccountData returned error: %v", err)
	}
	if granteeAccess == nil || !granteeAccess.Allowed || !granteeAccess.ViaGrant {
		t.Fatalf("unexpected grantee access: %+v", granteeAccess)
	}

	profileAccess, err := client.CanAccessGameAccountData(context.Background(), "grantee", "jp", "123", "profile", now)
	if err != nil {
		t.Fatalf("profile grant lookup returned error: %v", err)
	}
	if profileAccess == nil || profileAccess.Allowed {
		t.Fatalf("profile should not be granted, got %+v", profileAccess)
	}

	if _, err := client.UpsertGameAccountDataGrant(context.Background(), "owner", "banned", "jp", "123", "suite", now.Add(time.Hour)); err != nil {
		t.Fatalf("upsert banned grantee grant returned error: %v", err)
	}
	bannedAccess, err := client.CanAccessGameAccountData(context.Background(), "banned", "jp", "123", "suite", now)
	if err != nil {
		t.Fatalf("banned grant lookup returned error: %v", err)
	}
	if bannedAccess == nil || bannedAccess.Allowed {
		t.Fatalf("banned grantee should not be granted, got %+v", bannedAccess)
	}

	if _, err := client.UpsertGameAccountDataGrant(context.Background(), "owner", "grantee", "jp", "123", "mysekai", now.Add(-time.Hour)); err != nil {
		t.Fatalf("upsert expired grant returned error: %v", err)
	}
	deleted, err := client.CleanupExpiredGameAccountDataGrants(context.Background(), now)
	if err != nil {
		t.Fatalf("CleanupExpiredGameAccountDataGrants returned error: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
}
