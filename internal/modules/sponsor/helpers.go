package sponsor

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"

	sql "entgo.io/ent/dialect/sql"
)

const (
	defaultSponsorPlanName = "爱发电赞助"
	oneTimePlanName        = "一次性赞助"
	anonymousSponsorName   = "匿名赞助者"
)

type parsedAfdianSponsor struct {
	ID            string
	AfdianUserID  string
	OutTradeNo    string
	Name          string
	Avatar        string
	PlanID        string
	PlanName      string
	PlanRank      int
	PlanPayMonths *int
	Message       string
	Source        string
	IsActive      bool
	PaidAt        *time.Time
	PlanExpiresAt *time.Time
	SupportCount  int
	TotalAmount   string
	Raw           map[string]any
}

type AfdianSyncResult struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

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

func intPointerOrNil(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}

func normalizePlanName(planName string, payMonths *int, expiresAt *time.Time) string {
	planName = strings.TrimSpace(planName)
	if planName != "" {
		return planName
	}
	if payMonths == nil && expiresAt == nil {
		return oneTimePlanName
	}
	return defaultSponsorPlanName
}

func DefaultPlanName(payMonths *int, expiresAt *time.Time) string {
	return normalizePlanName("", payMonths, expiresAt)
}

func sponsorItemFromRow(row *postgresql.Sponsor) SponsorItem {
	planName := normalizePlanName(stringPtrValue(row.PlanName), row.PlanPayMonths, row.PlanExpiresAt)
	name := strings.TrimSpace(stringPtrValue(row.Name))
	if name == "" {
		name = anonymousSponsorName
	}
	return SponsorItem{
		ID:     row.ID,
		Name:   name,
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
		IsActive:           row.IsActive,
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

func BuildSponsorPageResponse(rows []*postgresql.Sponsor, now time.Time) SponsorPageResponse {
	items := make([]SponsorItem, 0, len(rows))
	summary := SponsorSummary{
		SupporterCount: len(rows),
		GeneratedAt:    now.UTC(),
	}
	for _, row := range rows {
		item := sponsorItemFromRow(row)
		if item.PlanExpiresAt != nil && item.PlanExpiresAt.Before(now) {
			item.IsActive = false
		}
		if item.IsActive {
			summary.ActiveCount++
		} else {
			summary.PastCount++
		}
		if item.PlanPayMonths == nil && item.PlanExpiresAt == nil {
			summary.OneTimeCount++
		}
		items = append(items, item)
	}
	sortSponsorItems(items)
	return SponsorPageResponse{Summary: summary, Supporters: items}
}

func sortSponsorItems(items []SponsorItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].PlanRank != items[j].PlanRank {
			return items[i].PlanRank > items[j].PlanRank
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
	sum := md5.Sum([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	return "sponsor_" + hex.EncodeToString(sum[:])
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
	case json.Number:
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

func calculateExpiresAt(paidAt *time.Time, months *int) *time.Time {
	if paidAt == nil || months == nil || *months <= 0 {
		return nil
	}
	expiresAt := paidAt.AddDate(0, *months, 0)
	return &expiresAt
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
		case json.Number:
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

func parseAfdianOrder(order map[string]any, now time.Time) (parsedAfdianSponsor, bool) {
	status := readInt(order, "status")
	if status != 0 && status != 2 {
		return parsedAfdianSponsor{}, false
	}

	afdianUserID := readString(order, "user_id", "userId")
	outTradeNo := readString(order, "out_trade_no", "outTradeNo")
	month := intPointerOrNil(readInt(order, "month", "months"))
	paidAt := parseUnixTime(order["paid_at"])
	if paidAt == nil {
		paidAt = parseUnixTime(order["create_time"])
	}
	if paidAt == nil {
		paidAt = parseUnixTime(order["created_at"])
	}
	if paidAt == nil {
		paidAt = &now
	}

	planID := readString(order, "plan_id", "planId")
	planName := readString(order, "plan_name", "planName", "title")
	totalAmount := readString(order, "total_amount", "totalAmount", "show_amount", "showAmount", "amount")
	expiresAt := calculateExpiresAt(paidAt, month)
	isActive := true
	if expiresAt != nil && expiresAt.Before(now) {
		isActive = false
	}
	if planID == "" {
		month = nil
		expiresAt = nil
		planName = normalizePlanName(planName, nil, nil)
	}

	return parsedAfdianSponsor{
		ID:            stableSponsorID(afdianUserID, outTradeNo),
		AfdianUserID:  afdianUserID,
		OutTradeNo:    outTradeNo,
		PlanID:        planID,
		PlanName:      normalizePlanName(planName, month, expiresAt),
		PlanRank:      parseAmountRank(totalAmount),
		PlanPayMonths: month,
		Message:       readString(order, "remark", "message", "memo"),
		Source:        "afdian",
		IsActive:      isActive,
		PaidAt:        paidAt,
		PlanExpiresAt: expiresAt,
		SupportCount:  1,
		TotalAmount:   totalAmount,
		Raw:           order,
	}, true
}

func parseAfdianSponsorItem(item map[string]any, now time.Time) (parsedAfdianSponsor, bool) {
	user := readMap(item, "user", "sponsor", "supporter")
	plan := readMap(item, "current_plan", "currentPlan", "plan")
	afdianUserID := readString(user, "user_id", "userId", "id")
	if afdianUserID == "" {
		afdianUserID = readString(item, "user_id", "userId", "id")
	}
	if afdianUserID == "" {
		return parsedAfdianSponsor{}, false
	}

	month := intPointerOrNil(readInt(plan, "pay_month", "payMonth", "month", "months"))
	paidAt := parseUnixTime(item["last_pay_time"])
	if paidAt == nil {
		paidAt = parseUnixTime(item["first_pay_time"])
	}
	if paidAt == nil {
		paidAt = parseUnixTime(item["create_time"])
	}
	expiresAt := parseUnixTime(plan["expire_time"])
	if expiresAt == nil {
		expiresAt = parseUnixTime(plan["expires_at"])
	}
	isActive := plan != nil && (expiresAt == nil || expiresAt.After(now))
	totalAmount := readString(item, "all_sum_amount", "total_amount", "totalAmount", "show_amount", "showAmount", "amount")
	planPrice := readString(plan, "price", "show_price", "showPrice")
	planRank := parseAmountRank(planPrice)
	if planRank == 0 {
		planRank = parseAmountRank(totalAmount)
	}
	planName := normalizePlanName(readString(plan, "name", "title", "plan_name", "planName"), month, expiresAt)

	return parsedAfdianSponsor{
		ID:            stableSponsorID(afdianUserID, ""),
		AfdianUserID:  afdianUserID,
		Name:          readString(user, "name", "nickname", "user_name", "userName"),
		Avatar:        readString(user, "avatar", "avatar_url", "avatarUrl"),
		PlanID:        readString(plan, "plan_id", "planId", "id"),
		PlanName:      planName,
		PlanRank:      planRank,
		PlanPayMonths: month,
		Message:       readString(item, "remark", "message", "memo"),
		Source:        "afdian",
		IsActive:      isActive,
		PaidAt:        paidAt,
		PlanExpiresAt: expiresAt,
		SupportCount:  readInt(item, "support_count", "supportCount"),
		TotalAmount:   totalAmount,
		Raw:           item,
	}, true
}

func UpsertParsedSponsor(ctx context.Context, db *postgresql.Client, item parsedAfdianSponsor, incrementCount bool) (*postgresql.Sponsor, error) {
	return upsertParsedSponsor(ctx, db, item, incrementCount, true)
}

func upsertParsedSponsor(ctx context.Context, db *postgresql.Client, item parsedAfdianSponsor, incrementCount bool, allowRetry bool) (*postgresql.Sponsor, error) {
	existing, err := db.Sponsor.Query().Where(sponsorSchema.IDEQ(item.ID)).Only(ctx)
	if err != nil && postgresql.IsNotFound(err) && item.OutTradeNo != "" {
		existing, err = db.Sponsor.Query().Where(sponsorSchema.OutTradeNoEQ(item.OutTradeNo)).Only(ctx)
	}
	if err != nil && !postgresql.IsNotFound(err) {
		return nil, err
	}

	if existing == nil || postgresql.IsNotFound(err) {
		create := db.Sponsor.Create().
			SetID(item.ID).
			SetSource(sponsorSchema.Source(item.Source)).
			SetIsActive(item.IsActive).
			SetAfdianSyncDisabled(false).
			SetPlanRank(item.PlanRank).
			SetSupportCount(maxInt(item.SupportCount, 1)).
			SetRaw(item.Raw)
		setSponsorCreateFields(create, item)
		saved, createErr := create.Save(ctx)
		if createErr != nil && allowRetry && postgresql.IsConstraintError(createErr) {
			// A concurrent sync/webhook created the same record between our lookup
			// and insert; re-resolve and fall through to the update path.
			return upsertParsedSponsor(ctx, db, item, incrementCount, false)
		}
		return saved, createErr
	}

	// An admin can pin a sponsor with afdian_sync_disabled so neither the periodic
	// sync nor webhooks overwrite it. Leave the record completely untouched.
	if existing.AfdianSyncDisabled {
		return existing, nil
	}

	update := existing.Update().
		SetIsActive(item.IsActive).
		SetPlanRank(item.PlanRank).
		SetRaw(item.Raw)
	setSponsorUpdateFields(update, item)
	if incrementCount && item.OutTradeNo != "" && item.OutTradeNo != stringPtrValue(existing.OutTradeNo) {
		update.SetSupportCount(existing.SupportCount + 1)
	} else if item.SupportCount > 0 {
		update.SetSupportCount(item.SupportCount)
	}
	return update.Save(ctx)
}

func setSponsorCreateFields(create *postgresql.SponsorCreate, item parsedAfdianSponsor) {
	create.SetNillableAfdianUserID(stringPointerOrNil(trimLimit(item.AfdianUserID, 128)))
	create.SetNillableOutTradeNo(stringPointerOrNil(trimLimit(item.OutTradeNo, 128)))
	create.SetNillableName(stringPointerOrNil(trimLimit(item.Name, 128)))
	create.SetNillableAvatar(stringPointerOrNil(trimLimit(item.Avatar, 500)))
	create.SetNillablePlanID(stringPointerOrNil(trimLimit(item.PlanID, 128)))
	create.SetNillablePlanName(stringPointerOrNil(trimLimit(item.PlanName, 128)))
	create.SetNillablePlanPayMonths(item.PlanPayMonths)
	create.SetNillableMessage(stringPointerOrNil(trimLimit(item.Message, 1000)))
	create.SetNillablePaidAt(item.PaidAt)
	create.SetNillablePlanExpiresAt(item.PlanExpiresAt)
	create.SetNillableTotalAmount(stringPointerOrNil(trimLimit(item.TotalAmount, 32)))
}

func setSponsorUpdateFields(update *postgresql.SponsorUpdateOne, item parsedAfdianSponsor) {
	update.SetNillableAfdianUserID(stringPointerOrNil(trimLimit(item.AfdianUserID, 128)))
	update.SetNillableOutTradeNo(stringPointerOrNil(trimLimit(item.OutTradeNo, 128)))
	update.SetNillablePaidAt(item.PaidAt)
	update.SetNillablePlanExpiresAt(item.PlanExpiresAt)
	update.SetNillableTotalAmount(stringPointerOrNil(trimLimit(item.TotalAmount, 32)))
	update.SetNillablePlanPayMonths(item.PlanPayMonths)
	update.SetSource(sponsorSchema.Source(item.Source))
	update.SetNillableName(stringPointerOrNil(trimLimit(item.Name, 128)))
	update.SetNillableAvatar(stringPointerOrNil(trimLimit(item.Avatar, 500)))
	update.SetNillablePlanID(stringPointerOrNil(trimLimit(item.PlanID, 128)))
	update.SetNillablePlanName(stringPointerOrNil(trimLimit(item.PlanName, 128)))
	update.SetNillableMessage(stringPointerOrNil(trimLimit(item.Message, 1000)))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func ParseAfdianWebhookPayload(payload map[string]any, now time.Time) (parsedAfdianSponsor, bool) {
	data := readMap(payload, "data")
	if data == nil {
		data = payload
	}
	order := readMap(data, "order")
	if order == nil {
		order = readMap(payload, "order")
	}
	if order == nil {
		return parsedAfdianSponsor{}, false
	}
	return parseAfdianOrder(order, now)
}

// ErrAfdianNotConfigured signals that the Afdian API credentials required to
// reach the open API (e.g. to verify a webhook order) are missing.
var ErrAfdianNotConfigured = errors.New("afdian user_id or api token is not configured")

func afdianHTTPClient(cfg config.AfdianConfig) *http.Client {
	return &http.Client{Timeout: time.Duration(maxInt(cfg.RequestTimeoutSecond, 10)) * time.Second}
}

func afdianBaseURL(cfg config.AfdianConfig) string {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if baseURL == "" {
		baseURL = "https://afdian.com/api/open"
	}
	return baseURL
}

// VerifyAfdianOrder re-queries the Afdian open API for the given out_trade_no and
// returns the authoritative, parsed order. Webhook payloads carry no signature, so
// callers must use this to confirm an order is real before trusting it. Returns
// ErrAfdianNotConfigured when API credentials are missing, or found=false when the
// order does not exist on Afdian's side (likely forged).
func VerifyAfdianOrder(ctx context.Context, cfg config.AfdianConfig, outTradeNo string, now time.Time) (parsedAfdianSponsor, bool, error) {
	outTradeNo = strings.TrimSpace(outTradeNo)
	if outTradeNo == "" {
		return parsedAfdianSponsor{}, false, nil
	}
	if strings.TrimSpace(cfg.UserID) == "" || strings.TrimSpace(cfg.APIToken) == "" {
		return parsedAfdianSponsor{}, false, ErrAfdianNotConfigured
	}

	order, found, err := queryAfdianOrderByTradeNo(ctx, afdianHTTPClient(cfg), afdianBaseURL(cfg), cfg, outTradeNo)
	if err != nil || !found {
		return parsedAfdianSponsor{}, false, err
	}
	parsed, ok := parseAfdianOrder(order, now)
	if !ok {
		return parsedAfdianSponsor{}, false, nil
	}
	return parsed, true, nil
}

func queryAfdianOrderByTradeNo(ctx context.Context, client *http.Client, baseURL string, cfg config.AfdianConfig, outTradeNo string) (map[string]any, bool, error) {
	paramsBytes, err := json.Marshal(map[string]any{"out_trade_no": outTradeNo})
	if err != nil {
		return nil, false, err
	}
	params := string(paramsBytes)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	body := map[string]any{
		"user_id": cfg.UserID,
		"params":  params,
		"ts":      ts,
		"sign":    afdianSign(cfg.APIToken, params, ts, cfg.UserID),
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/query-order", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, fmt.Errorf("afdian api returned status %d", resp.StatusCode)
	}

	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(respBody))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, false, err
	}
	if ec := readInt(payload, "ec"); ec != 0 && ec != 200 {
		return nil, false, fmt.Errorf("afdian api returned ec %d", ec)
	}
	data := readMap(payload, "data")
	if data == nil {
		return nil, false, nil
	}
	listRaw, ok := data["list"].([]any)
	if !ok {
		return nil, false, nil
	}
	for _, raw := range listRaw {
		order, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if readString(order, "out_trade_no", "outTradeNo") == outTradeNo {
			return order, true, nil
		}
	}
	return nil, false, nil
}

func SyncAfdianSponsors(ctx context.Context, db *postgresql.Client, cfg config.AfdianConfig, now time.Time) (AfdianSyncResult, error) {
	if strings.TrimSpace(cfg.UserID) == "" || strings.TrimSpace(cfg.APIToken) == "" {
		return AfdianSyncResult{}, ErrAfdianNotConfigured
	}
	client := afdianHTTPClient(cfg)
	baseURL := afdianBaseURL(cfg)

	result := AfdianSyncResult{}
	for page := 1; page <= 100; page++ {
		items, totalPage, err := queryAfdianSponsorPage(ctx, client, baseURL, cfg, page)
		if err != nil {
			return result, err
		}
		for _, raw := range items {
			parsed, ok := parseAfdianSponsorItem(raw, now)
			if !ok {
				result.Skipped++
				continue
			}
			if _, err := UpsertParsedSponsor(ctx, db, parsed, false); err != nil {
				return result, err
			}
			result.Imported++
		}
		if totalPage <= page || len(items) == 0 {
			break
		}
	}
	return result, nil
}

func queryAfdianSponsorPage(ctx context.Context, client *http.Client, baseURL string, cfg config.AfdianConfig, page int) ([]map[string]any, int, error) {
	paramsBytes, err := json.Marshal(map[string]any{
		"page": page,
	})
	if err != nil {
		return nil, 0, err
	}
	params := string(paramsBytes)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	body := map[string]any{
		"user_id": cfg.UserID,
		"params":  params,
		"ts":      ts,
		"sign":    afdianSign(cfg.APIToken, params, ts, cfg.UserID),
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/query-sponsor", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("afdian api returned status %d", resp.StatusCode)
	}

	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(respBody))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, 0, err
	}
	if ec := readInt(payload, "ec"); ec != 0 && ec != 200 {
		return nil, 0, fmt.Errorf("afdian api returned ec %d", ec)
	}
	data := readMap(payload, "data")
	if data == nil {
		return nil, 0, nil
	}
	totalPage := readInt(data, "total_page", "totalPage")
	if totalPage <= 0 {
		totalPage = page
	}
	listRaw, ok := data["list"].([]any)
	if !ok {
		return nil, totalPage, nil
	}
	items := make([]map[string]any, 0, len(listRaw))
	for _, raw := range listRaw {
		if item, ok := raw.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items, totalPage, nil
}

func afdianSign(token string, params string, ts string, userID string) string {
	sum := md5.Sum([]byte(token + "params" + params + "ts" + ts + "user_id" + userID))
	return hex.EncodeToString(sum[:])
}
