package admingamebindings

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

func handleAdminListGlobalGameAccountBindings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		filters, err := parseAdminGlobalGameBindingQueryFilters(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalList, adminAuditTargetTypeGameAccount, adminAuditTargetIDAll, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidQueryFilters, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid query filters")
		}

		dbCtx := c.Context()
		baseQuery := applyAdminGlobalGameBindingFilters(apiHelper.DBManager.DB.GameAccountBinding.Query(), filters, actorRole)

		total, err := baseQuery.Clone().Count(dbCtx)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalList, adminAuditTargetTypeGameAccount, adminAuditTargetIDAll, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountBindingsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count game account bindings")
		}

		offset := (filters.Page - 1) * filters.PageSize
		rows, err := applyAdminGlobalGameBindingSort(baseQuery.Clone(), filters.Sort).
			Limit(filters.PageSize).
			Offset(offset).
			WithUser(func(query *postgresql.UserQuery) {
				query.Select(userSchema.FieldID, userSchema.FieldName, userSchema.FieldEmail, userSchema.FieldRole)
			}).
			All(dbCtx)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalList, adminAuditTargetTypeGameAccount, adminAuditTargetIDAll, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryBindingsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query game account bindings")
		}

		totalPages := platformPagination.CalculateTotalPages(total, filters.PageSize)
		resp := adminGlobalGameBindingListResponse{
			GeneratedAt: adminNowUTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     platformPagination.HasMoreByOffset(filters.Page, filters.PageSize, total),
			Sort:        filters.Sort,
			Filters: adminGlobalGameBindingAppliedFilters{
				Query:      filters.Query,
				Server:     filters.Server,
				GameUserID: filters.GameUserID,
				UserID:     filters.UserID,
				Verified:   filters.Verified,
			},
			Items: buildAdminGlobalGameBindingItems(rows),
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalList, adminAuditTargetTypeGameAccount, adminAuditTargetIDAll, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminDeleteGlobalGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		server, gameUserID, err := parseAdminGameBindingPath(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalDelete, adminAuditTargetTypeGameAccount, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidPathParams, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid path params")
		}

		row, err := queryGameBindingWithOwner(c, apiHelper, server, gameUserID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalDelete, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonBindingNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "binding not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalDelete, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryBindingFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query binding")
		}
		if err := ensureAdminCanManageBindingOwner(actorUserID, actorRole, row); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalDelete, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, nil))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		if err := apiHelper.DBManager.DB.GameAccountBinding.DeleteOneID(row.ID).Exec(c.Context()); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalDelete, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteBindingFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete binding")
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalDelete, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"sourceUserID": row.Edges.User.ID,
		})
		return harukiAPIHelper.SuccessResponse[string](c, "binding deleted", nil)
	}
}

func handleAdminReassignGlobalGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		server, gameUserID, err := parseAdminGameBindingPath(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalReassign, adminAuditTargetTypeGameAccount, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidPathParams, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid path params")
		}

		targetUserID, err := parseAdminGameBindingReassignPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalReassign, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		targetUser, err := queryManageableTargetUserByID(c, apiHelper, actorUserID, actorRole, targetUserID)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalReassign, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidTargetUser, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid target user")
		}

		row, err := queryGameBindingWithOwner(c, apiHelper, server, gameUserID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalReassign, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonBindingNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "binding not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalReassign, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryBindingFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query binding")
		}
		if err := ensureAdminCanManageBindingOwner(actorUserID, actorRole, row); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalReassign, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, nil))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		resp := adminGlobalGameBindingReassignResponse{
			Server:       server,
			GameUserID:   gameUserID,
			FromUserID:   row.Edges.User.ID,
			TargetUserID: targetUser.ID,
			Changed:      row.Edges.User.ID != targetUser.ID,
		}
		if resp.Changed {
			if _, err := row.Update().SetUserID(targetUser.ID).Save(c.Context()); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalReassign, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonReassignBindingFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to reassign binding")
			}
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalReassign, adminAuditTargetTypeGameAccount, server+":"+gameUserID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"sourceUserID": row.Edges.User.ID,
			"targetUserID": targetUser.ID,
			"changed":      resp.Changed,
		})
		return harukiAPIHelper.SuccessResponse(c, "binding reassigned", &resp)
	}
}

