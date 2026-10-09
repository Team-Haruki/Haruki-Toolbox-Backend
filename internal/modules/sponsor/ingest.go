package sponsor

import (
	"context"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"
	orderSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsorafdianorder"
)

// parsedAfdianOrder is one order from query-order or a webhook.
type parsedAfdianOrder struct {
	OutTradeNo   string
	AfdianUserID string
	UserName     string
	PlanID       string
	PlanTitle    string
	ProductType  int
	Month        int
	Status       int
	TotalAmount  string
	ShowAmount   string
	Remark       string
	PaidAt       time.Time
}

func (o parsedAfdianOrder) facts() AfdianOrderFacts {
	return AfdianOrderFacts{
		OutTradeNo:  o.OutTradeNo,
		PlanID:      o.PlanID,
		ProductType: o.ProductType,
		Month:       o.Month,
		Status:      o.Status,
		PaidAt:      o.PaidAt,
	}
}

// parsedAfdianSponsor is one entry of query-sponsor: the supporter's profile
// and the plan expiry Afdian reports for them.
type parsedAfdianSponsor struct {
	ID                string
	AfdianUserID      string
	Name              string
	Avatar            string
	PlanID            string
	PlanName          string
	PlanRank          int
	LastPaidAt        *time.Time
	ReportedExpiresAt *time.Time
	TotalAmount       string
	Raw               map[string]any
}

// AfdianSyncResult summarises one synchronisation pass.
type AfdianSyncResult struct {
	Imported  int  `json:"imported"`
	Skipped   int  `json:"skipped"`
	Orders    int  `json:"orders"`
	NewOrders int  `json:"newOrders"`
	Full      bool `json:"full"`
	// Split counts rows whose legacy expiry was split in this pass.
	Split int `json:"split"`
}

// parseAfdianOrder reads an order object. Only completed payments (status 2)
// are accepted. create_time is not in the developer guide but query-order
// returns it; without it the order counts from now (the receive time).
func parseAfdianOrder(order map[string]any, now time.Time) (parsedAfdianOrder, bool) {
	status := readInt(order, "status")
	if status != afdianOrderStatusPaid {
		return parsedAfdianOrder{}, false
	}
	outTradeNo := readString(order, "out_trade_no", "outTradeNo")
	afdianUserID := readString(order, "user_id", "userId")
	if outTradeNo == "" || afdianUserID == "" {
		return parsedAfdianOrder{}, false
	}
	paidAt := parseUnixTime(order["create_time"])
	if paidAt == nil {
		paidAt = parseUnixTime(order["paid_at"])
	}
	if paidAt == nil {
		paidAt = &now
	}
	productType := afdianProductTypeRegular
	if readString(order, "product_type", "productType") != "" {
		productType = readInt(order, "product_type", "productType")
	}
	return parsedAfdianOrder{
		OutTradeNo:   trimLimit(outTradeNo, 128),
		AfdianUserID: trimLimit(afdianUserID, 128),
		UserName:     trimLimit(readString(order, "user_name", "userName"), 128),
		PlanID:       trimLimit(readString(order, "plan_id", "planId"), 128),
		PlanTitle:    trimLimit(readString(order, "plan_title", "planTitle"), 128),
		ProductType:  productType,
		Month:        readInt(order, "month", "months"),
		Status:       status,
		TotalAmount:  trimLimit(readString(order, "total_amount", "totalAmount"), 32),
		ShowAmount:   trimLimit(readString(order, "show_amount", "showAmount"), 32),
		Remark:       trimLimit(readString(order, "remark", "message", "memo"), 1000),
		PaidAt:       paidAt.UTC(),
	}, true
}

// ParseAfdianWebhookPayload extracts the order of a webhook body.
func ParseAfdianWebhookPayload(payload map[string]any, now time.Time) (parsedAfdianOrder, bool) {
	data := readMap(payload, "data")
	if data == nil {
		data = payload
	}
	order := readMap(data, "order")
	if order == nil {
		order = readMap(payload, "order")
	}
	if order == nil {
		return parsedAfdianOrder{}, false
	}
	return parseAfdianOrder(order, now)
}

