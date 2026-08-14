package bootstrap

import (
	"context"
	"fmt"
	"strings"
	"time"

	harukiAPI "github.com/Team-Haruki/Haruki-Toolbox-Backend/api"
	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	iosModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/ios"
	miscModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/misc"
	sponsorModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	ticketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	userProfileModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userprofile"
	userSocialModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usersocial"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	harukiCloudflare "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/cloudflare"
	harukiHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/handler"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"

	"github.com/gofiber/fiber/v3"
)

// Build validates cfg and assembles the server, workers, and external resources.
// If assembly fails, every resource acquired so far is released before Build
// returns. The returned Application owns all successfully assembled resources.
func Build(cfg harukiConfig.Config) (*Application, error) {
	if err := validateOAuth2ProviderConfig(cfg); err != nil {
		return nil, err
	}
	if err := validateUserSystemConfig(cfg); err != nil {
		return nil, err
	}
	if err := validateBackendConfig(cfg); err != nil {
		return nil, err
	}
	if err := validateBotRegistrationConfig(cfg); err != nil {
		return nil, err
	}

	application := &Application{
		shutdownTimeout: time.Duration(cfg.Backend.ShutdownTimeout) * time.Second,
	}
	buildComplete := false
	defer func() {
		if !buildComplete {
			application.stopAndWaitWorkers()
			if application.drainBackgroundTasks() == nil {
				_ = application.closeResources()
			}
		}
	}()

	resources, err := acquireApplicationResources(cfg, application)
	if err != nil {
		return nil, err
	}

	// Ory session wiring remains in the composition root so every HTTP module
	// shares the same trusted-header and Kratos integration.
	sessionHandler := harukiAPIHelper.NewSessionHandler(resources.redisClient.Redis, cfg.UserSystem.SessionSignToken)
	sessionHandler.ConfigureIdentityProvider(
		cfg.UserSystem.AuthProvider,
		cfg.UserSystem.KratosPublicURL,
		cfg.UserSystem.KratosAdminURL,
		cfg.UserSystem.KratosSessionHeader,
		cfg.UserSystem.KratosSessionCookie,
		cfg.UserSystem.KratosAutoLinkByEmail,
		cfg.UserSystem.KratosAutoProvisionUser,
		time.Duration(cfg.UserSystem.KratosRequestTimeout)*time.Second,
		resources.toolboxClient,
	)
	sessionHandler.ConfigureAuthProxy(
		cfg.UserSystem.AuthProxyEnabled,
		cfg.UserSystem.AuthProxyTrustedHeader,
		cfg.UserSystem.AuthProxyTrustedValue,
		cfg.UserSystem.AuthProxySubjectHeader,
		cfg.UserSystem.AuthProxyNameHeader,
		cfg.UserSystem.AuthProxyEmailHeader,
		cfg.UserSystem.AuthProxyEmailVerifiedHeader,
		cfg.UserSystem.AuthProxyUserIDHeader,
	)
	sessionHandler.ConfigureAuthProxySessionHeader(cfg.UserSystem.AuthProxySessionHeader)

	if err := resources.acquireHTTPResources(cfg, application); err != nil {
		return nil, err
	}
	if err := resources.acquireBotDatabase(cfg, application); err != nil {
		return nil, err
	}

	// Runtime-mutable settings are seeded from immutable startup configuration
	// and distributed through the existing Redis record.
	runtimeConfig := newRuntimeConfigService(cfg, resources.redisClient)
	apiHelper := harukiAPIHelper.NewHarukiToolboxRouterHelpers(
		resources.fiberApp,
		resources.databaseManager,
		resources.smtpClient,
		sessionHandler,
		resources.sekaiAPIClient,
		runtimeConfig,
	)
	apiHelper.BotRegistrationEnabled = cfg.HarukiBot.EnableRegistration
	apiHelper.BotCredentialSignToken = cfg.HarukiBot.CredentialSignToken
	turnstileVerifier := harukiCloudflare.NewClient(harukiCloudflare.Config{
		Secret:  cfg.UserSystem.CloudflareSecret,
		Bypass:  cfg.UserSystem.TurnstileBypass,
		Proxy:   cfg.Proxy,
		Timeout: 5 * time.Second,
	})
	suiteRestoreService := harukiHandler.NewSuiteRestoreService(harukiHandler.SuiteRestoreServiceOptions{
		StructuresFile:  cfg.RestoreSuite.StructuresFile,
		EnableRegions:   cfg.RestoreSuite.EnableRegions,
		SuiteRemoveKeys: cfg.SekaiClient.SuiteRemoveKeys,
	})
	application.backgroundTasks = harukiBackground.NewTaskGroup(func(name string, recovered any) {
		resources.logger.Errorf("Background task %q panicked: %v", name, recovered)
	})
	afdianConfig := sponsorModule.NewAfdianConfig(sponsorModule.AfdianConfigOptions{
		UserID:         cfg.Afdian.UserID,
		APIToken:       cfg.Afdian.APIToken,
		APIBaseURL:     cfg.Afdian.APIBaseURL,
		RequestTimeout: time.Duration(cfg.Afdian.RequestTimeoutSecond) * time.Second,
		WebhookSecret:  cfg.Afdian.WebhookSecret,
		SyncEnabled:    cfg.Afdian.SyncEnabled,
		SyncInterval:   time.Duration(cfg.Afdian.SyncIntervalSeconds) * time.Second,
	})
	harukiAPI.RegisterRoutes(apiHelper, harukiAPI.Dependencies{
		BackgroundTasks:   application.backgroundTasks,
		TurnstileVerifier: turnstileVerifier,
		UserDataBuilder:   harukiAPIHelper.NewUserDataBuilder(cfg.UserSystem.AvatarURL),
		AfdianConfig:      afdianConfig,
		MiscAssets: miscModule.NewAssetsConfig(miscModule.AssetsConfigOptions{
			AvatarBaseURL: cfg.UserSystem.AvatarURL,
		}),
		IOSEndpoints: iosModule.NewEndpointConfig(iosModule.EndpointConfigOptions{
			BackendURL:    cfg.Backend.BackendURL,
			BackendCDNURL: cfg.Backend.BackendCDNURL,
		}),
		HydraConfig: harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
			Provider:       cfg.OAuth2.Provider,
			PublicURL:      cfg.OAuth2.HydraPublicURL,
			BrowserURL:     cfg.OAuth2.HydraBrowserURL,
			AdminURL:       cfg.OAuth2.HydraAdminURL,
			ClientID:       cfg.OAuth2.HydraClientID,
			ClientSecret:   cfg.OAuth2.HydraClientSecret,
			RequestTimeout: time.Duration(cfg.OAuth2.HydraRequestTimeoutSecond) * time.Second,
		}),
		OAuth2AvatarBaseURL: cfg.UserSystem.AvatarURL,
		UploadHTTPClient:    harukiHttp.NewClient(strings.TrimSpace(cfg.Proxy), 15*time.Second),
		UploadLogger:        harukiLogger.NewLoggerFromGlobal("SekaiDataHandler"),
		BirthdaySubscription: harukiHandler.NewBirthdaySubscriptionConfig(harukiHandler.BirthdaySubscriptionConfigOptions{
			HMESInternalBaseURL: cfg.Subscription.HMESInternalBaseURL,
			HMESInternalToken:   cfg.Subscription.HMESInternalToken,
			UserAgent:           cfg.Subscription.UserAgent,
			RequestTimeout:      time.Duration(cfg.Subscription.RequestTimeoutSecond) * time.Second,
		}),
		SuiteRestoreService: suiteRestoreService,
		ServerCryptor: harukiSekai.NewServerCryptor(harukiSekai.ServerCryptorConfig{
			ENServerAESKey:    cfg.SekaiClient.ENServerAESKey,
			ENServerAESIV:     cfg.SekaiClient.ENServerAESIV,
			OtherServerAESKey: cfg.SekaiClient.OtherServerAESKey,
			OtherServerAESIV:  cfg.SekaiClient.OtherServerAESIV,
		}),
		UploadProxy: cfg.Proxy,
		UserProfileConfig: userProfileModule.NewConfig(userProfileModule.ConfigOptions{
			AvatarSaveDir: cfg.UserSystem.AvatarSaveDir,
			AvatarBaseURL: cfg.UserSystem.AvatarURL,
		}),
		SocialBotVerify: userSocialModule.NewBotVerifyConfig(userSocialModule.BotVerifyConfigOptions{
			Token: cfg.UserSystem.SocialPlatformVerifyToken,
		}),
		TicketNotifications: ticketsModule.NotificationConfig{
			FrontendURL: cfg.UserSystem.FrontendURL,
			DetailPath:  "/tickets",
			DisplayName: cfg.UserSystem.SMTP.MailName,
		},
	})

	schedulerCtx, stopSchedulers := context.WithCancel(context.Background())
	waitAfdianScheduler := startAfdianSponsorSyncScheduler(schedulerCtx, resources.toolboxClient, afdianConfig, resources.logger)
	waitStatsSampler := func() {}
	if cfg.Backend.ProfilingEnabled {
		sqlPools := []sqlPoolSource{{name: "toolbox", db: resources.toolboxSQLDB}}
		if resources.botSQLDB != nil {
			sqlPools = append(sqlPools, sqlPoolSource{name: "bot", db: resources.botSQLDB})
		}
		samplerInterval := time.Duration(cfg.Backend.ProfilingIntervalSeconds) * time.Second
		waitStatsSampler = startStatsSampler(schedulerCtx, samplerInterval, resources.mongoPoolStats, sqlPools, resources.logger)
	}
	// Workers are owned by Application so Serve/Close always cancel and drain them
	// before any database resource is released.
	application.stopWorkers = func() {
		stopSchedulers()
		waitAfdianScheduler()
		waitStatsSampler()
	}

	loadedRegions, failedRegions := suiteRestoreService.LoadStatus()
	if len(failedRegions) > 0 {
		resources.logger.Warnf("Suite restorer initialized with %d loaded region(s), %d failed region(s): %v", loadedRegions, len(failedRegions), failedRegions)
	} else {
		resources.logger.Infof("Suite restorer initialized with %d loaded region(s)", loadedRegions)
	}

	application.fiberApp = resources.fiberApp
	application.logger = resources.logger
	application.address = fmt.Sprintf("%s:%d", cfg.Backend.Host, cfg.Backend.Port)
	application.serverType = "HTTP"
	application.listenConfig = fiber.ListenConfig{DisableStartupMessage: true}
	if cfg.Backend.SSL {
		application.serverType = "HTTPS"
		application.listenConfig.CertFile = cfg.Backend.SSLCert
		application.listenConfig.CertKeyFile = cfg.Backend.SSLKey
	}

	buildComplete = true
	return application, nil
}
