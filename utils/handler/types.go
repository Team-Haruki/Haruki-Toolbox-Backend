package handler

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekaiapi"
)

type DataHandler struct {
	DBManager      *database.HarukiToolboxDBManager
	SekaiAPIClient *sekaiapi.HarukiSekaiAPIClient
	HttpClient     *harukiHttp.Client
	Logger         *harukiLogger.Logger
	WebhookEnabled bool
}
