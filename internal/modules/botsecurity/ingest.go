package botsecurity

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/botsecurityalert"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

// IngestPath is served on the backend port only. Oathkeeper has no rule for
// /internal/ (architecture test TestInternalAPINotRoutedByOathkeeper), so it
// is reachable only on the private network / compose network.
const IngestPath = "/internal/bot-security/alerts"

const (
	// maxIngestBodyBytes bounds one alert; Cloud's are a few hundred bytes.
	maxIngestBodyBytes = 16 << 10

	maxKindLength          = 64
	maxBotIDLength         = 64
	maxSourceIPLength      = 64
	maxNodeLength          = 64
	maxBuildIDBytes        = 128
	maxClientVersionBytes  = 128
	maxReasonBytes         = 1024
	maxAlertFutureSkew     = 10 * time.Minute
	ingestRateLimitWindow  = time.Minute
	ingestRateLimitPerIP   = 600
	ingestRateLimitKeyTTL  = 2 * ingestRateLimitWindow
	subjectWithoutBotOrIP  = "global"
	ingestStoreTimeout     = 5 * time.Second
	ingestRateLimitTimeout = time.Second
)

// alertPayload is the JSON object Haruki Cloud posts for one alert
// (internal/core/secevent in Haruki-Cloud). Unknown members are ignored so
// Cloud can add fields without breaking ingestion.
type alertPayload struct {
	Kind          string `json:"kind"`
	BotID         string `json:"bot_id"`
	BuildID       string `json:"build_id"`
	ClientVersion string `json:"client_version"`
	SourceIP      string `json:"source_ip"`
	Reason        string `json:"reason"`
	Enforced      bool   `json:"enforced"`
	Count         int64  `json:"count"`
	Threshold     int64  `json:"threshold"`
	WindowSeconds int64  `json:"window_seconds"`
	Node          string `json:"node"`
	Time          string `json:"time"`
}

// ingestResponse answers an accepted alert; Duplicate is true for a replay
// of an alert already stored (nothing was inserted).
type ingestResponse struct {
	Accepted  bool `json:"accepted"`
	ID        int  `json:"id"`
	Duplicate bool `json:"duplicate"`
}

// alertRecord is a validated alert ready to store.
type alertRecord struct {
	Kind          string
	BotID         string
	Subject       string
	SourceIP      string
	BuildID       string
	ClientVersion string
	Reason        string
	Enforced      bool
	Count         int64
	Threshold     int64
	WindowSeconds int64
	Node          string
	AlertTime     time.Time
}

// errInvalidAlert is returned for a body that is not a valid alert. Its
// message names the offending field but never repeats a value.
type errInvalidAlert struct{ detail string }

func (e errInvalidAlert) Error() string { return e.detail }

func invalidAlert(format string, args ...any) error {
	return errInvalidAlert{detail: fmt.Sprintf(format, args...)}
}

