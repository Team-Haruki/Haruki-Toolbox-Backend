package adminrisk

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/riskrule"
	"strings"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func normalizeRiskRuleItem(row *postgresql.RiskRule) riskRuleItem {
	item := riskRuleItem{
		Key:       row.RuleKey,
		Config:    row.Config,
		UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.Description != nil {
		item.Description = *row.Description
	}
	if row.UpdatedBy != nil {
		item.UpdatedBy = *row.UpdatedBy
	}
	return item
}

func handleListRiskRules(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		rows, err := apiHelper.DBManager.DB.RiskRule.Query().
			Order(riskrule.ByRuleKey(sql.OrderAsc())).
			All(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query risk rules")
		}

		items := make([]riskRuleItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, normalizeRiskRuleItem(row))
		}
		resp := riskRuleListResponse{
			GeneratedAt: adminNowUTC(),
			Items:       items,
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpsertRiskRules(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload riskRuleUpsertPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		if len(payload.Rules) == 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "rules is required")
		}
		if len(payload.Rules) > 100 {
			return harukiAPIHelper.ErrorBadRequest(c, "too many rules in one request")
		}

		updated := make([]riskRuleItem, 0, len(payload.Rules))
		for _, item := range payload.Rules {
			key := strings.TrimSpace(item.Key)
			if key == "" {
				return harukiAPIHelper.ErrorBadRequest(c, "rule key is required")
			}
			configValue := item.Config
			if configValue == nil {
				configValue = map[string]any{}
			}
			existing, err := apiHelper.DBManager.DB.RiskRule.Query().
				Where(riskrule.RuleKeyEQ(key)).
				Only(c.Context())
			if err != nil && !postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorInternal(c, "failed to upsert risk rule")
			}

			description := strings.TrimSpace(item.Description)
			if existing == nil || postgresql.IsNotFound(err) {
				builder := apiHelper.DBManager.DB.RiskRule.Create().
					SetRuleKey(key).
					SetConfig(configValue).
					SetUpdatedBy(actorUserID)
				if description != "" {
					builder.SetDescription(description)
				}
				row, err := builder.Save(c.Context())
				if err != nil {
					return harukiAPIHelper.ErrorInternal(c, "failed to upsert risk rule")
				}
				updated = append(updated, normalizeRiskRuleItem(row))
				continue
			}

			update := existing.Update().
				SetConfig(configValue).
				SetUpdatedBy(actorUserID)
			if description != "" {
				update.SetDescription(description)
			} else {
				update.ClearDescription()
			}
			row, err := update.Save(c.Context())
			if err != nil {
				return harukiAPIHelper.ErrorInternal(c, "failed to upsert risk rule")
			}
			updated = append(updated, normalizeRiskRuleItem(row))
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionRiskRulesUpsert, adminAuditTargetTypeRiskRule, "", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"count": len(updated),
		})
		resp := riskRuleListResponse{GeneratedAt: adminNowUTC(), Items: updated}
		return harukiAPIHelper.SuccessResponse(c, "risk rules updated", &resp)
	}
}
