package admincore

import (
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	_ "github.com/mattn/go-sqlite3"
)

func TestScopeSystemLogsForAdminActorHidesSuperAdminActorsAndTargets(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:admin-core-visibility?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	for _, seed := range []struct {
		id   string
		role userSchema.Role
	}{
		{id: "user-1", role: userSchema.RoleUser},
		{id: "super-1", role: userSchema.RoleSuperAdmin},
	} {
		if _, err := client.User.Create().SetID(seed.id).SetName(seed.id).SetEmail(seed.id + "@example.com").SetRole(seed.role).Save(t.Context()); err != nil {
			t.Fatalf("seed user %s: %v", seed.id, err)
		}
	}

	seedLog := func(action string, actorUserID *string, actorRole *string, targetUserID *string) {
		builder := client.SystemLog.Create().SetAction(action)
		if actorUserID != nil {
			builder.SetActorUserID(*actorUserID)
		}
		if actorRole != nil {
			builder.SetActorRole(*actorRole)
		}
		if targetUserID != nil {
			builder.SetTargetType("user").SetTargetID(*targetUserID)
		}
		if _, err := builder.Save(t.Context()); err != nil {
			t.Fatalf("seed log %s: %v", action, err)
		}
	}
	adminRole := RoleAdmin
	superRole := RoleSuperAdmin
	superActorID := "super-1"
	normalTarget := "user-1"
	superTarget := "super-1"
	seedLog("visible-admin-action", nil, &adminRole, &normalTarget)
	seedLog("hidden-super-actor", &superActorID, &superRole, &normalTarget)
	seedLog("hidden-super-actor-without-role", &superActorID, nil, &normalTarget)
	seedLog("hidden-super-target", nil, nil, &superTarget)
	seedLog("visible-system-action", nil, nil, nil)

	adminQuery, err := ScopeSystemLogsForAdminActor(t.Context(), client, client.SystemLog.Query(), RoleAdmin)
	if err != nil {
		t.Fatalf("ScopeSystemLogsForAdminActor(admin): %v", err)
	}
	adminRows, err := adminQuery.All(t.Context())
	if err != nil {
		t.Fatalf("query scoped admin logs: %v", err)
	}
	visibleActions := make(map[string]bool, len(adminRows))
	for _, row := range adminRows {
		visibleActions[row.Action] = true
	}
	if len(adminRows) != 2 || !visibleActions["visible-admin-action"] || !visibleActions["visible-system-action"] {
		t.Fatalf("admin visible actions = %v, want only non-super-admin rows", visibleActions)
	}

	superQuery, err := ScopeSystemLogsForAdminActor(t.Context(), client, client.SystemLog.Query(), RoleSuperAdmin)
	if err != nil {
		t.Fatalf("ScopeSystemLogsForAdminActor(super_admin): %v", err)
	}
	count, err := superQuery.Count(t.Context())
	if err != nil {
		t.Fatalf("count super-admin logs: %v", err)
	}
	if count != 5 {
		t.Fatalf("super-admin visible log count = %d, want 5", count)
	}
}
