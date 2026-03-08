package admin

import (
	"crypto/rand"
	"encoding/hex"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"haruki-suite/utils/database/postgresql/iosscriptcode"
	"haruki-suite/utils/database/postgresql/socialplatforminfo"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

type adminUserGameBindingsResponse struct {
	GeneratedAt time.Time                            `json:"generatedAt"`
	UserID      string                               `json:"userId"`
	Total       int                                  `json:"total"`
	Items       []harukiAPIHelper.GameAccountBinding `json:"items"`
}

type adminUserGameBindingUpsertResponse struct {
	UserID     string                             `json:"userId"`
	Server     string                             `json:"server"`
	GameUserID string                             `json:"gameUserId"`
	Created    bool                               `json:"created"`
	Binding    harukiAPIHelper.GameAccountBinding `json:"binding"`
}

type adminManagedSocialPlatformPayload struct {
	Platform      string `json:"platform"`
	PlatformSnake string `json:"platform_name"`
	UserID        string `json:"userId"`
	UserIDSnake   string `json:"user_id"`
	Verified      *bool  `json:"verified"`
}

type adminManagedAuthorizedSocialPayload struct {
	Platform      string `json:"platform"`
	PlatformSnake string `json:"platform_name"`
	UserID        string `json:"userId"`
	UserIDSnake   string `json:"user_id"`
	Comment       string `json:"comment"`
}

type adminUserSocialPlatformResponse struct {
	GeneratedAt    time.Time                           `json:"generatedAt"`
	UserID         string                              `json:"userId"`
	Exists         bool                                `json:"exists"`
	SocialPlatform *harukiAPIHelper.SocialPlatformInfo `json:"socialPlatform,omitempty"`
}

type adminUserAuthorizedSocialListResponse struct {
	GeneratedAt time.Time                                     `json:"generatedAt"`
	UserID      string                                        `json:"userId"`
	Total       int                                           `json:"total"`
	Items       []harukiAPIHelper.AuthorizeSocialPlatformInfo `json:"items"`
}

type adminUserAuthorizedSocialUpsertResponse struct {
	UserID     string                                      `json:"userId"`
	PlatformID int                                         `json:"platformId"`
	Created    bool                                        `json:"created"`
	Record     harukiAPIHelper.AuthorizeSocialPlatformInfo `json:"record"`
}

type adminUserIOSUploadCodeResponse struct {
	UserID     string `json:"userId"`
	UploadCode string `json:"uploadCode"`
}

type adminUserClearIOSUploadCodeResponse struct {
	UserID  string `json:"userId"`
	Cleared bool   `json:"cleared"`
}

type adminManagedEmailPayload struct {
	Email string `json:"email"`
}

type adminUserEmailResponse struct {
	UserID   string `json:"userId"`
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
}

type adminUpdateAllowCNMysekaiPayload struct {
	AllowCNMysekai      *bool `json:"allowCNMysekai"`
	AllowCNMysekaiSnake *bool `json:"allow_cn_mysekai"`
}

type adminUserAllowCNMysekaiResponse struct {
	UserID         string `json:"userId"`
	AllowCNMysekai bool   `json:"allowCNMysekai"`
}

func normalizeAdminManageSocialPlatform(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(harukiAPIHelper.SocialPlatformQQ):
		return string(harukiAPIHelper.SocialPlatformQQ), nil
	case string(harukiAPIHelper.SocialPlatformQQBot):
		return string(harukiAPIHelper.SocialPlatformQQBot), nil
	case string(harukiAPIHelper.SocialPlatformDiscord):
		return string(harukiAPIHelper.SocialPlatformDiscord), nil
	case string(harukiAPIHelper.SocialPlatformTelegram):
		return string(harukiAPIHelper.SocialPlatformTelegram), nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "unsupported social platform")
	}
}

