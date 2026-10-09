package sponsor

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"
	"github.com/google/uuid"

	sql "entgo.io/ent/dialect/sql"
)

const (
	defaultSponsorPlanName = "爱发电赞助"
	// legacyOneTimePlanName is the label the pre-split code wrote over the
	// plan name of every lapsed sponsor.
	legacyOneTimePlanName = "一次性赞助"
	customPlanName        = "自选方案"
	anonymousSponsorName  = "匿名赞助者"
)

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func trimLimit(value string, max int) string {
	value = strings.TrimSpace(value)
	if max > 0 && len(value) > max {
		return value[:max]
	}
	return value
}

func stringPointerOrNil(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// SponsorCategory is the category of a stored sponsor row at now. It is the
// one entry point every reader uses (public wall, admin list, migration
// report). plan_expires_at is the effective expiry, or the legacy single
// expiry on a row the split has not reached yet.
func SponsorCategory(row *postgresql.Sponsor, now time.Time) Category {
	return CategoryFor(row.PlanExpiresAt, now)
}

// DisplayPlanName is the tier label shown for a sponsor. A stored
// "一次性赞助" is the leftover of the pre-split bug (a lapsed plan was renamed
// to it) and is never shown.
func DisplayPlanName(row *postgresql.Sponsor) string {
	planName := strings.TrimSpace(stringPtrValue(row.PlanName))
	if planName == legacyOneTimePlanName {
		planName = ""
	}
	if planName != "" {
		return planName
	}
	return defaultSponsorPlanName
}

// DisplayName is the public name, with the anonymous fallback.
func DisplayName(row *postgresql.Sponsor) string {
	name := strings.TrimSpace(stringPtrValue(row.Name))
	if name == "" {
		return anonymousSponsorName
	}
	return name
}

func sponsorItemFromRow(row *postgresql.Sponsor, now time.Time) SponsorItem {
	category := SponsorCategory(row, now)
	planName := DisplayPlanName(row)
	return SponsorItem{
		ID:     row.ID,
		Name:   DisplayName(row),
		Avatar: stringPtrValue(row.Avatar),
		Plan: &SponsorPlan{
			ID:        stringPtrValue(row.PlanID),
			Name:      planName,
			Title:     planName,
			Rank:      row.PlanRank,
			PayMonth:  row.PlanPayMonths,
			ExpiresAt: row.PlanExpiresAt,
		},
		PlanID:             stringPtrValue(row.PlanID),
		PlanName:           planName,
		PlanPrice:          amountStringToFloat(row.TotalAmount),
		PlanRank:           row.PlanRank,
		PlanPayMonths:      row.PlanPayMonths,
		Message:            stringPtrValue(row.Message),
		Source:             string(row.Source),
		Category:           category,
		IsActive:           category == CategoryCurrent,
		AfdianSyncDisabled: row.AfdianSyncDisabled,
		TotalAmount:        amountStringToFloat(row.TotalAmount),
		Month:              row.PlanPayMonths,
		PaidAt:             row.PaidAt,
		PlanExpiresAt:      row.PlanExpiresAt,
		SupportCount:       row.SupportCount,
	}
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

// BuildSponsorPageResponse builds the public wall. Every supporter is current
// or former, so activeCount + pastCount == supporterCount.
func BuildSponsorPageResponse(rows []*postgresql.Sponsor, now time.Time) SponsorPageResponse {
	items := make([]SponsorItem, 0, len(rows))
	summary := SponsorSummary{
		SupporterCount: len(rows),
		GeneratedAt:    now.UTC(),
	}
	for _, row := range rows {
		item := sponsorItemFromRow(row, now)
		if item.Category == CategoryCurrent {
			summary.ActiveCount++
		} else {
			summary.PastCount++
		}
		items = append(items, item)
	}
	sortSponsorItems(items)
	return SponsorPageResponse{Summary: summary, Supporters: items}
}

func sortSponsorItems(items []SponsorItem) {
	sort.SliceStable(items, func(i, j int) bool {
		// Primary: higher tier (plan rank) first.
		if items[i].PlanRank != items[j].PlanRank {
			return items[i].PlanRank > items[j].PlanRank
		}
		// Secondary: longer duration first (later expiry ahead; one-time/no-expiry last).
		ei, ej := items[i].PlanExpiresAt, items[j].PlanExpiresAt
		if (ei == nil) != (ej == nil) {
			return ej == nil
		}
		if ei != nil && ej != nil && !ei.Equal(*ej) {
			return ei.After(*ej)
		}
		if items[i].PlanName != items[j].PlanName {
			return items[i].PlanName < items[j].PlanName
		}
		if items[i].PaidAt == nil || items[j].PaidAt == nil {
			return items[j].PaidAt == nil
		}
		return items[i].PaidAt.Before(*items[j].PaidAt)
	})
}

func QuerySponsors(ctx context.Context, db *postgresql.Client) ([]*postgresql.Sponsor, error) {
	return db.Sponsor.Query().
		Order(
			sponsorSchema.ByPlanRank(sql.OrderDesc()),
			sponsorSchema.ByCreatedAt(sql.OrderAsc()),
		).
		All(ctx)
}

func stableSponsorID(afdianUserID string, outTradeNo string) string {
	afdianUserID = strings.TrimSpace(afdianUserID)
	if afdianUserID != "" {
		return "afdian_" + afdianUserID
	}
	outTradeNo = strings.TrimSpace(outTradeNo)
	if outTradeNo != "" {
		return "afdian_order_" + outTradeNo
	}
	return "sponsor_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// NewManualSponsorID is the id of a supporter an admin enters by hand.
func NewManualSponsorID() string {
	return "manual_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func parseAmountRank(amount string) int {
	value, err := strconv.ParseFloat(strings.TrimSpace(amount), 64)
	if err != nil || value <= 0 {
		return 0
	}
	return int(value * 100)
}

func parseUnixTime(raw any) *time.Time {
	switch v := raw.(type) {
	case float64:
		if v <= 0 {
			return nil
		}
		t := time.Unix(int64(v), 0).UTC()
		return &t
	case int64:
		if v <= 0 {
			return nil
		}
		t := time.Unix(v, 0).UTC()
		return &t
	case jsonvalue.Number:
		i, err := v.Int64()
		if err != nil || i <= 0 {
			return nil
		}
		t := time.Unix(i, 0).UTC()
		return &t
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		if i, err := strconv.ParseInt(v, 10, 64); err == nil && i > 0 {
			t := time.Unix(i, 0).UTC()
			return &t
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}

func readString(record map[string]any, keys ...string) string {
	if record == nil {
		return ""
	}
	for _, key := range keys {
		value, ok := record[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed
			}
		case jsonvalue.Number:
			return v.String()
		case float64:
			if v == float64(int64(v)) {
				return strconv.FormatInt(int64(v), 10)
			}
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			return strconv.Itoa(v)
		case int64:
			return strconv.FormatInt(v, 10)
		}
	}
	return ""
}

func readInt(record map[string]any, keys ...string) int {
	raw := readString(record, keys...)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func readMap(record map[string]any, keys ...string) map[string]any {
	if record == nil {
		return nil
	}
	for _, key := range keys {
		if value, ok := record[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}
