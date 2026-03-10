package admincontent

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/friendlink"
	"strconv"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

type adminFriendLinkPayload struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Avatar      string   `json:"avatar"`
	URL         string   `json:"url"`
	Tags        []string `json:"tags"`
}

type adminFriendLinkItem struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Avatar      string   `json:"avatar"`
	URL         string   `json:"url"`
	Tags        []string `json:"tags"`
}

type adminFriendLinksResponse struct {
	GeneratedAt time.Time             `json:"generatedAt"`
	Total       int                   `json:"total"`
	Items       []adminFriendLinkItem `json:"items"`
}

type adminFriendGroupPayload struct {
	Group          string `json:"group"`
	GroupName      string `json:"groupName"`
	GroupNameSnake string `json:"group_name"`
	Name           string `json:"name"`
}

type adminFriendGroupItemPayload struct {
	Name           string  `json:"name"`
	Avatar         *string `json:"avatar"`
	Bg             *string `json:"bg"`
	GroupInfo      string  `json:"groupInfo"`
	GroupInfoSnake string  `json:"group_info"`
	Detail         string  `json:"detail"`
	Description    string  `json:"description"`
}

type adminFriendGroupItem struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Avatar    *string `json:"avatar,omitempty"`
	Bg        *string `json:"bg,omitempty"`
	GroupInfo string  `json:"groupInfo"`
	Detail    string  `json:"detail"`
}

type adminFriendGroup struct {
	ID        int                    `json:"id"`
	Group     string                 `json:"group"`
	GroupList []adminFriendGroupItem `json:"groupList"`
}

type adminFriendGroupsResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	TotalGroups int                `json:"totalGroups"`
	TotalItems  int                `json:"totalItems"`
	Items       []adminFriendGroup `json:"items"`
}

type adminFriendGroupCreateResponse struct {
	ID    int    `json:"id"`
	Group string `json:"group"`
}

type adminFriendGroupItemResponse struct {
	GroupID int                  `json:"groupId"`
	Created bool                 `json:"created"`
	Item    adminFriendGroupItem `json:"item"`
}

func handleAdminListFriendLinks(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminContentActionFriendLinkList
		rows, err := apiHelper.DBManager.DB.FriendLink.Query().
			Order(friendlink.ByID(sql.OrderAsc())).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, adminAuditTargetIDAll, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryFriendLinksFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query friend links")
		}

		items := make([]adminFriendLinkItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, buildAdminFriendLinkItem(row))
		}

		resp := adminFriendLinksResponse{
			GeneratedAt: adminNowUTC(),
			Total:       len(items),
			Items:       items,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, adminAuditTargetIDAll, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminCreateFriendLink(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminContentActionFriendLinkCreate
		payload, err := parseAdminFriendLinkPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		builder := buildAdminFriendLinkCreateBuilder(apiHelper, payload)
		created, err := builder.Save(c.Context())
		if err != nil {
			if postgresql.IsConstraintError(err) {
				// Imported data may leave identity sequence behind existing max(id). Retry once with explicit next id.
				nextID, nextErr := queryNextFriendLinkID(c, apiHelper)
				if nextErr != nil {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonResolveFriendLinkNextIdFailed, map[string]any{
						"error": nextErr.Error(),
					}))
					return harukiAPIHelper.ErrorInternal(c, "failed to create friend link")
				}

				retryBuilder := buildAdminFriendLinkCreateBuilder(apiHelper, payload).SetID(nextID)
				created, err = retryBuilder.Save(c.Context())
				if err == nil {
					resp := buildAdminFriendLinkItem(created)
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(created.ID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
						"fallbackBySetID": true,
					})
					return harukiAPIHelper.SuccessResponse(c, "friend link created", &resp)
				}
				if postgresql.IsConstraintError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendLinkConflict, nil))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "friend link conflict", nil)
				}
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateFriendLinkFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create friend link")
		}

		resp := buildAdminFriendLinkItem(created)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(created.ID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "friend link created", &resp)
	}
}

func handleAdminUpdateFriendLink(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminContentActionFriendLinkUpdate
		friendLinkID, err := parseAdminPathPositiveInt(c.Params("id"), "id")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidFriendLinkId, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid id")
		}

		payload, err := parseAdminFriendLinkPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		updated, err := apiHelper.DBManager.DB.FriendLink.UpdateOneID(friendLinkID).
			SetName(payload.Name).
			SetDescription(payload.Description).
			SetAvatar(payload.Avatar).
			SetURL(payload.URL).
			SetTags(payload.Tags).
			Save(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendLinkNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "friend link not found")
			}
			if postgresql.IsConstraintError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendLinkConflict, nil))
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "friend link conflict", nil)
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateFriendLinkFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update friend link")
		}

		resp := buildAdminFriendLinkItem(updated)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "friend link updated", &resp)
	}
}

func handleAdminDeleteFriendLink(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminContentActionFriendLinkDelete
		friendLinkID, err := parseAdminPathPositiveInt(c.Params("id"), "id")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidFriendLinkId, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid id")
		}

		err = apiHelper.DBManager.DB.FriendLink.DeleteOneID(friendLinkID).Exec(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendLinkNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "friend link not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteFriendLinkFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete friend link")
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse[string](c, "friend link deleted", nil)
	}
}

func RegisterAdminContentRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := apiHelper.Router.Group("/api/admin", apiHelper.SessionHandler.VerifySessionToken)
	content := adminGroup.Group("/content", adminCoreModule.RequireAdmin(apiHelper))

	friendLinks := content.Group("/friend-links")
	friendLinks.Get("", handleAdminListFriendLinks(apiHelper))
	friendLinks.Post("", handleAdminCreateFriendLink(apiHelper))
	friendLinks.Put("/:id", handleAdminUpdateFriendLink(apiHelper))
	friendLinks.Delete("/:id", handleAdminDeleteFriendLink(apiHelper))

	friendGroups := content.Group("/friend-groups")
	friendGroups.Get("", handleAdminListFriendGroups(apiHelper))
	friendGroups.Post("", handleAdminCreateFriendGroup(apiHelper))
	friendGroups.Delete("/:group_id", handleAdminDeleteFriendGroup(apiHelper))
	friendGroups.Post("/:group_id/items", handleAdminCreateFriendGroupItem(apiHelper))
	friendGroups.Put("/:group_id/items/:item_id", handleAdminUpdateFriendGroupItem(apiHelper))
	friendGroups.Delete("/:group_id/items/:item_id", handleAdminDeleteFriendGroupItem(apiHelper))
}