func parseAdminManagedSocialPlatformPayload(c fiber.Ctx) (*adminManagedSocialPlatformPayload, error) {
	var payload adminManagedSocialPlatformPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	platform := strings.TrimSpace(payload.Platform)
	if platform == "" {
		platform = strings.TrimSpace(payload.PlatformSnake)
	}
	normalizedPlatform, err := normalizeAdminManageSocialPlatform(platform)
	if err != nil {
		return nil, err
	}

	userID := strings.TrimSpace(payload.UserID)
	if userID == "" {
		userID = strings.TrimSpace(payload.UserIDSnake)
	}
	if userID == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "userId is required")
	}

	verified := true
	if payload.Verified != nil {
		verified = *payload.Verified
	}

	return &adminManagedSocialPlatformPayload{
		Platform: normalizedPlatform,
		UserID:   userID,
		Verified: &verified,
	}, nil
}

func parseAdminManagedAuthorizedSocialPayload(c fiber.Ctx) (*adminManagedAuthorizedSocialPayload, error) {
	var payload adminManagedAuthorizedSocialPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	platform := strings.TrimSpace(payload.Platform)
	if platform == "" {
		platform = strings.TrimSpace(payload.PlatformSnake)
	}
	normalizedPlatform, err := normalizeAdminManageSocialPlatform(platform)
	if err != nil {
		return nil, err
	}

	userID := strings.TrimSpace(payload.UserID)
	if userID == "" {
		userID = strings.TrimSpace(payload.UserIDSnake)
	}
	if userID == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "userId is required")
	}

	return &adminManagedAuthorizedSocialPayload{
		Platform: normalizedPlatform,
		UserID:   userID,
		Comment:  strings.TrimSpace(payload.Comment),
	}, nil
}

func parseAdminGameBindingPayload(c fiber.Ctx) (*harukiAPIHelper.CreateGameAccountBindingPayload, error) {
	var payload harukiAPIHelper.CreateGameAccountBindingPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}
	return &payload, nil
}

func parseAdminManagedEmailPayload(c fiber.Ctx) (*adminManagedEmailPayload, error) {
	var payload adminManagedEmailPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	payload.Email = strings.TrimSpace(payload.Email)
	if payload.Email == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "email is required")
	}
	if len(payload.Email) > 320 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "email exceeds max length")
	}

	parsed, err := mail.ParseAddress(payload.Email)
	if err != nil || parsed == nil || strings.TrimSpace(parsed.Address) != payload.Email {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid email format")
	}

	return &payload, nil
}

func parseAdminUpdateAllowCNMysekaiPayload(c fiber.Ctx) (*adminUpdateAllowCNMysekaiPayload, error) {
	var payload adminUpdateAllowCNMysekaiPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	allow := payload.AllowCNMysekai
	if allow == nil {
		allow = payload.AllowCNMysekaiSnake
	}
	if allow == nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "allowCNMysekai is required")
	}

	return &adminUpdateAllowCNMysekaiPayload{
		AllowCNMysekai: allow,
	}, nil
}

func resolveManageableTargetUser(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string) (*postgresql.User, error) {
	targetUserID := strings.TrimSpace(c.Params("target_user_id"))
	if targetUserID == "" {
		writeAdminAuditLog(c, apiHelper, action, "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_target_user_id", nil))
		return nil, fiber.NewError(fiber.StatusBadRequest, "target_user_id is required")
	}

	actorUserID, actorRole, err := currentAdminActor(c)
	if err != nil {
		writeAdminAuditLog(c, apiHelper, action, "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
		return nil, err
	}

	targetUser, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(targetUserID)).
		Select(userSchema.FieldID, userSchema.FieldRole).
		Only(c.Context())
	if err != nil {
		if postgresql.IsNotFound(err) {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
			return nil, fiber.NewError(fiber.StatusNotFound, "user not found")
		}
		writeAdminAuditLog(c, apiHelper, action, "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_target_user_failed", nil))
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to query target user")
	}

	if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", map[string]any{
			"actorRole":  actorRole,
			"targetRole": normalizeRole(string(targetUser.Role)),
		}))
		return nil, err
	}

	return targetUser, nil
}

