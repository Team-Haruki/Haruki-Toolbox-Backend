package adminusers

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/gofiber/fiber/v3"
)

func TestListUsersSearchesSocialBindingsWithinActorScope(t *testing.T) {
	helper := newAdminUserTicketNotificationTestHelper(t)
	db, ctx := helper.DBManager.DB, t.Context()
	for _, seed := range []struct{ id, role string }{
		{"actor", roleAdmin}, {"owner", roleUser}, {"authorized", roleUser}, {"hidden", roleSuperAdmin}, {"unrelated", roleUser},
	} {
		seedAdminUserTicketNotificationTestUser(t, helper, seed.id, seed.id, seed.id+"@example.test", seed.role, false, false)
	}
	db.SocialPlatformInfo.Create().SetPlatform("qq").SetPlatformUserID("123456789").SetVerified(true).SetUserID("owner").SaveX(ctx)
	// Multiple matching authorizations must not duplicate a user or inflate total.
	for i, owner := range []string{"owner", "authorized", "authorized", "hidden", "actor"} {
		db.AuthorizeSocialPlatformInfo.Create().SetPlatform("qqbot").SetPlatformUserID("QID-123456789").SetPlatformID(i + 1).SetUserID(owner).SaveX(ctx)
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "actor")
		c.Locals("userRole", roleAdmin)
		return c.Next()
	})
	app.Get("/", handleListUsers(helper))
	for _, tc := range []struct {
		query string
		total int
	}{
		{"123456789", 2}, {"qid-123", 2}, {"unrelated@example.test", 1}, {"no-such-account", 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/?q="+tc.query+"&page_size=1", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			var body harukiAPIHelper.GenericResponse[adminUserListResponse]
			if err := json.UnmarshalRead(resp.Body, &body); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 || body.UpdatedData == nil || body.UpdatedData.Total != tc.total {
				t.Fatalf("status=%d data=%+v", resp.StatusCode, body.UpdatedData)
			}
			for _, user := range body.UpdatedData.Items {
				if user.UserID == "hidden" || user.UserID == "actor" {
					t.Fatalf("unauthorized user returned: %s", user.UserID)
				}
			}
		})
	}
}
