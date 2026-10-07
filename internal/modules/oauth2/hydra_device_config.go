package oauth2

import (
	"context"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"golang.org/x/sync/singleflight"
)

// deviceFlowGateCacheTTL bounds how long a successful runtime-switch read is
// reused. The runtime config service does a Redis GET under a service-wide lock
// on every read, which the private API and OAuth game-data reads share, so an
// anonymous device/auth or polling flood must not call it per request.
const deviceFlowGateCacheTTL = time.Second

// DeviceFlowRuntimeGate reads the runtime device-flow switch. It answers
// (true, nil) when the switch is on, (false, nil) when it is off or missing,
// and an error when the switch cannot be read, which handlers map to 503.
type DeviceFlowRuntimeGate func(ctx context.Context) (bool, error)

// DeviceFlowTimings are the device-flow durations.
type DeviceFlowTimings struct {
	MinPollInterval       time.Duration
	ClaimTTL              time.Duration
	ApproveLease          time.Duration
	ApprovalTimeout       time.Duration
	MinRemainingToApprove time.Duration
	MaxApproveAttempts    int
	// RecordGrace keeps a flow record past its user code's expiry so late
	// polls can still be told expired_token.
	RecordGrace    time.Duration
	ReaperInterval time.Duration
	ReaperGrace    time.Duration
}

// DeviceFlowLimits are the device-flow rate limits; windows are 10 minutes
// except the per-day limits.
type DeviceFlowLimits struct {
	AuthAttemptUnknownClientWarnPer10m int
	AuthAttemptClientWarnMultiplier    int
	AuthIssuedGlobalPer10m             int
	AuthIssuedPublicPer10m             int
	AuthIssuedConfidentialPer10m       int
	AuthIssuedClientDefaultPer10m      int
	LookupUserPer10m                   int
	LookupFailUserPer10m               int
	LookupFailUserPerDay               int
	LookupFailGlobalPer10m             int
	DecisionUserPerDay                 int
	MaxSlowDown                        int
	MaxIntervalSeconds                 int
}

// DeviceFlowConfigOptions is copied by NewDeviceFlowConfig. Only the
// composition root reads startup configuration; it resolves nothing here
// except the documented defaults of empty URLs.
type DeviceFlowConfigOptions struct {
	Enabled bool
	// ClientAllowlist empty allows every client holding the device grant.
	ClientAllowlist []string
	// VerificationURL empty means FrontendURL + "/device".
	VerificationURL string
	FrontendURL     string
	HydraIssuerURL  string
	// AllowedOrigins empty means [origin(FrontendURL)].
	AllowedOrigins  []string
	UserCodeCharset string
	UserCodeLength  int
	UserCodeTTL     time.Duration
	Timings         DeviceFlowTimings
	Limits          DeviceFlowLimits
	// Logger receives the oauth2_device structured log lines; nil uses the
	// default logger.
	Logger *harukiLogger.Logger
}

// DeviceFlowConfig is the immutable device-flow configuration. The zero value
// is usable: Enabled is false and no method panics. Without a runtime gate the
// flow is active exactly when it is enabled at startup.
type DeviceFlowConfig struct {
	enabled         bool
	clientAllowlist map[string]struct{}
	verificationURL string
	frontendOrigin  string
	issuerOrigin    string
	issuerPath      string
	allowedOrigins  map[string]struct{}
	userCodeCharset string
	userCodeLength  int
	userCodeTTL     time.Duration
	timings         DeviceFlowTimings
	limits          DeviceFlowLimits
	logger          *harukiLogger.Logger
	gate            *deviceFlowGate
}

func NewDeviceFlowConfig(options DeviceFlowConfigOptions) DeviceFlowConfig {
	frontendOrigin := urlOrigin(options.FrontendURL)
	verificationURL := strings.TrimSpace(options.VerificationURL)
	if verificationURL == "" && strings.TrimSpace(options.FrontendURL) != "" {
		verificationURL = strings.TrimRight(strings.TrimSpace(options.FrontendURL), "/") + "/device"
	}
	issuerOrigin, issuerPath := "", ""
	if issuer, err := url.Parse(strings.TrimSpace(options.HydraIssuerURL)); err == nil && issuer.Scheme != "" && issuer.Host != "" {
		issuerOrigin = strings.ToLower(issuer.Scheme) + "://" + strings.ToLower(issuer.Host)
		issuerPath = strings.TrimRight(issuer.Path, "/")
	}
	allowedOrigins := make(map[string]struct{})
	for _, origin := range options.AllowedOrigins {
		if normalized := urlOrigin(origin); normalized != "" {
			allowedOrigins[normalized] = struct{}{}
		}
	}
	if len(allowedOrigins) == 0 && frontendOrigin != "" {
		allowedOrigins[frontendOrigin] = struct{}{}
	}
	clientAllowlist := make(map[string]struct{})
	for _, clientID := range options.ClientAllowlist {
		if trimmed := strings.TrimSpace(clientID); trimmed != "" {
			clientAllowlist[trimmed] = struct{}{}
		}
	}
	return DeviceFlowConfig{
		enabled:         options.Enabled,
		clientAllowlist: clientAllowlist,
		verificationURL: verificationURL,
		frontendOrigin:  frontendOrigin,
		issuerOrigin:    issuerOrigin,
		issuerPath:      issuerPath,
		allowedOrigins:  allowedOrigins,
		userCodeCharset: options.UserCodeCharset,
		userCodeLength:  options.UserCodeLength,
		userCodeTTL:     options.UserCodeTTL,
		timings:         options.Timings,
		limits:          options.Limits,
		logger:          options.Logger,
	}
}

