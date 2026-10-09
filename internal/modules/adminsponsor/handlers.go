package adminsponsor

import (
	"errors"
	"strconv"
	"strings"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	sharedSponsor "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"

	"github.com/gofiber/fiber/v3"
)

const (
	adminSponsorActionList          = "admin.sponsor.list"
	adminSponsorActionDetail        = "admin.sponsor.detail"
	adminSponsorActionCreate        = "admin.sponsor.create"
	adminSponsorActionUpdate        = "admin.sponsor.update"
	adminSponsorActionSyncAfdian    = "admin.sponsor.sync_afdian"
	adminSponsorActionManualCreate  = "admin.sponsor.manual_duration.create"
	adminSponsorActionManualUpdate  = "admin.sponsor.manual_duration.update"
	adminSponsorActionManualDelete  = "admin.sponsor.manual_duration.delete"
	adminSponsorTargetType          = "sponsor"
	adminSponsorManualEntryMetaKey  = "entry_id"
	adminSponsorNotFoundMessage     = "sponsor not found"
	adminSponsorQueryFailedMessage  = "failed to query sponsor"
	adminSponsorManualFailedMessage = "failed to save manual duration"
)

func auditFailure(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action, targetID, reason string, extra map[string]any) {
	adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminSponsorTargetType, targetID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(reason, extra))
}

func handleAdminListSponsors(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		rows, err := sharedSponsor.QuerySponsors(c.Context(), apiHelper.DBManager.DB)
		if err != nil {
			auditFailure(c, apiHelper, adminSponsorActionList, "all", "query_sponsors_failed", nil)
			return harukiAPIHelper.ErrorInternal(c, "failed to query sponsors")
		}
		now := time.Now().UTC()
		items := make([]adminSponsorItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, buildAdminSponsorItem(row, now))
		}
		resp := adminSponsorListResponse{
			GeneratedAt: now,
			Total:       len(items),
			Items:       items,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionList, adminSponsorTargetType, "all", harukiAPIHelper.SystemLogResultSuccess, map[string]any{"total": resp.Total})
		return harukiAPIHelper.Responses.SuccessResponse(c, "success", &resp)
	}
}

// loadSponsorOr404 loads the sponsor named by the route or writes the error.
func loadSponsorOr404(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string) (*postgresql.Sponsor, error) {
	sponsorID := c.Params("sponsor_id")
	if sponsorID == "" {
		return nil, harukiAPIHelper.ErrorBadRequest(c, "sponsor_id is required")
	}
	row, err := apiHelper.DBManager.DB.Sponsor.Query().Where(sponsorSchema.IDEQ(sponsorID)).Only(c.Context())
	if err != nil {
		if postgresql.IsNotFound(err) {
			auditFailure(c, apiHelper, action, sponsorID, "sponsor_not_found", nil)
			return nil, harukiAPIHelper.ErrorNotFound(c, adminSponsorNotFoundMessage)
		}
		auditFailure(c, apiHelper, action, sponsorID, "query_sponsor_failed", nil)
		return nil, harukiAPIHelper.ErrorInternal(c, adminSponsorQueryFailedMessage)
	}
	return row, nil
}

// respondDetail builds the sponsor detail and writes the audit entry for
// action: success (with meta) only once the detail is built, failure when
// building it fails.
func respondDetail(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action, sponsorID, message string, meta map[string]any) error {
	resp, err := loadAdminSponsorDetail(c, apiHelper, sponsorID)
	if err != nil {
		auditFailure(c, apiHelper, action, sponsorID, "build_sponsor_detail_failed", meta)
		return harukiAPIHelper.ErrorInternal(c, adminSponsorQueryFailedMessage)
	}
	adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminSponsorTargetType, sponsorID, harukiAPIHelper.SystemLogResultSuccess, meta)
	return harukiAPIHelper.Responses.SuccessResponse(c, message, &resp)
}

func loadAdminSponsorDetail(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, sponsorID string) (adminSponsorDetailResponse, error) {
	row, err := apiHelper.DBManager.DB.Sponsor.Get(c.Context(), sponsorID)
	if err != nil {
		return adminSponsorDetailResponse{}, err
	}
	return buildAdminSponsorDetail(c.Context(), apiHelper.DBManager.DB, row, time.Now().UTC())
}

func handleAdminGetSponsor(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		row, err := loadSponsorOr404(c, apiHelper, adminSponsorActionDetail)
		if row == nil {
			return err
		}
		return respondDetail(c, apiHelper, adminSponsorActionDetail, row.ID, "success", nil)
	}
}

