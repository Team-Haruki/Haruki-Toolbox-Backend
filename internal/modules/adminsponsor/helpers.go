package adminsponsor

import (
	"strconv"
	"strings"
	"time"

	sharedSponsor "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"

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

func buildAdminSponsorItem(row *postgresql.Sponsor) adminSponsorItem {
	planName := stringPtrValue(row.PlanName)
	if strings.TrimSpace(planName) == "" {
		planName = sharedSponsor.DefaultPlanName(row.PlanPayMonths, row.PlanExpiresAt)
	}
	name := stringPtrValue(row.Name)
	if strings.TrimSpace(name) == "" {
		name = "匿名赞助者"
	}
	return adminSponsorItem{
		ID:                 row.ID,
		Name:               name,
		Avatar:             stringPtrValue(row.Avatar),
		PlanName:           planName,
		Message:            stringPtrValue(row.Message),
		Source:             string(row.Source),
		IsActive:           row.IsActive,
		AfdianSyncDisabled: row.AfdianSyncDisabled,
		TotalAmount:        amountStringToFloat(row.TotalAmount),
		Month:              row.PlanPayMonths,
		PaidAt:             row.PaidAt,
		PlanExpiresAt:      row.PlanExpiresAt,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
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