// parseAfdianSponsorItem reads one query-sponsor entry. current_plan holding
// only name:"" means no current plan (developer guide); its expire_time is
// recorded only for a regular (non-sale) plan.
func parseAfdianSponsorItem(item map[string]any) (parsedAfdianSponsor, bool) {
	user := readMap(item, "user", "sponsor", "supporter")
	plan := readMap(item, "current_plan", "currentPlan", "plan")
	afdianUserID := readString(user, "user_id", "userId", "id")
	if afdianUserID == "" {
		afdianUserID = readString(item, "user_id", "userId", "id")
	}
	if afdianUserID == "" {
		return parsedAfdianSponsor{}, false
	}

	lastPaidAt := parseUnixTime(item["last_pay_time"])
	if lastPaidAt == nil {
		lastPaidAt = parseUnixTime(item["first_pay_time"])
	}
	if lastPaidAt == nil {
		lastPaidAt = parseUnixTime(item["create_time"])
	}
	var reported *time.Time
	if readString(plan, "name") != "" && readInt(plan, "product_type") != afdianProductTypeForSale {
		reported = parseUnixTime(plan["expire_time"])
	}
	totalAmount := readString(item, "all_sum_amount", "total_amount", "totalAmount")
	planRank := parseAmountRank(readString(plan, "price", "show_price", "showPrice"))
	if planRank == 0 {
		planRank = parseAmountRank(totalAmount)
	}

	return parsedAfdianSponsor{
		ID:                stableSponsorID(afdianUserID, ""),
		AfdianUserID:      trimLimit(afdianUserID, 128),
		Name:              trimLimit(readString(user, "name", "nickname", "user_name", "userName"), 128),
		Avatar:            trimLimit(readString(user, "avatar", "avatar_url", "avatarUrl"), 500),
		PlanID:            trimLimit(readString(plan, "plan_id", "planId"), 128),
		PlanName:          trimLimit(readString(plan, "name", "title", "plan_name", "planName"), 128),
		PlanRank:          planRank,
		LastPaidAt:        lastPaidAt,
		ReportedExpiresAt: reported,
		TotalAmount:       trimLimit(totalAmount, 32),
		Raw:               item,
	}, true
}

// storeAfdianOrder upserts an order and makes sure its sponsor row exists. It
// does not recompute; callers do once all orders of a pass are stored.
func storeAfdianOrder(ctx context.Context, db *postgresql.Client, order parsedAfdianOrder, now time.Time) (sponsorID string, created bool, err error) {
	sponsorID = stableSponsorID(order.AfdianUserID, order.OutTradeNo)
	if err := ensureAfdianSponsorRow(ctx, db, sponsorID, order.AfdianUserID, order.UserName, now); err != nil {
		return "", false, err
	}

	exists, err := db.SponsorAfdianOrder.Query().Where(orderSchema.IDEQ(order.OutTradeNo)).Exist(ctx)
	if err != nil {
		return "", false, err
	}
	if !exists {
		createErr := db.SponsorAfdianOrder.Create().
			SetID(order.OutTradeNo).
			SetSponsorID(sponsorID).
			SetAfdianUserID(order.AfdianUserID).
			SetPlanID(order.PlanID).
			SetPlanTitle(order.PlanTitle).
			SetProductType(order.ProductType).
			SetMonth(order.Month).
			SetStatus(order.Status).
			SetTotalAmount(order.TotalAmount).
			SetShowAmount(order.ShowAmount).
			SetRemark(order.Remark).
			SetPaidAt(order.PaidAt).
			Exec(ctx)
		if createErr == nil {
			return sponsorID, true, nil
		}
		if !postgresql.IsConstraintError(createErr) {
			return "", false, createErr
		}
		// A concurrent webhook/sync stored it first; refresh it below.
	}
	// Orders do not change after payment; the refresh only keeps the stored
	// copy equal to what Afdian returns. paid_at is never moved, so the
	// position of an order in the stacking order stays stable.
	err = db.SponsorAfdianOrder.UpdateOneID(order.OutTradeNo).
		SetPlanID(order.PlanID).
		SetPlanTitle(order.PlanTitle).
		SetProductType(order.ProductType).
		SetMonth(order.Month).
		SetStatus(order.Status).
		SetTotalAmount(order.TotalAmount).
		SetShowAmount(order.ShowAmount).
		SetRemark(order.Remark).
		Exec(ctx)
	return sponsorID, false, err
}