func handleAdminCreateSponsor(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload adminSponsorCreatePayload
		if err := c.Bind().Body(&payload); err != nil {
			auditFailure(c, apiHelper, adminSponsorActionCreate, "new", "invalid_request_payload", nil)
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		name, err := trimOptional(&payload.Name, 128, "name")
		if err != nil || name == nil || *name == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "name is required")
		}
		for _, field := range []struct {
			value *string
			max   int
			name  string
		}{{&payload.Avatar, 500, "avatar"}, {&payload.PlanName, 128, "planName"}, {&payload.Message, 1000, "message"}} {
			if _, err := trimOptional(field.value, field.max, field.name); err != nil {
				return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid "+field.name)
			}
		}
		row, err := sharedSponsor.CreateManualSponsor(c.Context(), apiHelper.DBManager.DB, sharedSponsor.ManualSponsorInput{
			Name:     *name,
			Avatar:   payload.Avatar,
			PlanName: payload.PlanName,
			Message:  payload.Message,
		}, time.Now().UTC())
		if err != nil {
			auditFailure(c, apiHelper, adminSponsorActionCreate, "new", "create_sponsor_failed", nil)
			return harukiAPIHelper.ErrorInternal(c, "failed to create sponsor")
		}
		return respondDetail(c, apiHelper, adminSponsorActionCreate, row.ID, "sponsor created", nil)
	}
}

func handleAdminUpdateSponsor(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		sponsorID := c.Params("sponsor_id")
		if sponsorID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "sponsor_id is required")
		}

		var payload adminSponsorUpdatePayload
		if err := c.Bind().Body(&payload); err != nil {
			auditFailure(c, apiHelper, adminSponsorActionUpdate, sponsorID, "invalid_request_payload", nil)
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		row, err := loadSponsorOr404(c, apiHelper, adminSponsorActionUpdate)
		if row == nil {
			return err
		}

		updater := row.Update()
		if name, err := trimOptional(payload.Name, 128, "name"); err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid name")
		} else if name != nil {
			if *name == "" {
				updater.ClearName()
			} else {
				updater.SetName(*name)
			}
		}
		if avatar, err := trimOptional(payload.Avatar, 500, "avatar"); err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid avatar")
		} else if avatar != nil {
			if *avatar == "" {
				updater.ClearAvatar()
			} else {
				updater.SetAvatar(*avatar)
			}
		}
		if planName, err := trimOptional(payload.PlanName, 128, "planName"); err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid planName")
		} else if planName != nil {
			if *planName == "" {
				updater.ClearPlanName()
			} else {
				updater.SetPlanName(*planName)
			}
		}
		if message, err := trimOptional(payload.Message, 1000, "message"); err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid message")
		} else if message != nil {
			if *message == "" {
				updater.ClearMessage()
			} else {
				updater.SetMessage(*message)
			}
		}
		if source, err := parseSource(payload.Source); err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid source")
		} else if source != nil {
			updater.SetSource(*source)
		}
		if payload.AfdianSyncDisabled != nil {
			updater.SetAfdianSyncDisabled(*payload.AfdianSyncDisabled)
		}
		if paidAt, provided, err := parseOptionalTime(payload.PaidAt, "paidAt"); err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid paidAt")
		} else if provided {
			if paidAt == nil {
				updater.ClearPaidAt()
			} else {
				updater.SetPaidAt(*paidAt)
			}
		}
		// isActive and planExpiresAt are derived now; older clients still
		// send them with every save, so they are ignored rather than refused.

		updated, err := updater.Save(c.Context())
		if err != nil {
			auditFailure(c, apiHelper, adminSponsorActionUpdate, sponsorID, "update_sponsor_failed", nil)
			return harukiAPIHelper.ErrorInternal(c, "failed to update sponsor")
		}

		resp := adminSponsorMutationResponse{Sponsor: buildAdminSponsorItem(updated, time.Now().UTC())}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionUpdate, adminSponsorTargetType, sponsorID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.Responses.SuccessResponse(c, "sponsor updated", &resp)
	}
}

func manualEntryMeta(entry *postgresql.SponsorManualDuration) map[string]any {
	return map[string]any{
		adminSponsorManualEntryMetaKey: entry.ID,
		"amount":                       entry.Amount,
		"unit":                         string(entry.Unit),
	}
}

func parseManualPayload(c fiber.Ctx) (sharedSponsor.ManualDurationInput, error) {
	var payload adminManualDurationPayload
	if err := c.Bind().Body(&payload); err != nil {
		return sharedSponsor.ManualDurationInput{}, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}
	input := sharedSponsor.ManualDurationInput{Amount: payload.Amount, Unit: payload.Unit, Note: payload.Note}
	startsAt, provided, err := parseOptionalTime(payload.StartsAt, "startsAt")
	if err != nil {
		return input, err
	}
	if provided && startsAt != nil {
		input.StartsAt = startsAt
	}
	return input, nil
}

func parseEntryID(c fiber.Ctx) (int, error) {
	entryID, err := strconv.Atoi(strings.TrimSpace(c.Params("entry_id")))
	if err != nil || entryID <= 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, "invalid entry_id")
	}
	return entryID, nil
}