// validKind reports whether kind is 1–64 characters of [a-z_].
func validKind(kind string) bool {
	if kind == "" || len(kind) > maxKindLength {
		return false
	}
	for i := 0; i < len(kind); i++ {
		if c := kind[i]; c != '_' && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

// validIdentifier accepts an optional identifier (bot id, address, node
// name): at most maxLen bytes, no whitespace or control characters.
func validIdentifier(value string, maxLen int) bool {
	if len(value) > maxLen {
		return false
	}
	return !strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

// sanitizeText bounds a free-text field that originates from the bot client
// (build id, client version) or from Cloud's reason string. Oversized text is
// truncated rather than rejected, so a client cannot suppress the alert about
// itself by sending a long build id. Control characters (NUL included, which
// PostgreSQL text cannot store) become U+FFFD.
func sanitizeText(value string, maxBytes int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return utf8.RuneError
		}
		return r
	}, strings.TrimSpace(value))
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// parseAlertPayload decodes and validates one alert body.
func parseAlertPayload(body []byte, now time.Time) (alertRecord, error) {
	var payload alertPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return alertRecord{}, invalidAlert("body must be one JSON object with the alert fields")
	}
	if !validKind(payload.Kind) {
		return alertRecord{}, invalidAlert("kind must be 1-%d characters of [a-z_]", maxKindLength)
	}
	if !validIdentifier(payload.BotID, maxBotIDLength) {
		return alertRecord{}, invalidAlert("bot_id must be at most %d characters without whitespace", maxBotIDLength)
	}
	if !validIdentifier(payload.SourceIP, maxSourceIPLength) {
		return alertRecord{}, invalidAlert("source_ip must be at most %d characters without whitespace", maxSourceIPLength)
	}
	if !validIdentifier(payload.Node, maxNodeLength) {
		return alertRecord{}, invalidAlert("node must be at most %d characters without whitespace", maxNodeLength)
	}
	if payload.Count < 0 || payload.Threshold < 0 || payload.WindowSeconds < 0 {
		return alertRecord{}, invalidAlert("count, threshold and window_seconds must not be negative")
	}
	alertTime, err := time.Parse(time.RFC3339, payload.Time)
	if err != nil {
		return alertRecord{}, invalidAlert("time must be an RFC 3339 timestamp")
	}
	alertTime = alertTime.UTC()
	if alertTime.Year() < 2000 || alertTime.After(now.Add(maxAlertFutureSkew)) {
		return alertRecord{}, invalidAlert("time is out of range")
	}

	subject := payload.BotID
	if subject == "" {
		subject = payload.SourceIP
	}
	if subject == "" {
		subject = subjectWithoutBotOrIP
	}
	return alertRecord{
		Kind:          payload.Kind,
		BotID:         payload.BotID,
		Subject:       subject,
		SourceIP:      payload.SourceIP,
		BuildID:       sanitizeText(payload.BuildID, maxBuildIDBytes),
		ClientVersion: sanitizeText(payload.ClientVersion, maxClientVersionBytes),
		Reason:        sanitizeText(payload.Reason, maxReasonBytes),
		Enforced:      payload.Enforced,
		Count:         payload.Count,
		Threshold:     payload.Threshold,
		WindowSeconds: payload.WindowSeconds,
		Node:          payload.Node,
		AlertTime:     alertTime,
	}, nil
}

// storeAlert inserts the alert unless one with the same (kind, subject,
// node, alert time) exists. It returns the stored row's id and whether the
// alert was a duplicate. The unique index decides, so concurrent replays
// insert exactly one row.
func storeAlert(ctx context.Context, db *postgresql.Client, record alertRecord, receivedAt time.Time) (int, bool, error) {
	builder := db.BotSecurityAlert.Create().
		SetKind(record.Kind).
		SetSubject(record.Subject).
		SetSourceIP(record.SourceIP).
		SetBuildID(record.BuildID).
		SetClientVersion(record.ClientVersion).
		SetReason(record.Reason).
		SetEnforced(record.Enforced).
		SetCount(record.Count).
		SetThreshold(record.Threshold).
		SetWindowSeconds(record.WindowSeconds).
		SetNode(record.Node).
		SetAlertTime(record.AlertTime).
		SetReceivedAt(receivedAt.UTC()).
		SetStatus(botsecurityalert.StatusOpen)
	if record.BotID != "" {
		builder.SetBotID(record.BotID)
	}
	row, err := builder.Save(ctx)
	if err == nil {
		return row.ID, false, nil
	}
	if !postgresql.IsConstraintError(err) {
		return 0, false, err
	}
	existingID, queryErr := db.BotSecurityAlert.Query().
		Where(
			botsecurityalert.KindEQ(record.Kind),
			botsecurityalert.SubjectEQ(record.Subject),
			botsecurityalert.NodeEQ(record.Node),
			botsecurityalert.AlertTimeEQ(record.AlertTime),
		).
		OnlyID(ctx)
	if queryErr != nil {
		return 0, false, errors.Join(err, queryErr)
	}
	return existingID, true, nil
}

