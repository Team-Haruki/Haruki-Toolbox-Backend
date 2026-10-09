package adminsponsor

import (
	"context"
	"strconv"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"

	sharedSponsor "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"
	orderSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsorafdianorder"
	manualSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsormanualduration"

	"github.com/gofiber/fiber/v3"
)

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func amountStringToFloat(amount *string) *float64 {
	if amount == nil || strings.TrimSpace(*amount) == "" {
		return nil
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(*amount), 64)
	if err != nil {
		return nil
	}
	return &value
}

func buildAdminSponsorItem(row *postgresql.Sponsor, now time.Time) adminSponsorItem {
	category := sharedSponsor.SponsorCategory(row, now)
	return adminSponsorItem{
		ID:                       row.ID,
		Name:                     sharedSponsor.DisplayName(row),
		Avatar:                   stringPtrValue(row.Avatar),
		PlanName:                 sharedSponsor.DisplayPlanName(row, category),
		Message:                  stringPtrValue(row.Message),
		Source:                   string(row.Source),
		Category:                 string(category),
		IsActive:                 category == sharedSponsor.CategoryCurrent,
		AfdianSyncDisabled:       row.AfdianSyncDisabled,
		TotalAmount:              amountStringToFloat(row.TotalAmount),
		Month:                    row.PlanPayMonths,
		PaidAt:                   row.PaidAt,
		PlanExpiresAt:            row.PlanExpiresAt,
		AfdianExpiresAt:          row.AfdianExpiresAt,
		AfdianMonths:             row.AfdianDurationMonths,
		DurationMigrationPending: row.DurationSplitAt == nil,
		CreatedAt:                row.CreatedAt,
		UpdatedAt:                row.UpdatedAt,
	}
}

func orderKindLabel(kind sharedSponsor.AfdianOrderKind) string {
	switch kind {
	case sharedSponsor.AfdianOrderDuration:
		return "duration"
	case sharedSponsor.AfdianOrderOneTime:
		return "one_time"
	default:
		return "ignored"
	}
}

func optionalAmount(amount string) *float64 {
	return amountStringToFloat(&amount)
}

func buildAdminSponsorDetail(ctx context.Context, db *postgresql.Client, row *postgresql.Sponsor, now time.Time) (adminSponsorDetailResponse, error) {
	var resp adminSponsorDetailResponse
	resp.Sponsor = buildAdminSponsorItem(row, now)

	orders, err := db.SponsorAfdianOrder.Query().
		Where(orderSchema.SponsorIDEQ(row.ID)).
		Order(orderSchema.ByPaidAt(sql.OrderDesc())).
		All(ctx)
	if err != nil {
		return resp, err
	}
	entries, err := db.SponsorManualDuration.Query().
		Where(manualSchema.SponsorIDEQ(row.ID)).
		Order(manualSchema.ByStartsAt(), manualSchema.ByID()).
		All(ctx)
	if err != nil {
		return resp, err
	}
	durations := sharedSponsor.MergeSponsorDurations(row, orders, entries)

	resp.Afdian.ExpiresAt = durations.Afdian.End
	resp.Afdian.Months = durations.Afdian.Months
	resp.Afdian.ReportedExpiresAt = row.AfdianReportedExpiresAt
	resp.Afdian.ReportedAt = row.AfdianReportedAt
	resp.Afdian.Orders = make([]adminAfdianOrderItem, 0, len(orders))
	for _, order := range orders {
		resp.Afdian.Orders = append(resp.Afdian.Orders, adminAfdianOrderItem{
			OutTradeNo:  order.ID,
			PlanID:      order.PlanID,
			PlanTitle:   order.PlanTitle,
			ProductType: order.ProductType,
			Month:       order.Month,
			Kind:        orderKindLabel(sharedSponsor.ClassifyAfdianOrder(sharedSponsor.OrderFacts(order))),
			TotalAmount: optionalAmount(order.TotalAmount),
			ShowAmount:  optionalAmount(order.ShowAmount),
			Remark:      order.Remark,
			PaidAt:      order.PaidAt,
		})
	}
	resp.ManualDurations = make([]adminManualDurationItem, 0, len(entries))
	for _, entry := range entries {
		resp.ManualDurations = append(resp.ManualDurations, buildManualItem(entry))
	}
	// A split row's cached expiry equals this; a pending row shows the merge
	// it will get.
	resp.EffectiveExpiresAt = row.PlanExpiresAt
	if row.DurationSplitAt != nil {
		resp.EffectiveExpiresAt = durations.EffectiveExpiresAt
	}
	return resp, nil
}

func buildManualItem(entry *postgresql.SponsorManualDuration) adminManualDurationItem {
	item := adminManualDurationItem{
		ID:        entry.ID,
		Amount:    entry.Amount,
		Unit:      string(entry.Unit),
		StartsAt:  entry.StartsAt,
		Note:      entry.Note,
		Origin:    string(entry.Origin),
		CreatedBy: entry.CreatedBy,
		CreatedAt: entry.CreatedAt,
		UpdatedBy: stringPtrValue(entry.UpdatedBy),
	}
	if entry.UpdatedBy != nil {
		updatedAt := entry.UpdatedAt
		item.UpdatedAt = &updatedAt
	}
	return item
}

func trimOptional(value *string, max int, fieldName string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if max > 0 && len(trimmed) > max {
		return nil, fiber.NewError(fiber.StatusBadRequest, fieldName+" exceeds max length")
	}
	return &trimmed, nil
}

func parseOptionalTime(value *string, fieldName string) (*time.Time, bool, error) {
	if value == nil {
		return nil, false, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, true, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		parsed, err = time.Parse("2006-01-02T15:04", trimmed)
	}
	if err != nil {
		return nil, false, fiber.NewError(fiber.StatusBadRequest, "invalid "+fieldName)
	}
	utc := parsed.UTC()
	return &utc, true, nil
}

func parseSource(value *string) (*sponsorSchema.Source, error) {
	if value == nil {
		return nil, nil
	}
	source := sponsorSchema.Source(strings.ToLower(strings.TrimSpace(*value)))
	switch source {
	case sponsorSchema.SourceAfdian, sponsorSchema.SourceManual, sponsorSchema.SourceLegacy, sponsorSchema.SourceImported:
		return &source, nil
	default:
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid source")
	}
}