func buildAdminGameBindingItems(rows []*postgresql.GameAccountBinding) []harukiAPIHelper.GameAccountBinding {
	items := make([]harukiAPIHelper.GameAccountBinding, 0, len(rows))
	for _, row := range rows {
		items = append(items, harukiAPIHelper.GameAccountBinding{
			Server:   harukiUtils.SupportedDataUploadServer(row.Server),
			UserID:   row.GameUserID,
			Verified: row.Verified,
			Suite:    row.Suite,
			Mysekai:  row.Mysekai,
		})
	}
	return items
}

func buildAdminAuthorizedSocialItems(rows []*postgresql.AuthorizeSocialPlatformInfo) []harukiAPIHelper.AuthorizeSocialPlatformInfo {
	items := make([]harukiAPIHelper.AuthorizeSocialPlatformInfo, 0, len(rows))
	for _, row := range rows {
		items = append(items, harukiAPIHelper.AuthorizeSocialPlatformInfo{
			ID:       row.PlatformID,
			Platform: row.Platform,
			UserID:   row.PlatformUserID,
			Comment:  row.Comment,
		})
	}
	return items
}

func generateAdminIOSUploadCode() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func handleListUserGameAccountBindings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.game_account_bindings.list"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		rows, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(targetUser.ID))).
			All(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_bindings_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query game account bindings")
		}

		resp := adminUserGameBindingsResponse{
			GeneratedAt: time.Now().UTC(),
			UserID:      targetUser.ID,
			Total:       len(rows),
			Items:       buildAdminGameBindingItems(rows),
		}

		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpdateUserEmail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.email.update"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		payload, err := parseAdminManagedEmailPayload(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		userConflict, err := apiHelper.DBManager.DB.User.Query().
			Where(
				userSchema.EmailEQ(payload.Email),
				userSchema.IDNEQ(targetUser.ID),
			).
			Exist(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_user_conflict_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to check email conflict")
		}
		if userConflict {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("email_conflict", nil))
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "email already in use", nil)
		}

		if _, err := apiHelper.DBManager.DB.User.UpdateOneID(targetUser.ID).SetEmail(payload.Email).Save(c.Context()); err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_user_email_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update user email")
		}

		resp := adminUserEmailResponse{
			UserID:   targetUser.ID,
			Email:    payload.Email,
			Verified: true,
		}
		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"email":    payload.Email,
			"verified": true,
		})
		return harukiAPIHelper.SuccessResponse(c, "user email updated", &resp)
	}
}

func handleUpdateUserAllowCNMysekai(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.allow_cn_mysekai.update"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		payload, err := parseAdminUpdateAllowCNMysekaiPayload(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		updated, err := apiHelper.DBManager.DB.User.UpdateOneID(targetUser.ID).
			SetAllowCnMysekai(*payload.AllowCNMysekai).
			Save(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_allow_cn_mysekai_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update allow_cn_mysekai")
		}

		resp := adminUserAllowCNMysekaiResponse{
			UserID:         updated.ID,
			AllowCNMysekai: updated.AllowCnMysekai,
		}
		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"allowCNMysekai": updated.AllowCnMysekai,
		})
		return harukiAPIHelper.SuccessResponse(c, "allow_cn_mysekai updated", &resp)
	}
}

func handleUpsertUserGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.game_account_binding.upsert"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		serverRaw := strings.TrimSpace(c.Params("server"))
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverRaw)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_server", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		gameUserID := strings.TrimSpace(c.Params("game_user_id"))
		if gameUserID == "" {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_game_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id is required")
		}
		if _, err := strconv.Atoi(gameUserID); err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_game_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id must be numeric")
		}

		payload, err := parseAdminGameBindingPayload(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		existing, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(
				gameaccountbinding.ServerEQ(string(server)),
				gameaccountbinding.GameUserIDEQ(gameUserID),
			).
			WithUser().
			Only(c.Context())
		if err != nil && !postgresql.IsNotFound(err) {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_binding_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query existing binding")
		}

		created := false
		if existing != nil {
			if existing.Edges.User == nil || existing.Edges.User.ID != targetUser.ID {
				writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("binding_owned_by_other_user", map[string]any{
					"server":     string(server),
					"gameUserID": gameUserID,
				}))
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding belongs to another user", nil)
			}

			if _, err := existing.Update().
				SetVerified(true).
				SetSuite(payload.Suite).
				SetMysekai(payload.MySekai).
				Save(c.Context()); err != nil {
				writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_binding_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to update game account binding")
			}
		} else {
			created = true
			if _, err := apiHelper.DBManager.DB.GameAccountBinding.Create().
				SetServer(string(server)).
				SetGameUserID(gameUserID).
				SetVerified(true).
				SetSuite(payload.Suite).
				SetMysekai(payload.MySekai).
				SetUserID(targetUser.ID).
				Save(c.Context()); err != nil {
				if postgresql.IsConstraintError(err) {
					writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("binding_conflict", nil))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding conflict", nil)
				}
				writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("create_binding_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to create game account binding")
			}
		}

		resp := adminUserGameBindingUpsertResponse{
			UserID:     targetUser.ID,
			Server:     string(server),
			GameUserID: gameUserID,
			Created:    created,
			Binding: harukiAPIHelper.GameAccountBinding{
				Server:   server,
				UserID:   gameUserID,
				Verified: true,
				Suite:    payload.Suite,
				Mysekai:  payload.MySekai,
			},
		}
		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"server":     string(server),
			"gameUserID": gameUserID,
			"created":    created,
		})
		return harukiAPIHelper.SuccessResponse(c, "game account binding upserted", &resp)
	}
}

func handleDeleteUserGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.game_account_binding.delete"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		serverRaw := strings.TrimSpace(c.Params("server"))
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverRaw)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_server", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		gameUserID := strings.TrimSpace(c.Params("game_user_id"))
		if gameUserID == "" {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_game_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id is required")
		}

		existing, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(
				gameaccountbinding.ServerEQ(string(server)),
				gameaccountbinding.GameUserIDEQ(gameUserID),
			).
			WithUser().
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("binding_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "binding not found")
			}
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_binding_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query binding")
		}

		if existing.Edges.User == nil || existing.Edges.User.ID != targetUser.ID {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("binding_owned_by_other_user", map[string]any{
				"server":     string(server),
				"gameUserID": gameUserID,
			}))
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding belongs to another user", nil)
		}

		if err := apiHelper.DBManager.DB.GameAccountBinding.DeleteOne(existing).Exec(c.Context()); err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("delete_binding_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete binding")
		}

		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"server":     string(server),
			"gameUserID": gameUserID,
		})
		return harukiAPIHelper.SuccessResponse[string](c, "game account binding deleted", nil)
	}
}

func handleGetUserSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.social_platform.get"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		info, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(socialplatforminfo.HasUserWith(userSchema.IDEQ(targetUser.ID))).
			Only(c.Context())
		if err != nil && !postgresql.IsNotFound(err) {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_social_platform_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platform info")
		}

		resp := adminUserSocialPlatformResponse{
			GeneratedAt: time.Now().UTC(),
			UserID:      targetUser.ID,
			Exists:      info != nil,
		}
		if info != nil {
			resp.SocialPlatform = &harukiAPIHelper.SocialPlatformInfo{
				Platform: info.Platform,
				UserID:   info.PlatformUserID,
				Verified: info.Verified,
			}
		}

		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"exists": resp.Exists,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpsertUserSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.social_platform.upsert"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		payload, err := parseAdminManagedSocialPlatformPayload(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		conflictExists, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(
				socialplatforminfo.PlatformEQ(payload.Platform),
				socialplatforminfo.PlatformUserIDEQ(payload.UserID),
				socialplatforminfo.Not(socialplatforminfo.HasUserWith(userSchema.IDEQ(targetUser.ID))),
			).
			Exist(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_conflict_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to check social platform conflict")
		}
		if conflictExists {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("social_platform_conflict", nil))
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "social platform already bound by another user", nil)
		}

		existing, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(socialplatforminfo.HasUserWith(userSchema.IDEQ(targetUser.ID))).
			Only(c.Context())
		if err != nil && !postgresql.IsNotFound(err) {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_social_platform_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platform info")
		}

		created := false
		if existing != nil {
			if _, err := existing.Update().
				SetPlatform(payload.Platform).
				SetPlatformUserID(payload.UserID).
				SetVerified(*payload.Verified).
				Save(c.Context()); err != nil {
				if postgresql.IsConstraintError(err) {
					writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("social_platform_conflict", nil))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "social platform conflict", nil)
				}
				writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_social_platform_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to update social platform info")
			}
		} else {
			created = true
			if _, err := apiHelper.DBManager.DB.SocialPlatformInfo.Create().
				SetPlatform(payload.Platform).
				SetPlatformUserID(payload.UserID).
				SetVerified(*payload.Verified).
				SetUserSocialPlatformInfo(targetUser.ID).
				Save(c.Context()); err != nil {
				if postgresql.IsConstraintError(err) {
					writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("social_platform_conflict", nil))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "social platform conflict", nil)
				}
				writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("create_social_platform_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to create social platform info")
			}
		}

		resp := adminUserSocialPlatformResponse{
			GeneratedAt: time.Now().UTC(),
			UserID:      targetUser.ID,
			Exists:      true,
			SocialPlatform: &harukiAPIHelper.SocialPlatformInfo{
				Platform: payload.Platform,
				UserID:   payload.UserID,
				Verified: *payload.Verified,
			},
		}
		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"platform": payload.Platform,
			"created":  created,
			"verified": *payload.Verified,
		})
		return harukiAPIHelper.SuccessResponse(c, "social platform upserted", &resp)
	}
}

func handleClearUserSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.social_platform.clear"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		affected, err := apiHelper.DBManager.DB.SocialPlatformInfo.Delete().
			Where(socialplatforminfo.HasUserWith(userSchema.IDEQ(targetUser.ID))).
			Exec(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("clear_social_platform_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to clear social platform info")
		}

		resp := adminUserSocialPlatformResponse{
			GeneratedAt: time.Now().UTC(),
			UserID:      targetUser.ID,
			Exists:      false,
		}
		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"deleted": affected > 0,
		})
		return harukiAPIHelper.SuccessResponse(c, "social platform cleared", &resp)
	}
}

func handleListUserAuthorizedSocialPlatforms(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.authorized_social_platforms.list"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		rows, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
			Where(authorizesocialplatforminfo.UserIDEQ(targetUser.ID)).
			All(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_authorized_social_platforms_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query authorized social platforms")
		}

		resp := adminUserAuthorizedSocialListResponse{
			GeneratedAt: time.Now().UTC(),
			UserID:      targetUser.ID,
			Total:       len(rows),
			Items:       buildAdminAuthorizedSocialItems(rows),
		}
		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpsertUserAuthorizedSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.authorized_social_platform.upsert"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		platformID, err := strconv.Atoi(strings.TrimSpace(c.Params("platform_id")))
		if err != nil || platformID <= 0 {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_platform_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "platform_id must be positive integer")
		}

		payload, err := parseAdminManagedAuthorizedSocialPayload(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		existing, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
			Where(
				authorizesocialplatforminfo.UserIDEQ(targetUser.ID),
				authorizesocialplatforminfo.PlatformIDEQ(platformID),
			).
			Only(c.Context())
		if err != nil && !postgresql.IsNotFound(err) {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_authorized_social_platform_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query authorized social platform")
		}

		created := false
		if existing != nil {
			if _, err := existing.Update().
				SetPlatform(payload.Platform).
				SetPlatformUserID(payload.UserID).
				SetComment(payload.Comment).
				Save(c.Context()); err != nil {
				writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_authorized_social_platform_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to update authorized social platform")
			}
		} else {
			created = true
			if _, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Create().
				SetUserID(targetUser.ID).
				SetPlatform(payload.Platform).
				SetPlatformUserID(payload.UserID).
				SetPlatformID(platformID).
				SetComment(payload.Comment).
				Save(c.Context()); err != nil {
				if postgresql.IsConstraintError(err) {
					writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("authorized_social_platform_conflict", nil))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "authorized social platform conflict", nil)
				}
				writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("create_authorized_social_platform_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to create authorized social platform")
			}
		}

		resp := adminUserAuthorizedSocialUpsertResponse{
			UserID:     targetUser.ID,
			PlatformID: platformID,
			Created:    created,
			Record: harukiAPIHelper.AuthorizeSocialPlatformInfo{
				ID:       platformID,
				Platform: payload.Platform,
				UserID:   payload.UserID,
				Comment:  payload.Comment,
			},
		}
		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"platformID": platformID,
			"created":    created,
		})
		return harukiAPIHelper.SuccessResponse(c, "authorized social platform upserted", &resp)
	}
}

func handleDeleteUserAuthorizedSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.authorized_social_platform.delete"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		platformID, err := strconv.Atoi(strings.TrimSpace(c.Params("platform_id")))
		if err != nil || platformID <= 0 {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_platform_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "platform_id must be positive integer")
		}

		affected, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Delete().
			Where(
				authorizesocialplatforminfo.UserIDEQ(targetUser.ID),
				authorizesocialplatforminfo.PlatformIDEQ(platformID),
			).
			Exec(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("delete_authorized_social_platform_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete authorized social platform")
		}
		if affected == 0 {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("authorized_social_platform_not_found", nil))
			return harukiAPIHelper.ErrorNotFound(c, "authorized social platform not found")
		}

		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"platformID": platformID,
		})
		return harukiAPIHelper.SuccessResponse[string](c, "authorized social platform deleted", nil)
	}
}

func handleRegenerateUserIOSUploadCode(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.ios_upload_code.regenerate"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		code, err := generateAdminIOSUploadCode()
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("generate_upload_code_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to generate upload code")
		}

		existing, err := apiHelper.DBManager.DB.IOSScriptCode.Query().
			Where(iosscriptcode.UserIDEQ(targetUser.ID)).
			Only(c.Context())
		if err != nil && !postgresql.IsNotFound(err) {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_ios_upload_code_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query upload code")
		}

		if existing != nil {
			if _, err := existing.Update().SetUploadCode(code).Save(c.Context()); err != nil {
				writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_ios_upload_code_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to update upload code")
			}
		} else {
			if _, err := apiHelper.DBManager.DB.IOSScriptCode.Create().
				SetUserID(targetUser.ID).
				SetUploadCode(code).
				Save(c.Context()); err != nil {
				if postgresql.IsConstraintError(err) {
					writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("ios_upload_code_conflict", nil))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "upload code conflict", nil)
				}
				writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("create_ios_upload_code_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to create upload code")
			}
		}

		resp := adminUserIOSUploadCodeResponse{UserID: targetUser.ID, UploadCode: code}
		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "ios upload code regenerated", &resp)
	}
}

func handleClearUserIOSUploadCode(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.user.ios_upload_code.clear"
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to resolve target user")
		}

		affected, err := apiHelper.DBManager.DB.IOSScriptCode.Delete().
			Where(iosscriptcode.UserIDEQ(targetUser.ID)).
			Exec(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("clear_ios_upload_code_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to clear ios upload code")
		}

		resp := adminUserClearIOSUploadCodeResponse{
			UserID:  targetUser.ID,
			Cleared: affected > 0,
		}
		writeAdminAuditLog(c, apiHelper, action, "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"cleared": resp.Cleared,
		})
		return harukiAPIHelper.SuccessResponse(c, "ios upload code cleared", &resp)
	}
}
