package adminwebhook

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/webhookendpoint"
	"haruki-suite/utils/database/postgresql/webhooksubscription"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func handleListAdminWebhookSubscribers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		webhookID, err := sanitizeWebhookID(c.Params("webhook_id"))
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionSubscribers, adminWebhookTargetType, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidWebhookID, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid webhook_id")
		}

		exists, err := apiHelper.DBManager.DB.WebhookEndpoint.Query().
			Where(webhookendpoint.IDEQ(webhookID)).
			Exist(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionSubscribers, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonQueryWebhooksFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query webhook")
		}
		if !exists {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionSubscribers, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonWebhookNotFound, nil))
			return harukiAPIHelper.ErrorNotFound(c, "webhook not found")
		}

		rows, err := apiHelper.DBManager.DB.WebhookSubscription.Query().
			Where(webhooksubscription.WebhookIDEQ(webhookID)).
			Order(webhooksubscription.ByCreatedAt(sql.OrderDesc()), webhooksubscription.ByID(sql.OrderAsc())).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionSubscribers, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonQuerySubscribersFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query webhook subscribers")
		}

		items := make([]adminWebhookSubscriberItem, 0, len(rows))
		for _, row := range rows {
			if row == nil {
				continue
			}
			item := adminWebhookSubscriberItem{
				UserID:   row.UserID,
				Server:   row.Server,
				DataType: row.DataType,
			}
			createdAt := row.CreatedAt.UTC()
			item.CreatedAt = &createdAt
			items = append(items, item)
		}

		resp := adminWebhookSubscribersResponse{
			GeneratedAt: adminWebhookNowUTC(),
			WebhookID:   webhookID,
			Total:       len(items),
			Items:       items,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionSubscribers, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}
