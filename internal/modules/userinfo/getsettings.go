package userinfo

import (
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func loadCurrentUserData(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (*harukiAPIHelper.HarukiToolboxUserData, error) {
	ctx := c.Context()
	userID, err := userCoreModule.CurrentUserID(c)
	if err != nil {
		return nil, harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
	}
	user, err := apiHelper.DBManager.DB.User.
		Query().
		Where(userSchema.IDEQ(userID)).
		WithSocialPlatformInfo().
		WithAuthorizedSocialPlatforms().
		WithGameAccountBindings().
		WithIosScriptCode().
		Only(ctx)
	if err != nil {
		if postgresql.IsNotFound(err) {
			return nil, harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
		}
		return nil, harukiAPIHelper.ErrorInternal(c, "failed to query user settings")
	}
	var emailVerifiedOverride *bool
	if verified, ok := c.Locals("emailVerified").(bool); ok {
		emailVerifiedOverride = &verified
	} else if identityID, ok := c.Locals("identityID").(string); ok && strings.TrimSpace(identityID) != "" {
		fallback := false
		emailVerifiedOverride = &fallback
	}
	ud := harukiAPIHelper.BuildUserDataFromDBUserWithEmailVerified(user, nil, emailVerifiedOverride)
	if displayName, ok := c.Locals("displayName").(string); ok {
		trimmed := strings.TrimSpace(displayName)
		if trimmed != "" {
			ud.Name = &trimmed
		}
	}
	return &ud, nil
}

func handleGetMe(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ud, err := loadCurrentUserData(c, apiHelper)
		if err != nil {
			return err
		}
		return harukiAPIHelper.SuccessResponse(c, "success get current user", ud)
	}
}

func handleGetSettings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ud, err := loadCurrentUserData(c, apiHelper)
		if err != nil {
			return err
		}
		return harukiAPIHelper.SuccessResponse(c, "success get latest settings", ud)
	}
}