// IngestRouteOptions configures RegisterIngestRoutes.
type IngestRouteOptions struct {
	Config IngestConfig
	// Logger defaults to the global logger named BotSecurity.
	Logger *harukiLogger.Logger
	// Now defaults to time.Now.
	Now func() time.Time
}

// RegisterIngestRoutes registers POST /internal/bot-security/alerts on the
// main app when bot_security.ingest_token_sha256 is configured; otherwise
// nothing is registered.
func RegisterIngestRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, options IngestRouteOptions) {
	if apiHelper == nil || apiHelper.Router == nil || !options.Config.Enabled() {
		return
	}
	logger := options.Logger
	if logger == nil {
		logger = harukiLogger.NewLoggerFromGlobal("BotSecurity")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	apiHelper.Router.Post(IngestPath, handleIngestAlert(apiHelper, options.Config, now, logger))
}

func handleIngestAlert(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, cfg IngestConfig, now func() time.Time, logger *harukiLogger.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")

		if reason := cfg.authorize(c.Request().Header.PeekAll(fiber.HeaderAuthorization)); reason != "" {
			logger.Warnf("bot_security_ingest event=unauthorized reason=%s ip=%s", reason, strconv.Quote(c.IP()))
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}

		current := now().UTC()
		if retryAfter, limited := ingestRateLimited(c, apiHelper, current, logger); limited {
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(retryAfter))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate_limited"})
		}

		body := c.Body()
		if len(body) > maxIngestBodyBytes {
			return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "payload_too_large"})
		}
		record, err := parseAlertPayload(body, current)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_request", "detail": err.Error()})
		}

		if apiHelper.DBManager == nil || apiHelper.DBManager.DB == nil {
			logger.Errorf("bot_security_ingest event=store_unavailable")
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "temporarily_unavailable"})
		}
		ctx, cancel := context.WithTimeout(c.Context(), ingestStoreTimeout)
		defer cancel()
		id, duplicate, err := storeAlert(ctx, apiHelper.DBManager.DB, record, current)
		if err != nil {
			logger.Errorf("bot_security_ingest event=store_failed kind=%s error=%s", record.Kind, strconv.Quote(err.Error()))
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "temporarily_unavailable"})
		}
		logger.Infof("bot_security_ingest event=accepted id=%d kind=%s subject=%s node=%s duplicate=%t",
			id, record.Kind, strconv.Quote(record.Subject), strconv.Quote(record.Node), duplicate)
		return c.Status(fiber.StatusAccepted).JSON(ingestResponse{Accepted: true, ID: id, Duplicate: duplicate})
	}
}

// ingestRateLimited counts the caller's alerts in a fixed one-minute window
// in Redis. It fails open (and logs) when Redis is missing or erroring: an
// authenticated caller losing alerts is worse than an unbounded window.
func ingestRateLimited(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, now time.Time, logger *harukiLogger.Logger) (int, bool) {
	if apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil || apiHelper.DBManager.Redis.Redis == nil {
		return 0, false
	}
	window := int64(ingestRateLimitWindow / time.Second)
	slot := now.Unix() / window
	ctx, cancel := context.WithTimeout(c.Context(), ingestRateLimitTimeout)
	defer cancel()
	count, err := apiHelper.DBManager.Redis.IncrementWithTTL(ctx, harukiRedis.BuildBotSecurityIngestRateLimitKey(slot, c.IP()), ingestRateLimitKeyTTL)
	if err != nil {
		logger.Warnf("bot_security_ingest event=rate_limit_unavailable error_type=%T", err)
		return 0, false
	}
	if count <= ingestRateLimitPerIP {
		return 0, false
	}
	retryAfter := int(window - now.Unix()%window)
	if retryAfter < 1 {
		retryAfter = 1
	}
	return retryAfter, true
}
