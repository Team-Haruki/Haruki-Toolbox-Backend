package admin

import (
	"context"
	"fmt"
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformAuthHeader "haruki-suite/internal/platform/authheader"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	maxAdminSessionListCount = 500
	adminSessionScanCount    = 200
)

type adminSessionItem struct {
	SessionTokenID string     `json:"sessionTokenId"`
	TTLSeconds     int64      `json:"ttlSeconds"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	Current        bool       `json:"current"`
}

type adminSessionListResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	UserID      string             `json:"userId"`
	Total       int                `json:"total"`
	Items       []adminSessionItem `json:"items"`
}

type adminReauthPayload struct {
	Password string `json:"password"`
}

type adminReauthResponse struct {
	ReauthenticatedAt time.Time `json:"reauthenticatedAt"`
}

func parseSessionTokenIDFromAuthorization(authHeader string, sessionSignKey string) string {
	if strings.TrimSpace(sessionSignKey) == "" {
		return ""
	}
	tokenStr, ok := platformAuthHeader.ExtractBearerToken(authHeader)
	if !ok {
		return ""
	}

	claims := &harukiAPIHelper.SessionClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(sessionSignKey), nil
	})
	if err != nil || !token.Valid {
		return ""
	}
	return strings.TrimSpace(claims.SessionToken)
}

func listUserSessionItems(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string, currentSessionTokenID string) ([]adminSessionItem, error) {
	prefix := strings.TrimSpace(userID) + ":"
	var cursor uint64
	items := make([]adminSessionItem, 0, 16)
	seen := make(map[string]struct{})

	for {
		keys, nextCursor, err := apiHelper.DBManager.Redis.Redis.Scan(ctx, cursor, prefix+"*", adminSessionScanCount).Result()
		if err != nil {
			return nil, err
		}

		for _, key := range keys {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			sessionTokenID := strings.TrimSpace(strings.TrimPrefix(key, prefix))
			if sessionTokenID == "" {
				continue
			}
			if _, ok := seen[sessionTokenID]; ok {
				continue
			}
			seen[sessionTokenID] = struct{}{}

			ttl, err := apiHelper.DBManager.Redis.Redis.TTL(ctx, key).Result()
			if err != nil {
				return nil, err
			}

			item := adminSessionItem{
				SessionTokenID: sessionTokenID,
				TTLSeconds:     int64(ttl / time.Second),
				Current:        sessionTokenID == currentSessionTokenID,
			}

			if ttl > 0 {
				expiresAt := adminNowUTC().Add(ttl)
				item.ExpiresAt = &expiresAt
			}
			items = append(items, item)

			if len(items) >= maxAdminSessionListCount {
				break
			}
		}

		cursor = nextCursor
		if cursor == 0 || len(items) >= maxAdminSessionListCount {
			break
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Current != items[j].Current {
			return items[i].Current
		}
		if items[i].TTLSeconds == items[j].TTLSeconds {
			return items[i].SessionTokenID < items[j].SessionTokenID
		}
		return items[i].TTLSeconds > items[j].TTLSeconds
	})

	return items, nil
}

func listUserKratosSessionItems(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, identityID string, currentSessionID string) ([]adminSessionItem, error) {
	sessions, err := apiHelper.SessionHandler.ListKratosSessionsByIdentityID(ctx, identityID)
	if err != nil {
		return nil, err
	}

	now := adminNowUTC()
	items := make([]adminSessionItem, 0, len(sessions))
	for _, session := range sessions {
		sessionID := strings.TrimSpace(session.ID)
		if sessionID == "" {
			continue
		}

		item := adminSessionItem{
			SessionTokenID: sessionID,
			TTLSeconds:     -1,
			Current:        sessionID == currentSessionID,
		}
		if session.ExpiresAt != nil {
			expiresAt := session.ExpiresAt.UTC()
			item.ExpiresAt = &expiresAt
			ttl := int64(expiresAt.Sub(now) / time.Second)
			if ttl < 0 {
				ttl = 0
			}
			item.TTLSeconds = ttl
		}
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Current != items[j].Current {
			return items[i].Current
		}
		if items[i].TTLSeconds == items[j].TTLSeconds {
			return items[i].SessionTokenID < items[j].SessionTokenID
		}
		return items[i].TTLSeconds > items[j].TTLSeconds
	})

	if len(items) > maxAdminSessionListCount {
		items = items[:maxAdminSessionListCount]
	}
	return items, nil
}

func resolveAdminKratosIdentityID(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string) (string, error) {
	dbUser, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(userID)).
		Select(userSchema.FieldKratosIdentityID).
		Only(ctx)
	if err != nil {
		return "", err
	}
	if dbUser.KratosIdentityID == nil {
		return "", nil
	}
	return strings.TrimSpace(*dbUser.KratosIdentityID), nil
}

func shouldUseKratosSessionInventory(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, currentLocalSessionTokenID, kratosHeaderToken, cookieHeader, bearerToken string) bool {
	if apiHelper == nil || apiHelper.SessionHandler == nil || !apiHelper.SessionHandler.UsesKratosProvider() {
		return false
	}
	if strings.TrimSpace(currentLocalSessionTokenID) != "" {
		return false
	}
	if strings.TrimSpace(kratosHeaderToken) != "" || strings.TrimSpace(cookieHeader) != "" {
		return true
	}
	return strings.TrimSpace(bearerToken) != ""
}

func resolveCurrentKratosSessionID(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, bearerToken, kratosHeaderToken, cookieHeader string) (string, error) {
	sessionToken := strings.TrimSpace(kratosHeaderToken)
	if sessionToken == "" {
		sessionToken = strings.TrimSpace(bearerToken)
	}
	return apiHelper.SessionHandler.ResolveKratosSessionID(ctx, sessionToken, cookieHeader)
}

func mapKratosSessionDeleteError(err error) (statusCode int, message string, known bool) {
	switch {
	case err == nil:
		return fiber.StatusOK, "", false
	case harukiAPIHelper.IsKratosSessionNotFoundError(err):
		return fiber.StatusNotFound, "session not found", true
	case harukiAPIHelper.IsKratosInvalidInputError(err):
		return fiber.StatusBadRequest, "invalid session_token_id", true
	default:
		return fiber.StatusInternalServerError, "failed to delete session", false
	}
}

func handleListAdminSessions(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return respondFiberOrUnauthorized(c, err, "missing user session")
		}

		authHeader := c.Get("Authorization")
		bearerToken, _ := platformAuthHeader.ExtractBearerToken(authHeader)
		sessionSignKey := ""
		if apiHelper != nil && apiHelper.SessionHandler != nil {
			sessionSignKey = apiHelper.SessionHandler.SessionSignKey
		}
		currentSessionTokenID := parseSessionTokenIDFromAuthorization(authHeader, sessionSignKey)

		kratosHeaderToken := ""
		if apiHelper != nil && apiHelper.SessionHandler != nil {
			kratosHeaderToken = strings.TrimSpace(c.Get(apiHelper.SessionHandler.KratosSessionHeader))
		}
		cookieHeader := strings.TrimSpace(c.Get("Cookie"))

		useKratosSessions := shouldUseKratosSessionInventory(apiHelper, currentSessionTokenID, kratosHeaderToken, cookieHeader, bearerToken)
		items := make([]adminSessionItem, 0, 8)
		listedWithKratos := false
		if useKratosSessions {
			identityID, identityErr := resolveAdminKratosIdentityID(c.Context(), apiHelper, userID)
			if identityErr != nil {
				reason := adminFailureReasonQueryUserFailed
				if postgresql.IsNotFound(identityErr) {
					reason = adminFailureReasonTargetUserNotFound
				}
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsList, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(reason, map[string]any{
					"provider": "kratos",
				}))
				return harukiAPIHelper.ErrorInternal(c, "failed to list sessions")
			}
			if identityID != "" {
				currentKratosSessionID := ""
				if sessionID, sessionErr := resolveCurrentKratosSessionID(c.Context(), apiHelper, bearerToken, kratosHeaderToken, cookieHeader); sessionErr == nil {
					currentKratosSessionID = sessionID
				}
				items, err = listUserKratosSessionItems(c.Context(), apiHelper, identityID, currentKratosSessionID)
				if err != nil {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsList, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonListSessionsFailed, map[string]any{
						"provider": "kratos",
					}))
					return harukiAPIHelper.ErrorInternal(c, "failed to list sessions")
				}
				listedWithKratos = true
			}
		}
		if !listedWithKratos {
			items, err = listUserSessionItems(c.Context(), apiHelper, userID, currentSessionTokenID)
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsList, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonListSessionsFailed, map[string]any{
					"provider": "local",
				}))
				return harukiAPIHelper.ErrorInternal(c, "failed to list sessions")
			}
		}

		resp := adminSessionListResponse{
			GeneratedAt: adminNowUTC(),
			UserID:      userID,
			Total:       len(items),
			Items:       items,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsList, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total":    len(items),
			"provider": map[bool]string{true: "kratos", false: "local"}[listedWithKratos],
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleDeleteAdminSession(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return respondFiberOrUnauthorized(c, err, "missing user session")
		}

		sessionTokenID := strings.TrimSpace(c.Params("session_token_id"))
		if sessionTokenID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingSessionTokenId, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "session_token_id is required")
		}

		authHeader := c.Get("Authorization")
		bearerToken, _ := platformAuthHeader.ExtractBearerToken(authHeader)
		sessionSignKey := ""
		if apiHelper != nil && apiHelper.SessionHandler != nil {
			sessionSignKey = apiHelper.SessionHandler.SessionSignKey
		}
		currentSessionTokenID := parseSessionTokenIDFromAuthorization(authHeader, sessionSignKey)
		kratosHeaderToken := ""
		if apiHelper != nil && apiHelper.SessionHandler != nil {
			kratosHeaderToken = strings.TrimSpace(c.Get(apiHelper.SessionHandler.KratosSessionHeader))
		}
		cookieHeader := strings.TrimSpace(c.Get("Cookie"))
		useKratosSessions := shouldUseKratosSessionInventory(apiHelper, currentSessionTokenID, kratosHeaderToken, cookieHeader, bearerToken)

		affected := int64(0)
		if useKratosSessions {
			if err := apiHelper.SessionHandler.RevokeKratosSessionByID(c.Context(), sessionTokenID); err != nil {
				if statusCode, message, known := mapKratosSessionDeleteError(err); known {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteSessionFailed, map[string]any{
						"provider":   "kratos",
						"statusCode": statusCode,
						"message":    message,
					}))
					switch statusCode {
					case fiber.StatusBadRequest:
						return harukiAPIHelper.ErrorBadRequest(c, message)
					case fiber.StatusNotFound:
						return harukiAPIHelper.ErrorNotFound(c, message)
					default:
						return harukiAPIHelper.ErrorInternal(c, message)
					}
				}
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteSessionFailed, map[string]any{
					"provider": "kratos",
				}))
				return harukiAPIHelper.ErrorInternal(c, "failed to delete session")
			}
			affected = 1
		} else {
			key := userID + ":" + sessionTokenID
			affected, err = apiHelper.DBManager.Redis.Redis.Del(c.Context(), key).Result()
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteSessionFailed, map[string]any{
					"provider": "local",
				}))
				return harukiAPIHelper.ErrorInternal(c, "failed to delete session")
			}
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"sessionTokenID": sessionTokenID,
			"affected":       affected,
			"provider":       map[bool]string{true: "kratos", false: "local"}[useKratosSessions],
		})
		return harukiAPIHelper.SuccessResponse[string](c, "session deleted", nil)
	}
}

func handleAdminReauth(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return respondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload adminReauthPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		password := strings.TrimSpace(payload.Password)
		if password == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingPassword, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "password is required")
		}

		dbUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(userID)).
			Only(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to verify account")
		}

		if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() {
			err := apiHelper.SessionHandler.VerifyKratosPassword(c.Context(), dbUser.Email, payload.Password)
			if err != nil {
				if harukiAPIHelper.IsKratosInvalidCredentialsError(err) || harukiAPIHelper.IsKratosInvalidInputError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPasswordMismatch, nil))
					return harukiAPIHelper.ErrorForbidden(c, "password mismatch")
				}
				if harukiAPIHelper.IsIdentityProviderUnavailableError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata("identity_provider_unavailable", nil))
					return harukiAPIHelper.ErrorInternal(c, "failed to verify account")
				}
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPasswordMismatch, map[string]any{
					"provider": "kratos",
				}))
				return harukiAPIHelper.ErrorInternal(c, "failed to verify account")
			}
		} else if err := bcrypt.CompareHashAndPassword([]byte(dbUser.PasswordHash), []byte(payload.Password)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPasswordMismatch, nil))
			return harukiAPIHelper.ErrorForbidden(c, "password mismatch")
		}

		resp := adminReauthResponse{ReauthenticatedAt: adminNowUTC()}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "reauthenticated", &resp)
	}
}
