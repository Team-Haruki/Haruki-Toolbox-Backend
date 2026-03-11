package adminoauth

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

func handleGetOAuthClientStatistics(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		filters, err := parseAdminOAuthClientStatisticsFilters(c, adminNow())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidQueryFilters, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid query filters")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		authorizationBase := apiHelper.DBManager.DB.OAuthAuthorization.Query().
			Where(oauthauthorization.HasClientWith(oauthclient.IDEQ(dbClient.ID)))
		authorizationTotal, err := authorizationBase.Clone().Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountAuthorizationsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count oauth authorizations")
		}
		authorizationActive, err := authorizationBase.Clone().Where(oauthauthorization.RevokedEQ(false)).Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountActiveAuthorizationsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count active oauth authorizations")
		}
		if authorizationActive > authorizationTotal {
			authorizationActive = authorizationTotal
		}
		authorizationRevoked := authorizationTotal - authorizationActive

		tokenBase := apiHelper.DBManager.DB.OAuthToken.Query().
			Where(oauthtoken.HasClientWith(oauthclient.IDEQ(dbClient.ID)))
		tokenTotal, err := tokenBase.Clone().Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountTokensFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count oauth tokens")
		}
		tokenActive, err := tokenBase.Clone().Where(oauthtoken.RevokedEQ(false)).Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountActiveTokensFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count active oauth tokens")
		}
		if tokenActive > tokenTotal {
			tokenActive = tokenTotal
		}
		tokenRevoked := tokenTotal - tokenActive

		authorizationCounts, authorizationCreatedInRange, err := queryAdminOAuthAuthorizationTrendCounts(
			c.Context(),
			apiHelper.DBManager.DB,
			dbClient.ID,
			filters.From,
			filters.To,
			filters.Bucket,
		)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryAuthorizationTrendsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth authorization trends")
		}

		tokenCounts, tokenIssuedInRange, err := queryAdminOAuthTokenTrendCounts(
			c.Context(),
			apiHelper.DBManager.DB,
			dbClient.ID,
			filters.From,
			filters.To,
			filters.Bucket,
		)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTokenTrendsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth token trends")
		}

		resp := adminOAuthClientStatisticsResponse{
			GeneratedAt: adminNowUTC(),
			ClientID:    dbClient.ClientID,
			ClientName:  dbClient.Name,
			ClientType:  dbClient.ClientType,
			Active:      dbClient.Active,
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Bucket:      filters.Bucket,
			Summary: adminOAuthClientStatisticsSummary{
				AuthorizationTotal:          authorizationTotal,
				AuthorizationActive:         authorizationActive,
				AuthorizationRevoked:        authorizationRevoked,
				AuthorizationCreatedInRange: authorizationCreatedInRange,
				TokenTotal:                  tokenTotal,
				TokenActive:                 tokenActive,
				TokenRevoked:                tokenRevoked,
				TokenIssuedInRange:          tokenIssuedInRange,
			},
			Trend: buildAdminOAuthClientTrendPointsFromCounts(filters.From, filters.To, filters.Bucket, authorizationCounts, tokenCounts),
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"from":        resp.From.Format(time.RFC3339),
			"to":          resp.To.Format(time.RFC3339),
			"bucket":      resp.Bucket,
			"trendPoints": len(resp.Trend),
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}
