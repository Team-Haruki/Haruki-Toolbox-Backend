package upload

import (
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiDataHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/handler"
)

func newUploadDataHandler(helper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) *harukiDataHandler.DataHandler {
	return &harukiDataHandler.DataHandler{
		BackgroundTasks:         dependencies.BackgroundTasks,
		DBManager:               helper.DBManager,
		SekaiAPIClient:          helper.SekaiAPIClient,
		HttpClient:              dependencies.HTTPClient,
		Logger:                  dependencies.DataHandlerLogger,
		OAuth2WebhookAuthorizer: dependencies.OAuth2WebhookAuthorizer,
		BirthdaySubscription:    dependencies.BirthdaySubscription,
		SuiteRestoreService:     dependencies.SuiteRestoreService,
		ServerCryptor:           dependencies.ServerCryptor,
		WebhookEnabled:          helper.GetWebhookEnabled(),
	}
}
