package sponsor

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"
)

// The Afdian open API is used read-only: query-order (50 orders per page,
// newest first, or the orders named by out_trade_no) and query-sponsor (20
// sponsors per page).
const (
	afdianQueryOrderPath   = "/query-order"
	afdianQuerySponsorPath = "/query-sponsor"
	afdianMaxPages         = 1000
	afdianMaxResponseBytes = 8 * 1024 * 1024
)

// ErrAfdianNotConfigured signals that the Afdian API credentials required to
// reach the open API (e.g. to verify a webhook order) are missing.
var ErrAfdianNotConfigured = errors.New("afdian user_id or api token is not configured")

func afdianHTTPClient(cfg AfdianConfig) *http.Client {
	return &http.Client{Timeout: cfg.timeout()}
}

func afdianSign(token string, params string, ts string, userID string) string {
	// Afdian's published API protocol requires this exact MD5 signature format.
	// It authenticates a compatibility request and is not used for password
	// storage, content integrity, or any protocol we control.
	sum := md5.Sum([]byte(token + "params" + params + "ts" + ts + "user_id" + userID)) // NOSONAR
	return hex.EncodeToString(sum[:])
}

// afdianPage is one page of a list endpoint.
type afdianPage struct {
	items     []map[string]any
	totalPage int
}

// callAfdianList posts params to a list endpoint and returns data.list.
func callAfdianList(ctx context.Context, client *http.Client, cfg AfdianConfig, path string, params map[string]any) (afdianPage, error) {
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return afdianPage{}, err
	}
	encodedParams := string(paramsBytes)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	bodyBytes, err := json.Marshal(map[string]any{
		"user_id": cfg.userID,
		"params":  encodedParams,
		"ts":      ts,
		"sign":    afdianSign(cfg.apiToken, encodedParams, ts, cfg.userID),
	})
	if err != nil {
		return afdianPage{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.baseURL()+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return afdianPage{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return afdianPage{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, afdianMaxResponseBytes))
	if err != nil {
		return afdianPage{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return afdianPage{}, fmt.Errorf("afdian api returned status %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := json.UnmarshalRead(bytes.NewReader(respBody), &payload, jsonvalue.Numbers); err != nil {
		return afdianPage{}, err
	}
	if ec := readInt(payload, "ec"); ec != 0 && ec != 200 {
		return afdianPage{}, fmt.Errorf("afdian api returned ec %d", ec)
	}
	data := readMap(payload, "data")
	if data == nil {
		return afdianPage{}, nil
	}
	page := afdianPage{totalPage: readInt(data, "total_page", "totalPage")}
	listRaw, _ := data["list"].([]any)
	for _, raw := range listRaw {
		if item, ok := raw.(map[string]any); ok {
			page.items = append(page.items, item)
		}
	}
	return page, nil
}

// VerifyAfdianOrder re-queries the Afdian open API for the given out_trade_no and
// returns the authoritative, parsed order. Webhook payloads carry no signature, so
// callers must use this to confirm an order is real before trusting it. Returns
// ErrAfdianNotConfigured when API credentials are missing, or found=false when the
// order does not exist on Afdian's side (likely forged) or is not paid.
func VerifyAfdianOrder(ctx context.Context, cfg AfdianConfig, outTradeNo string, now time.Time) (parsedAfdianOrder, bool, error) {
	outTradeNo = strings.TrimSpace(outTradeNo)
	if outTradeNo == "" {
		return parsedAfdianOrder{}, false, nil
	}
	if !cfg.credentialsConfigured() {
		return parsedAfdianOrder{}, false, ErrAfdianNotConfigured
	}

	page, err := callAfdianList(ctx, afdianHTTPClient(cfg), cfg, afdianQueryOrderPath, map[string]any{"out_trade_no": outTradeNo})
	if err != nil {
		return parsedAfdianOrder{}, false, err
	}
	for _, raw := range page.items {
		if readString(raw, "out_trade_no", "outTradeNo") != outTradeNo {
			continue
		}
		parsed, ok := parseAfdianOrder(raw, now)
		return parsed, ok, nil
	}
	return parsedAfdianOrder{}, false, nil
}