// ensureAfdianSponsorRow creates a minimal row for a first-seen Afdian user. A
// new row has no legacy expiry to split, so it starts out split.
func ensureAfdianSponsorRow(ctx context.Context, db *postgresql.Client, sponsorID, afdianUserID, name string, now time.Time) error {
	exists, err := db.Sponsor.Query().Where(sponsorSchema.IDEQ(sponsorID)).Exist(ctx)
	if err != nil || exists {
		return err
	}
	err = db.Sponsor.Create().
		SetID(sponsorID).
		SetNillableAfdianUserID(stringPointerOrNil(afdianUserID)).
		SetNillableName(stringPointerOrNil(name)).
		SetSource(sponsorSchema.SourceAfdian).
		SetIsActive(false).
		SetDurationSplitAt(now).
		SetSupportCount(0).
		Exec(ctx)
	if err != nil && postgresql.IsConstraintError(err) {
		return nil
	}
	return err
}

// RecordAfdianOrder stores a verified webhook order, refreshes the profile
// fields an order carries and recomputes the sponsor.
func RecordAfdianOrder(ctx context.Context, db *postgresql.Client, order parsedAfdianOrder, now time.Time) (*postgresql.Sponsor, error) {
	sponsorID, _, err := storeAfdianOrder(ctx, db, order, now)
	if err != nil {
		return nil, err
	}
	row, err := db.Sponsor.Get(ctx, sponsorID)
	if err != nil {
		return nil, err
	}
	update := db.Sponsor.UpdateOneID(sponsorID)
	changed := false
	if row.PaidAt == nil || order.PaidAt.After(*row.PaidAt) {
		update.SetPaidAt(order.PaidAt)
		changed = true
		// The newest order's remark and plan are the profile's; a pinned
		// profile keeps the admin's text.
		if !row.AfdianSyncDisabled {
			if order.Remark != "" {
				update.SetMessage(order.Remark)
			}
			if ClassifyAfdianOrder(order.facts()) == AfdianOrderDuration && order.PlanTitle != "" {
				update.SetPlanName(order.PlanTitle)
			}
		}
	}
	if changed {
		if err := update.Exec(ctx); err != nil {
			return nil, err
		}
	}
	return RecomputeSponsor(ctx, db, sponsorID, now)
}

// UpsertAfdianSponsorProfile applies one query-sponsor entry: the profile
// (unless an admin pinned it with afdian_sync_disabled), the cumulative
// amount, the last payment time and the plan expiry Afdian reports. Durations
// are not written here; RecomputeSponsor derives them.
func UpsertAfdianSponsorProfile(ctx context.Context, db *postgresql.Client, item parsedAfdianSponsor, now time.Time) error {
	row, err := db.Sponsor.Query().Where(sponsorSchema.IDEQ(item.ID)).Only(ctx)
	if err != nil && !postgresql.IsNotFound(err) {
		return err
	}
	if row == nil {
		create := db.Sponsor.Create().
			SetID(item.ID).
			SetSource(sponsorSchema.SourceAfdian).
			SetIsActive(false).
			SetDurationSplitAt(now).
			SetPlanRank(item.PlanRank).
			SetSupportCount(0).
			SetRaw(item.Raw).
			SetNillableAfdianUserID(stringPointerOrNil(item.AfdianUserID)).
			SetNillableName(stringPointerOrNil(item.Name)).
			SetNillableAvatar(stringPointerOrNil(item.Avatar)).
			SetNillablePlanID(stringPointerOrNil(item.PlanID)).
			SetNillablePlanName(stringPointerOrNil(item.PlanName)).
			SetNillablePaidAt(item.LastPaidAt).
			SetNillableTotalAmount(stringPointerOrNil(item.TotalAmount))
		if item.ReportedExpiresAt != nil {
			create.SetAfdianReportedExpiresAt(*item.ReportedExpiresAt).SetAfdianReportedAt(now)
		}
		createErr := create.Exec(ctx)
		if createErr == nil || !postgresql.IsConstraintError(createErr) {
			return createErr
		}
		// Created concurrently by a webhook; apply as an update.
		if row, err = db.Sponsor.Get(ctx, item.ID); err != nil {
			return err
		}
	}

	update := row.Update().
		SetRaw(item.Raw).
		SetNillableAfdianUserID(stringPointerOrNil(item.AfdianUserID)).
		SetNillableTotalAmount(stringPointerOrNil(item.TotalAmount))
	if item.LastPaidAt != nil && (row.PaidAt == nil || item.LastPaidAt.After(*row.PaidAt)) {
		update.SetPaidAt(*item.LastPaidAt)
	}
	// A lapsed plan comes back as current_plan {name: ""}: keep the last
	// reported expiry and the last plan name instead of clearing them. That
	// name used to be replaced by "一次性赞助" here, which is how every
	// lapsed duration sponsor ended up listed as one-time.
	if item.ReportedExpiresAt != nil {
		update.SetAfdianReportedExpiresAt(*item.ReportedExpiresAt).SetAfdianReportedAt(now)
	}
	if !row.AfdianSyncDisabled {
		update.SetNillableName(stringPointerOrNil(item.Name)).
			SetNillableAvatar(stringPointerOrNil(item.Avatar)).
			SetNillablePlanID(stringPointerOrNil(item.PlanID)).
			SetNillablePlanName(stringPointerOrNil(item.PlanName))
		if item.PlanRank > 0 {
			update.SetPlanRank(item.PlanRank)
		}
	}
	return update.Exec(ctx)
}