// respondManualError maps the sponsor module's manual-duration errors.
func respondManualError(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action, sponsorID string, entryID int, err error) error {
	meta := map[string]any{}
	if entryID > 0 {
		meta[adminSponsorManualEntryMetaKey] = entryID
	}
	switch {
	case postgresql.IsNotFound(err):
		auditFailure(c, apiHelper, action, sponsorID, "sponsor_not_found", meta)
		return harukiAPIHelper.ErrorNotFound(c, adminSponsorNotFoundMessage)
	case errors.Is(err, sharedSponsor.ErrManualDurationNotFound):
		auditFailure(c, apiHelper, action, sponsorID, "manual_duration_not_found", meta)
		return harukiAPIHelper.ErrorNotFound(c, "manual duration entry not found")
	case errors.Is(err, sharedSponsor.ErrSponsorNotSplit):
		auditFailure(c, apiHelper, action, sponsorID, "duration_migration_pending", meta)
		return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "duration migration has not run for this sponsor yet; run an Afdian sync first", nil)
	case errors.Is(err, sharedSponsor.ErrInvalidManualDuration):
		auditFailure(c, apiHelper, action, sponsorID, "invalid_manual_duration", meta)
		return harukiAPIHelper.ErrorBadRequest(c, "invalid manual duration: amount must be positive, unit day or month, note required (max 500), and it may not start in the future after a gap")
	default:
		auditFailure(c, apiHelper, action, sponsorID, "manual_duration_failed", meta)
		return harukiAPIHelper.ErrorInternal(c, adminSponsorManualFailedMessage)
	}
}

func handleAdminCreateManualDuration(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		sponsorID := c.Params("sponsor_id")
		actorID, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}
		input, err := parseManualPayload(c)
		if err != nil {
			auditFailure(c, apiHelper, adminSponsorActionManualCreate, sponsorID, "invalid_request_payload", nil)
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}
		entry, err := sharedSponsor.AddManualDuration(c.Context(), apiHelper.DBManager.DB, sponsorID, input, actorID, time.Now().UTC())
		if err != nil {
			return respondManualError(c, apiHelper, adminSponsorActionManualCreate, sponsorID, 0, err)
		}
		return respondDetail(c, apiHelper, adminSponsorActionManualCreate, sponsorID, "manual duration added", manualEntryMeta(entry))
	}
}

func handleAdminUpdateManualDuration(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		sponsorID := c.Params("sponsor_id")
		actorID, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}
		entryID, err := parseEntryID(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid entry_id")
		}
		input, err := parseManualPayload(c)
		if err != nil {
			auditFailure(c, apiHelper, adminSponsorActionManualUpdate, sponsorID, "invalid_request_payload", map[string]any{adminSponsorManualEntryMetaKey: entryID})
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}
		entry, err := sharedSponsor.UpdateManualDuration(c.Context(), apiHelper.DBManager.DB, sponsorID, entryID, input, actorID, time.Now().UTC())
		if err != nil {
			return respondManualError(c, apiHelper, adminSponsorActionManualUpdate, sponsorID, entryID, err)
		}
		return respondDetail(c, apiHelper, adminSponsorActionManualUpdate, sponsorID, "manual duration updated", manualEntryMeta(entry))
	}
}

func handleAdminDeleteManualDuration(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		sponsorID := c.Params("sponsor_id")
		entryID, err := parseEntryID(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid entry_id")
		}
		if err := sharedSponsor.DeleteManualDuration(c.Context(), apiHelper.DBManager.DB, sponsorID, entryID, time.Now().UTC()); err != nil {
			return respondManualError(c, apiHelper, adminSponsorActionManualDelete, sponsorID, entryID, err)
		}
		return respondDetail(c, apiHelper, adminSponsorActionManualDelete, sponsorID, "manual duration deleted", map[string]any{adminSponsorManualEntryMetaKey: entryID})
	}
}

func handleAdminSyncAfdianSponsors(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, afdianConfig sharedSponsor.AfdianConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		// The admin button always walks the whole order history, which also
		// runs any pending duration split.
		result, err := sharedSponsor.SyncAfdianSponsors(c.Context(), apiHelper.DBManager.DB, afdianConfig, time.Now().UTC(), sharedSponsor.SyncOptions{Full: true})
		if err != nil {
			// The error can carry upstream details; it stays in the audit log.
			auditFailure(c, apiHelper, adminSponsorActionSyncAfdian, "afdian", "afdian_sync_failed", map[string]any{"error": err.Error()})
			if errors.Is(err, sharedSponsor.ErrAfdianNotConfigured) {
				return harukiAPIHelper.ErrorBadRequest(c, "afdian api credentials are not configured")
			}
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "failed to sync afdian sponsors", nil)
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionSyncAfdian, adminSponsorTargetType, "afdian", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"imported":   result.Imported,
			"skipped":    result.Skipped,
			"orders":     result.Orders,
			"new_orders": result.NewOrders,
			"split":      result.Split,
		})
		return harukiAPIHelper.Responses.SuccessResponse(c, "afdian sponsors synced", &result)
	}
}
