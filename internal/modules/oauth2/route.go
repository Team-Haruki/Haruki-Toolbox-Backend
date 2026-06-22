package oauth2

import (
	"context"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
)

func RegisterOAuth2Routes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	// Let the bearer-token middleware reject tokens whose client has been disabled
	// (wired here to avoid utils/oauth2 importing this module).
	harukiOAuth2.OAuth2ClientActiveChecker = func(ctx context.Context, clientID string) (bool, error) {
		client, err := GetHydraOAuthClient(ctx, clientID)
		if err != nil {
			if IsHydraNotFoundError(err) {
				return false, nil
			}
			return false, err
		}
		return HydraOAuthClientActive(client), nil
	}

	registerHydraOAuth2Routes(apiHelper)

	registerOAuth2UserInfoRoutes(apiHelper)

	registerOAuth2GameDataRoutes(apiHelper)
}
