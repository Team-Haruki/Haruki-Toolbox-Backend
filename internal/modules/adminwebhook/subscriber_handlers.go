package adminwebhook

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/webhookendpoint"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/webhooksubscription"

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
		beforeID, err := pagination.ParsePositiveInt(c.Query("before_id"), 0, "before_id")
		if err != nil {
			return err
		}
		pageSize, err := pagination.ParsePositiveInt(c.Query("page_size"), postgresql.DefaultWebhookSubscriberPage, "page_size")
		if err != nil {
			return err
		}
		if pageSize > postgresql.MaxWebhookSubscriberPage {
			return harukiAPIHelper.ErrorBadRequest(c, "page_size exceeds max allowed size")
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

		db := apiHelper.DBManager.DB
		query := db.WebhookSubscription.Query().Where(webhooksubscription.WebhookIDEQ(webhookID))
		role, _ := c.Locals("userRole").(string)
		if role != adminCoreModule.RoleSuperAdmin {
			query.Where(func(s *sql.Selector) {
				users := sql.Table(userSchema.Table)
				s.Where(sql.Exists(sql.Select(users.C(userSchema.FieldID)).From(users).
					Where(sql.And(
						sql.ColumnsEQ(users.C(userSchema.FieldID), s.C(webhooksubscription.FieldUserID)),
						sql.NEQ(users.C(userSchema.FieldRole), userSchema.RoleSuperAdmin),
					))))
			})
		}
		total, err := query.Clone().Count(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count webhook subscribers")
		}
		rows, nextBeforeID, err := postgresql.ReadWebhookSubscriberPage(c.Context(), query, beforeID, pageSize)
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
			GeneratedAt:  adminWebhookNowUTC(),
			WebhookID:    webhookID,
			Total:        total,
			Items:        items,
			NextBeforeID: nextBeforeID,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionSubscribers, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.Responses.SuccessResponse(c, "success", &resp)
	}
}
