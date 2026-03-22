package handler

import (
	"haruki-suite/utils/database"
	harukiHttp "haruki-suite/utils/http"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/sekaiapi"
)

type DataHandler struct {
	DBManager      *database.HarukiToolboxDBManager
	SekaiAPIClient *sekaiapi.HarukiSekaiAPIClient
	HttpClient     *harukiHttp.Client
	Logger         *harukiLogger.Logger
	WebhookEnabled bool
}
