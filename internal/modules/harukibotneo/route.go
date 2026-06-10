package harukibotneo

import harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

func RegisterHarukiBotNeoRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	botAPI := apiHelper.Router.Group("/api/haruki-bot-neo")

	botAPI.Get("/status", handleGetStatus(apiHelper))

	botAPI.Post("/send-mail",
		apiHelper.SessionHandler.VerifySessionToken,
		handleSendMail(apiHelper),
	)
	botAPI.Post("/register",
		apiHelper.SessionHandler.VerifySessionToken,
		handleRegister(apiHelper),
	)
}
