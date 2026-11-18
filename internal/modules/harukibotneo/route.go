package harukibotneo

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
)

func RegisterHarukiBotNeoRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	botAPI := apiHelper.Router.Group("/api/haruki-bot-neo")

	botAPI.Get("/status", handleGetStatus(apiHelper))

	verifySession := apiHelper.SessionHandler.VerifySessionToken
	checkNotBanned := usercore.CheckUserNotBanned(apiHelper)
	botAPI.Post("/send-mail", verifySession, checkNotBanned, handleSendMail(apiHelper))
	botAPI.Post("/register", verifySession, checkNotBanned, handleRegister(apiHelper))
}
