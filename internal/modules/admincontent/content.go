package admincontent

import (
	"strconv"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"

	"github.com/gofiber/fiber/v3"
)

const (
	friendLinkRouteSegment = "/friend-links"
	FriendLinkAdminPath    = "/api/admin/content" + friendLinkRouteSegment
	FriendLinkBodyLimit    = 16 << 10
)

type adminFriendLinkPayload struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Avatar         string   `json:"avatar"`
	URL            string   `json:"url"`
	Tags           []string `json:"tags"`
	SortOrder      int      `json:"sortOrder"`
	SortOrderSnake int      `json:"sort_order"`
}

type adminFriendLinkItem struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Avatar      string   `json:"avatar"`
	URL         string   `json:"url"`
	Tags        []string `json:"tags"`
	SortOrder   int      `json:"sortOrder"`
}

type adminFriendLinksResponse struct {
	GeneratedAt time.Time             `json:"generatedAt"`
	Total       int                   `json:"total"`
	Items       []adminFriendLinkItem `json:"items"`
	NextPage    int                   `json:"nextPage,omitempty"`
}

type adminFriendGroupPayload struct {
	Group          string `json:"group"`
	GroupName      string `json:"groupName"`
	GroupNameSnake string `json:"group_name"`
	Name           string `json:"name"`
	SortOrder      int    `json:"sortOrder"`
	SortOrderSnake int    `json:"sort_order"`
}

type adminFriendGroupItemPayload struct {
	Name           string  `json:"name"`
	Avatar         *string `json:"avatar"`
	Bg             *string `json:"bg"`
	GroupInfo      string  `json:"groupInfo"`
	GroupInfoSnake string  `json:"group_info"`
	Detail         string  `json:"detail"`
	Description    string  `json:"description"`
	SortOrder      int     `json:"sortOrder"`
	SortOrderSnake int     `json:"sort_order"`
}

type adminFriendGroupItem struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Avatar    *string `json:"avatar,omitzero"`
	Bg        *string `json:"bg,omitzero"`
	GroupInfo string  `json:"groupInfo"`
	Detail    string  `json:"detail"`
	SortOrder int     `json:"sortOrder"`
}

type adminFriendGroup struct {
	ID        int                    `json:"id"`
	Group     string                 `json:"group"`
	SortOrder int                    `json:"sortOrder"`
	GroupList []adminFriendGroupItem `json:"groupList"`
}

type adminFriendGroupsResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	TotalGroups int                `json:"totalGroups"`
	TotalItems  int                `json:"totalItems"`
	Items       []adminFriendGroup `json:"items"`
}

type adminFriendGroupCreateResponse struct {
	ID        int    `json:"id"`
	Group     string `json:"group"`
	SortOrder int    `json:"sortOrder"`
}

type adminFriendGroupItemResponse struct {
	GroupID int                  `json:"groupId"`
	Created bool                 `json:"created"`
	Item    adminFriendGroupItem `json:"item"`
}

func handleAdminListFriendLinks(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminContentActionFriendLinkList
		page, size, err := pagination.ParsePageAndPageSize(c, 1, postgresql.DefaultFriendLinkPageSize, postgresql.MaxFriendLinkPageSize)
		if err != nil {
			return err
		}
		db := apiHelper.DBManager.DB
		rows, hasMore, err := db.ReadFriendLinkPage(c.Context(), page, size)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, adminAuditTargetIDAll, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryFriendLinksFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query friend links")
		}
		total, err := db.FriendLink.Query().Count(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count friend links")
		}

		items := make([]adminFriendLinkItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, buildAdminFriendLinkItem(row))
		}

		resp := adminFriendLinksResponse{
			GeneratedAt: adminNowUTC(),
			Total:       total,
			Items:       items,
		}
		if hasMore {
			resp.NextPage = page + 1
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, adminAuditTargetIDAll, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.Responses.SuccessResponse(c, "success", &resp)
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
					return harukiAPIHelper.Responses.SuccessResponse(c, "friend link created", &resp)
				}
				if postgresql.IsConstraintError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendLinkConflict, nil))
					return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "friend link conflict", nil)
				}
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateFriendLinkFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create friend link")
		}

		resp := buildAdminFriendLinkItem(created)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(created.ID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.Responses.SuccessResponse(c, "friend link created", &resp)
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
			SetSortOrder(payload.SortOrder).
			Save(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendLinkNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "friend link not found")
			}
			if postgresql.IsConstraintError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendLinkConflict, nil))
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "friend link conflict", nil)
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateFriendLinkFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update friend link")
		}

		resp := buildAdminFriendLinkItem(updated)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendLink, strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.Responses.SuccessResponse(c, "friend link updated", &resp)
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
		return harukiAPIHelper.Responses.SuccessResponse[string](c, "friend link deleted", nil)
	}
}

func RegisterAdminContentRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	content := adminGroup.Group("/content", adminCoreModule.RequireAdmin(apiHelper))

	friendLinks := content.Group(friendLinkRouteSegment)
	friendLinks.Get("", handleAdminListFriendLinks(apiHelper))
	friendLinks.Post("", handleAdminCreateFriendLink(apiHelper))
	friendLinks.Put("/:id", handleAdminUpdateFriendLink(apiHelper))
	friendLinks.Delete("/:id", handleAdminDeleteFriendLink(apiHelper))

	friendGroups := content.Group("/friend-groups")
	friendGroups.Get("", handleAdminListFriendGroups(apiHelper))
	friendGroups.Post("", handleAdminCreateFriendGroup(apiHelper))
	friendGroups.Put("/:group_id", handleAdminUpdateFriendGroup(apiHelper))
	friendGroups.Delete("/:group_id", handleAdminDeleteFriendGroup(apiHelper))
	friendGroups.Post("/:group_id/items", handleAdminCreateFriendGroupItem(apiHelper))
	friendGroups.Put("/:group_id/items/:item_id", handleAdminUpdateFriendGroupItem(apiHelper))
	friendGroups.Delete("/:group_id/items/:item_id", handleAdminDeleteFriendGroupItem(apiHelper))
}