func handleAdminBatchDeleteGlobalGameAccountBindings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload adminGlobalGameBindingBatchDeletePayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalBatchDelete, adminAuditTargetTypeGameAccount, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		items, err := sanitizeAdminBatchGameBindingRefs(payload.Items)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalBatchDelete, adminAuditTargetTypeGameAccount, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidItems, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid items")
		}

		results := make([]adminGlobalGameBindingBatchDeleteItemResult, 0, len(items))
		successCount := 0
		for _, item := range items {
			result := adminGlobalGameBindingBatchDeleteItemResult{
				Server:     item.Server,
				GameUserID: item.GameUserID,
			}

			row, err := queryGameBindingWithOwner(c, apiHelper, item.Server, item.GameUserID)
			if err != nil {
				if postgresql.IsNotFound(err) {
					result.Code = adminFailureReasonBindingNotFound
					result.Message = "binding not found"
					results = append(results, result)
					continue
				}
				result.Code = adminFailureReasonQueryBindingFailed
				result.Message = "failed to query binding"
				results = append(results, result)
				continue
			}

			if err := ensureAdminCanManageBindingOwner(actorUserID, actorRole, row); err != nil {
				result.Code = adminFailureReasonPermissionDenied
				result.Message = "insufficient permissions"
				results = append(results, result)
				continue
			}

			if err := apiHelper.DBManager.DB.GameAccountBinding.DeleteOneID(row.ID).Exec(c.Context()); err != nil {
				result.Code = adminBatchResultCodeDeleteBindingFailed
				result.Message = "failed to delete binding"
				results = append(results, result)
				continue
			}

			result.Success = true
			successCount++
			results = append(results, result)
		}

		resp := adminGlobalGameBindingBatchDeleteResponse{
			Total:   len(items),
			Success: successCount,
			Failed:  len(items) - successCount,
			Results: results,
		}
		resultState := harukiAPIHelper.SystemLogResultSuccess
		if resp.Failed > 0 {
			resultState = harukiAPIHelper.SystemLogResultFailure
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalBatchDelete, adminAuditTargetTypeGameAccount, adminAuditTargetIDBatch, resultState, map[string]any{
			"total":   resp.Total,
			"success": resp.Success,
			"failed":  resp.Failed,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminBatchReassignGlobalGameAccountBindings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload adminGlobalGameBindingBatchReassignPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalBatchReassign, adminAuditTargetTypeGameAccount, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		items, err := sanitizeAdminBatchGameBindingReassignItems(payload.Items)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalBatchReassign, adminAuditTargetTypeGameAccount, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidItems, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid items")
		}

		targetUserCache := make(map[string]*postgresql.User, len(items))
		results := make([]adminGlobalGameBindingBatchReassignItemResult, 0, len(items))
		successCount := 0
		for _, item := range items {
			result := adminGlobalGameBindingBatchReassignItemResult{
				Server:       item.Server,
				GameUserID:   item.GameUserID,
				TargetUserID: item.TargetUserID,
			}

			row, err := queryGameBindingWithOwner(c, apiHelper, item.Server, item.GameUserID)
			if err != nil {
				if postgresql.IsNotFound(err) {
					result.Code = adminFailureReasonBindingNotFound
					result.Message = "binding not found"
					results = append(results, result)
					continue
				}
				result.Code = adminFailureReasonQueryBindingFailed
				result.Message = "failed to query binding"
				results = append(results, result)
				continue
			}

			if err := ensureAdminCanManageBindingOwner(actorUserID, actorRole, row); err != nil {
				result.Code = adminFailureReasonPermissionDenied
				result.Message = "insufficient permissions"
				results = append(results, result)
				continue
			}

			targetUser, ok := targetUserCache[item.TargetUserID]
			if !ok {
				targetUser, err = queryManageableTargetUserByID(c, apiHelper, actorUserID, actorRole, item.TargetUserID)
				if err != nil {
					result.Code = adminBatchResultCodeInvalidTargetUser
					if fiberErr, ok := err.(*fiber.Error); ok {
						result.Message = fiberErr.Message
					} else {
						result.Message = "invalid target user"
					}
					results = append(results, result)
					continue
				}
				targetUserCache[item.TargetUserID] = targetUser
			}

			result.FromUserID = row.Edges.User.ID
			result.Changed = row.Edges.User.ID != targetUser.ID
			if result.Changed {
				if _, err := row.Update().SetUserID(targetUser.ID).Save(c.Context()); err != nil {
					result.Code = adminBatchResultCodeReassignBindingFailed
					result.Message = "failed to reassign binding"
					results = append(results, result)
					continue
				}
			}

			result.Success = true
			successCount++
			results = append(results, result)
		}

		resp := adminGlobalGameBindingBatchReassignResponse{
			Total:   len(items),
			Success: successCount,
			Failed:  len(items) - successCount,
			Results: results,
		}
		resultState := harukiAPIHelper.SystemLogResultSuccess
		if resp.Failed > 0 {
			resultState = harukiAPIHelper.SystemLogResultFailure
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionGameAccountGlobalBatchReassign, adminAuditTargetTypeGameAccount, adminAuditTargetIDBatch, resultState, map[string]any{
			"total":   resp.Total,
			"success": resp.Success,
			"failed":  resp.Failed,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}
