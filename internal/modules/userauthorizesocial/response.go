package userauthorizesocial

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"

	"github.com/gofiber/fiber/v3"
)

func buildAuthorizedSocialPlatformUserData(
	ctx fiber.Ctx,
	client *postgresql.AuthorizeSocialPlatformInfoClient,
	toolboxUserID string,
) (*harukiAPIHelper.HarukiToolboxUserData, error) {
	infos, err := client.Query().
		Where(authorizesocialplatforminfo.UserID(toolboxUserID)).
		All(ctx.Context())
	if err != nil {
		return nil, err
	}
	resp := make([]harukiAPIHelper.AuthorizeSocialPlatformInfo, 0, len(infos))
	for _, i := range infos {
		resp = append(resp, harukiAPIHelper.AuthorizeSocialPlatformInfo{
			PlatformID:            i.PlatformID,
			Platform:              i.Platform,
			UserID:                i.PlatformUserID,
			Comment:               i.Comment,
			AllowFastVerification: i.AllowFastVerification,
		})
	}
	ud := harukiAPIHelper.HarukiToolboxUserData{
		AuthorizeSocialPlatformInfo: &resp,
	}
	return &ud, nil
}