// WithRuntimeGate returns a copy whose Active also requires the runtime switch.
func (c DeviceFlowConfig) WithRuntimeGate(gate DeviceFlowRuntimeGate) DeviceFlowConfig {
	if gate == nil {
		c.gate = nil
		return c
	}
	c.gate = &deviceFlowGate{read: gate, now: time.Now}
	return c
}

// Enabled is the startup switch.
func (c DeviceFlowConfig) Enabled() bool { return c.enabled }

// Active is the effective switch: startup AND runtime. An error means the
// runtime switch could not be read and no successful read is younger than 1 s.
func (c DeviceFlowConfig) Active(ctx context.Context) (bool, error) {
	if !c.enabled {
		return false, nil
	}
	if c.gate == nil {
		return true, nil
	}
	return c.gate.active(ctx)
}

// ClientAllowed applies the optional client allowlist; empty allows all.
func (c DeviceFlowConfig) ClientAllowed(clientID string) bool {
	if len(c.clientAllowlist) == 0 {
		return true
	}
	_, ok := c.clientAllowlist[strings.TrimSpace(clientID)]
	return ok
}

func (c DeviceFlowConfig) VerificationURL() string { return c.verificationURL }

// VerificationURLComplete is the verification URL carrying the formatted user
// code, as RFC 8628 verification_uri_complete.
func (c DeviceFlowConfig) VerificationURLComplete(formattedCode string) string {
	return c.verificationURL + "?user_code=" + url.QueryEscape(formattedCode)
}

func (c DeviceFlowConfig) HydraIssuerOrigin() string { return c.issuerOrigin }
func (c DeviceFlowConfig) HydraIssuerPath() string   { return c.issuerPath }
func (c DeviceFlowConfig) FrontendOrigin() string    { return c.frontendOrigin }

// OriginAllowed reports whether a browser Origin header is in allowed_origins.
func (c DeviceFlowConfig) OriginAllowed(origin string) bool {
	normalized := urlOrigin(origin)
	if normalized == "" {
		return false
	}
	_, ok := c.allowedOrigins[normalized]
	return ok
}

func (c DeviceFlowConfig) UserCodeCharset() string    { return c.userCodeCharset }
func (c DeviceFlowConfig) UserCodeLength() int        { return c.userCodeLength }
func (c DeviceFlowConfig) UserCodeTTL() time.Duration { return c.userCodeTTL }
func (c DeviceFlowConfig) Timings() DeviceFlowTimings { return c.timings }
func (c DeviceFlowConfig) Limits() DeviceFlowLimits   { return c.limits }
func (c DeviceFlowConfig) log() *harukiLogger.Logger  { return deviceFlowLogger(c.logger) }
func deviceFlowLogger(l *harukiLogger.Logger) *harukiLogger.Logger {
	if l != nil {
		return l
	}
	return harukiLogger.DefaultLogger
}

// minPollInterval is the floor of the polling interval handed to devices.
func (c DeviceFlowConfig) minPollInterval() time.Duration {
	if c.timings.MinPollInterval > 0 {
		return c.timings.MinPollInterval
	}
	return 5 * time.Second
}

func (c DeviceFlowConfig) recordGrace() time.Duration {
	if c.timings.RecordGrace > 0 {
		return c.timings.RecordGrace
	}
	return 30 * time.Minute
}

func (c DeviceFlowConfig) maxIntervalSeconds() int {
	if c.limits.MaxIntervalSeconds > 0 {
		return c.limits.MaxIntervalSeconds
	}
	return 60
}

func (c DeviceFlowConfig) maxSlowDown() int {
	if c.limits.MaxSlowDown > 0 {
		return c.limits.MaxSlowDown
	}
	return 30
}

func (c DeviceFlowConfig) claimTTL() time.Duration {
	if c.timings.ClaimTTL > 0 {
		return c.timings.ClaimTTL
	}
	return 300 * time.Second
}

// approvalTimings are the timings of an approve, with the documented defaults
// for unset values: a 30 s lease, a 15 s chain timeout, 30 s minimum
// remaining lifetime and 3 attempts.
func (c DeviceFlowConfig) approvalTimings() DeviceFlowTimings {
	timings := c.timings
	if timings.ApproveLease <= 0 {
		timings.ApproveLease = 30 * time.Second
	}
	if timings.ApprovalTimeout <= 0 {
		timings.ApprovalTimeout = 15 * time.Second
	}
	if timings.MinRemainingToApprove <= 0 {
		timings.MinRemainingToApprove = 30 * time.Second
	}
	if timings.MaxApproveAttempts <= 0 {
		timings.MaxApproveAttempts = 3
	}
	return timings
}

// urlOrigin returns the lowercase scheme://host of an absolute URL, or "".
func urlOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
}

type deviceFlowGateValue struct {
	on bool
	at time.Time
}

// deviceFlowGate caches successful runtime-switch reads for
// deviceFlowGateCacheTTL and collapses concurrent reads. Errors are not cached.
type deviceFlowGate struct {
	read   DeviceFlowRuntimeGate
	now    func() time.Time
	cached atomic.Pointer[deviceFlowGateValue]
	group  singleflight.Group
}

func (g *deviceFlowGate) active(ctx context.Context) (bool, error) {
	if value := g.cached.Load(); value != nil && g.now().Sub(value.at) < deviceFlowGateCacheTTL {
		return value.on, nil
	}
	result, err, _ := g.group.Do("gate", func() (any, error) {
		on, err := g.read(context.WithoutCancel(ctx))
		if err != nil {
			return false, err
		}
		g.cached.Store(&deviceFlowGateValue{on: on, at: g.now()})
		return on, nil
	})
	if err != nil {
		return false, err
	}
	return result.(bool), nil
}
