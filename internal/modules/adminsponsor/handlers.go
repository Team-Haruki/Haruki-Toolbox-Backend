package adminsponsor

import (
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	sharedSponsor "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"

	"github.com/gofiber/fiber/v3"
)

const (
	adminSponsorActionList       = "admin.sponsor.list"
	adminSponsorActionUpdate     = "admin.sponsor.update"
	adminSponsorActionSyncAfdian = "admin.sponsor.sync_afdian"
	adminSponsorTargetType       = "sponsor"
)

func handleAdminListSponsors(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		rows, err := sharedSponsor.QuerySponsors(c.Context(), apiHelper.DBManager.DB)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionList, adminSponsorTargetType, "all", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata("query_sponsors_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query sponsors")
		}
		items := make([]adminSponsorItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, buildAdminSponsorItem(row))
		}
		resp := adminSponsorListResponse{
			GeneratedAt: time.Now().UTC(),
			Total:       len(items),
			Items:       items,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionList, adminSponsorTargetType, "all", harukiAPIHelper.SystemLogResultSuccess, map[string]any{"total": resp.Total})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
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
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionUpdate, adminSponsorTargetType, sponsorID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata("invalid_request_payload", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		row, err := apiHelper.DBManager.DB.Sponsor.Query().Where(sponsorSchema.IDEQ(sponsorID)).Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionUpdate, adminSponsorTargetType, sponsorID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata("sponsor_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "sponsor not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionUpdate, adminSponsorTargetType, sponsorID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata("query_sponsor_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query sponsor")
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
		if payload.IsActive != nil {
			updater.SetIsActive(*payload.IsActive)
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
		if expiresAt, provided, err := parseOptionalTime(payload.PlanExpiresAt, "planExpiresAt"); err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid planExpiresAt")
		} else if provided {
			if expiresAt == nil {
				updater.ClearPlanExpiresAt()
			} else {
				updater.SetPlanExpiresAt(*expiresAt)
			}
		}

		updated, err := updater.Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionUpdate, adminSponsorTargetType, sponsorID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata("update_sponsor_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update sponsor")
		}

		resp := adminSponsorMutationResponse{Sponsor: buildAdminSponsorItem(updated)}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionUpdate, adminSponsorTargetType, sponsorID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "sponsor updated", &resp)
	}
}

func handleAdminSyncAfdianSponsors(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		result, err := sharedSponsor.SyncAfdianSponsors(c.Context(), apiHelper.DBManager.DB, config.Cfg.Afdian, time.Now().UTC())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionSyncAfdian, adminSponsorTargetType, "afdian", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata("afdian_sync_failed", map[string]any{"error": err.Error()}))
			return harukiAPIHelper.ErrorBadRequest(c, "failed to sync afdian sponsors: "+err.Error())
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminSponsorActionSyncAfdian, adminSponsorTargetType, "afdian", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"imported": result.Imported,
			"skipped":  result.Skipped,
		})
		return harukiAPIHelper.SuccessResponse(c, "afdian sponsors synced", &result)
	}
}
