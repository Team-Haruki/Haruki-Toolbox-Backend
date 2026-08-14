package handler

import (
	"context"
	"time"

	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekaiapi"
)

// OAuth2WebhookAuthorizer is the narrow OAuth2 capability needed by upload
// webhook fanout. Keeping the port here lets the handler package remain
// independent from the HTTP-facing OAuth2 module that provides the adapter.
type OAuth2WebhookAuthorizer interface {
	Enabled() bool
	AuthorizedClientIDs(ctx context.Context, userID string, kratosIdentityID *string) ([]string, error)
}

const defaultBirthdaySubscriptionRequestTimeout = 5 * time.Second

// BirthdaySubscriptionConfig contains the immutable outbound-notification
// settings used while processing uploaded birthday data. Keeping this value on
// each DataHandler prevents one server instance from observing another
// instance's process-global configuration.
type BirthdaySubscriptionConfig struct {
	hmesInternalBaseURL string
	hmesInternalToken   string
	userAgent           string
	requestTimeout      time.Duration
}

type BirthdaySubscriptionConfigOptions struct {
	HMESInternalBaseURL string
	HMESInternalToken   string
	UserAgent           string
	RequestTimeout      time.Duration
}

func NewBirthdaySubscriptionConfig(options BirthdaySubscriptionConfigOptions) BirthdaySubscriptionConfig {
	return BirthdaySubscriptionConfig{
		hmesInternalBaseURL: options.HMESInternalBaseURL,
		hmesInternalToken:   options.HMESInternalToken,
		userAgent:           options.UserAgent,
		requestTimeout:      options.RequestTimeout,
	}
}

func (c BirthdaySubscriptionConfig) timeout() time.Duration {
	if c.requestTimeout <= 0 {
		return defaultBirthdaySubscriptionRequestTimeout
	}
	return c.requestTimeout
}

type DataHandler struct {
	BackgroundTasks         harukiBackground.Runner
	DBManager               *database.HarukiToolboxDBManager
	SekaiAPIClient          *sekaiapi.HarukiSekaiAPIClient
	HttpClient              *harukiHttp.Client
	Logger                  *harukiLogger.Logger
	OAuth2WebhookAuthorizer OAuth2WebhookAuthorizer
	BirthdaySubscription    BirthdaySubscriptionConfig
	SuiteRestoreService     *SuiteRestoreService
	ServerCryptor           harukiSekai.ServerCryptor
	WebhookEnabled          bool
}
