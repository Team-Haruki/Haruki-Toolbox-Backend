package oauth2

import (
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
)

func RegisterOAuth2Routes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	registerHydraOAuth2Routes(apiHelper)

	registerOAuth2UserInfoRoutes(apiHelper)

	registerOAuth2GameDataRoutes(apiHelper)
}
