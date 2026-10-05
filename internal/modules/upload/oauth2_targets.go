package upload

import (
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/gofiber/fiber/v3"
	"time"
)

type uploadTarget struct {
	Server     string     `json:"server"`
	GameUserID string     `json:"gameUserId"`
	DataType   string     `json:"dataType"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
}

func handleOAuthUploadTargets(helper *api.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actor, _ := c.Locals("userID").(string)
		accounts, err := helper.DBManager.DB.ListAccessibleGameAccounts(c.Context(), actor, time.Now().UTC(), "write")
		if err != nil {
			return api.ErrorInternal(c, "failed to query upload targets")
		}
		targets := []uploadTarget{}
		add := func(server, game, kind string, expires *time.Time) error {
			access, err := helper.DBManager.DB.CanWriteGameAccountData(c.Context(), actor, server, game, kind, time.Now().UTC())
			if err != nil {
				return err
			}
			if access.Allowed {
				targets = append(targets, uploadTarget{server, game, kind, expires})
			}
			return nil
		}
		for _, b := range accounts.Owned {
			for _, kind := range []string{"suite", "mysekai"} {
				if err := add(b.Server, b.GameUserID, kind, nil); err != nil {
					return api.ErrorInternal(c, "failed to query upload targets")
				}
			}
		}
		for _, g := range accounts.Grants {
			if err := add(g.Server, g.GameUserID, g.DataType, &g.ExpiresAt); err != nil {
				return api.ErrorInternal(c, "failed to query upload targets")
			}
		}
		return api.Responses.SuccessResponse(c, "ok", &targets)
	}
}