// SyncOptions selects how much of the order history a pass fetches.
type SyncOptions struct {
	// Full fetches every query-order page. Otherwise paging stops at the
	// first page that holds no new order (pages are newest first).
	Full bool
}

// SyncAfdianSponsors pulls orders (query-order) and supporters
// (query-sponsor), then recomputes every sponsor. After a full order pass the
// order history is complete, so rows still carrying the legacy single expiry
// are split (SplitLegacySponsorDurations).
func SyncAfdianSponsors(ctx context.Context, db *postgresql.Client, cfg AfdianConfig, now time.Time, opts SyncOptions) (AfdianSyncResult, error) {
	if !cfg.credentialsConfigured() {
		return AfdianSyncResult{}, ErrAfdianNotConfigured
	}
	client := afdianHTTPClient(cfg)
	result := AfdianSyncResult{Full: opts.Full}

	for page := 1; page <= afdianMaxPages; page++ {
		resp, err := callAfdianList(ctx, client, cfg, afdianQueryOrderPath, map[string]any{"page": page})
		if err != nil {
			return result, err
		}
		newOnPage := 0
		for _, raw := range resp.items {
			order, ok := parseAfdianOrder(raw, now)
			if !ok {
				continue
			}
			_, created, err := storeAfdianOrder(ctx, db, order, now)
			if err != nil {
				return result, err
			}
			result.Orders++
			if created {
				newOnPage++
				result.NewOrders++
			}
		}
		if resp.totalPage <= page || len(resp.items) == 0 || (!opts.Full && newOnPage == 0) {
			break
		}
	}

	for page := 1; page <= afdianMaxPages; page++ {
		resp, err := callAfdianList(ctx, client, cfg, afdianQuerySponsorPath, map[string]any{"page": page})
		if err != nil {
			return result, err
		}
		for _, raw := range resp.items {
			parsed, ok := parseAfdianSponsorItem(raw)
			if !ok {
				result.Skipped++
				continue
			}
			if err := UpsertAfdianSponsorProfile(ctx, db, parsed, now); err != nil {
				return result, err
			}
			result.Imported++
		}
		if resp.totalPage <= page || len(resp.items) == 0 {
			break
		}
	}

	if opts.Full {
		report, err := SplitLegacySponsorDurations(ctx, db, now, SplitOptions{OrdersComplete: true})
		if err != nil {
			return result, err
		}
		result.Split = report.Split
	}
	if err := RecomputeAllSponsors(ctx, db, now); err != nil {
		return result, err
	}
	return result, nil
}

// HasUnsplitSponsors reports whether any row still carries the legacy single
// expiry, i.e. the duration split has not run for it yet.
func HasUnsplitSponsors(ctx context.Context, db *postgresql.Client) (bool, error) {
	return db.Sponsor.Query().Where(sponsorSchema.DurationSplitAtIsNil()).Exist(ctx)
}
