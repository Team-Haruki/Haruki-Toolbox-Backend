package oauth2

import (
	"context"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
)

type RouteOptions struct {
	HydraConfig   *harukiOAuth2.HydraConfig
	AvatarBaseURL string
}

func RegisterOAuth2Routes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, options RouteOptions) {
	registerHydraOAuth2Routes(apiHelper, options.HydraConfig)

	registerOAuth2UserInfoRoutes(apiHelper, options.HydraConfig, options.AvatarBaseURL)

	registerOAuth2GameDataRoutes(apiHelper, options.HydraConfig)
}

func checkHydraOAuth2ClientActive(hydraConfig *harukiOAuth2.HydraConfig) harukiOAuth2.ClientActiveChecker {
	return func(ctx context.Context, clientID string) (bool, error) {
		client, err := GetHydraOAuthClient(ctx, hydraConfig, clientID)
		if err != nil {
			if IsHydraNotFoundError(err) {
				return false, nil
			}
			return false, err
		}
		return HydraOAuthClientActive(client), nil
	}
}
