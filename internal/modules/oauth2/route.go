package oauth2

import (
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterOAuth2Routes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	registerHydraOAuth2Routes(apiHelper)

	registerOAuth2UserInfoRoutes(apiHelper)

	registerOAuth2GameDataRoutes(apiHelper)
}
