package admincontent

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/group"
	"haruki-suite/utils/database/postgresql/grouplist"
	"strconv"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func handleAdminListFriendGroups(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminContentActionFriendGroupList
		rows, err := apiHelper.DBManager.DB.Group.Query().
			WithGroupList().
			Order(group.ByID(sql.OrderAsc())).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, adminAuditTargetIDAll, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryFriendGroupsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query friend groups")
		}

		items := make([]adminFriendGroup, 0, len(rows))
		totalItems := 0
		for _, row := range rows {
			groupItems := buildAdminFriendGroupItems(row.Edges.GroupList)
			totalItems += len(groupItems)
			items = append(items, adminFriendGroup{
				ID:        row.ID,
				Group:     row.Group,
				GroupList: groupItems,
			})
		}

		resp := adminFriendGroupsResponse{
			GeneratedAt: adminNowUTC(),
			TotalGroups: len(items),
			TotalItems:  totalItems,
			Items:       items,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, adminAuditTargetIDAll, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"groups": resp.TotalGroups,
			"items":  resp.TotalItems,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminCreateFriendGroup(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminContentActionFriendGroupCreate
		payload, err := parseAdminFriendGroupPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		created, err := apiHelper.DBManager.DB.Group.Create().
			SetGroup(payload.Group).
			Save(c.Context())
		if err != nil {
			if postgresql.IsConstraintError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, payload.Group, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendGroupConflict, nil))
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "friend group already exists", nil)
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, payload.Group, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateFriendGroupFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create friend group")
		}

		resp := adminFriendGroupCreateResponse{ID: created.ID, Group: created.Group}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, strconv.Itoa(created.ID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "friend group created", &resp)
	}
}

func handleAdminDeleteFriendGroup(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminContentActionFriendGroupDelete
		groupID, err := parseAdminPathPositiveInt(c.Params("group_id"), "group_id")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidGroupId, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid group_id")
		}

		tx, err := apiHelper.DBManager.DB.Tx(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonStartTransactionFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to start transaction")
		}
		if _, err := tx.GroupList.Delete().Where(grouplist.HasGroupWith(group.IDEQ(groupID))).Exec(c.Context()); err != nil {
			_ = tx.Rollback()
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteGroupItemsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete group items")
		}
		if err := tx.Group.DeleteOneID(groupID).Exec(c.Context()); err != nil {
			_ = tx.Rollback()
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendGroupNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "friend group not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteFriendGroupFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete friend group")
		}
		if err := tx.Commit(); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCommitTransactionFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to commit friend group deletion")
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroup, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse[string](c, "friend group deleted", nil)
	}
}

func handleAdminCreateFriendGroupItem(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminContentActionFriendGroupItemCreate
		groupID, err := parseAdminPathPositiveInt(c.Params("group_id"), "group_id")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidGroupId, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid group_id")
		}

		payload, err := parseAdminFriendGroupItemPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		exists, err := apiHelper.DBManager.DB.Group.Query().Where(group.IDEQ(groupID)).Exist(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryGroupFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query friend group")
		}
		if !exists {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendGroupNotFound, nil))
			return harukiAPIHelper.ErrorNotFound(c, "friend group not found")
		}

		builder := buildAdminFriendGroupItemCreateBuilder(apiHelper, groupID, payload)
		created, err := builder.Save(c.Context())
		if err != nil {
			if postgresql.IsConstraintError(err) {
				// Imported data may leave identity sequence behind existing max(id). Retry once with explicit next id.
				nextID, nextErr := queryNextFriendGroupItemID(c, apiHelper)
				if nextErr != nil {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonResolveFriendGroupItemNextIdFailed, map[string]any{
						"error": nextErr.Error(),
					}))
					return harukiAPIHelper.ErrorInternal(c, "failed to create friend group item")
				}

				retryBuilder := buildAdminFriendGroupItemCreateBuilder(apiHelper, groupID, payload).SetID(nextID)
				created, err = retryBuilder.Save(c.Context())
				if err == nil {
					item := buildAdminFriendGroupItems([]*postgresql.GroupList{created})[0]
					resp := adminFriendGroupItemResponse{
						GroupID: groupID,
						Created: true,
						Item:    item,
					}
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(created.ID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
						"groupID":         groupID,
						"fallbackBySetID": true,
					})
					return harukiAPIHelper.SuccessResponse(c, "friend group item created", &resp)
				}
				if postgresql.IsConstraintError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendGroupItemConflict, map[string]any{
						"error": err.Error(),
					}))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "friend group item conflict", nil)
				}
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateFriendGroupItemFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create friend group item")
		}

		item := buildAdminFriendGroupItems([]*postgresql.GroupList{created})[0]
		resp := adminFriendGroupItemResponse{
			GroupID: groupID,
			Created: true,
			Item:    item,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(created.ID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"groupID": groupID,
		})
		return harukiAPIHelper.SuccessResponse(c, "friend group item created", &resp)
	}
}

func handleAdminUpdateFriendGroupItem(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminContentActionFriendGroupItemUpdate
		groupID, err := parseAdminPathPositiveInt(c.Params("group_id"), "group_id")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidGroupId, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid group_id")
		}
		itemID, err := parseAdminPathPositiveInt(c.Params("item_id"), "item_id")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidItemId, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid item_id")
		}

		payload, err := parseAdminFriendGroupItemPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		existing, err := apiHelper.DBManager.DB.GroupList.Query().
			Where(
				grouplist.IDEQ(itemID),
				grouplist.HasGroupWith(group.IDEQ(groupID)),
			).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendGroupItemNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "friend group item not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryFriendGroupItemFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query friend group item")
		}

		updater := existing.Update().
			SetName(payload.Name).
			SetGroupInfo(payload.GroupInfo).
			SetDetail(payload.Detail)
		if payload.Avatar != nil {
			if *payload.Avatar == "" {
				updater.ClearAvatar()
			} else {
				updater.SetAvatar(*payload.Avatar)
			}
		}
		if payload.Bg != nil {
			if *payload.Bg == "" {
				updater.ClearBg()
			} else {
				updater.SetBg(*payload.Bg)
			}
		}
		updated, err := updater.Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateFriendGroupItemFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update friend group item")
		}

		item := buildAdminFriendGroupItems([]*postgresql.GroupList{updated})[0]
		resp := adminFriendGroupItemResponse{
			GroupID: groupID,
			Created: false,
			Item:    item,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"groupID": groupID,
		})
		return harukiAPIHelper.SuccessResponse(c, "friend group item updated", &resp)
	}
}

func handleAdminDeleteFriendGroupItem(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminContentActionFriendGroupItemDelete
		groupID, err := parseAdminPathPositiveInt(c.Params("group_id"), "group_id")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidGroupId, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid group_id")
		}
		itemID, err := parseAdminPathPositiveInt(c.Params("item_id"), "item_id")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidItemId, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid item_id")
		}

		affected, err := apiHelper.DBManager.DB.GroupList.Delete().
			Where(
				grouplist.IDEQ(itemID),
				grouplist.HasGroupWith(group.IDEQ(groupID)),
			).
			Exec(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteFriendGroupItemFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete friend group item")
		}
		if affected == 0 {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonFriendGroupItemNotFound, nil))
			return harukiAPIHelper.ErrorNotFound(c, "friend group item not found")
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminContentTargetTypeFriendGroupItem, strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"groupID": groupID,
		})
		return harukiAPIHelper.SuccessResponse[string](c, "friend group item deleted", nil)
	}
}
