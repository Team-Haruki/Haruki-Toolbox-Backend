package bootstrap

import (
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
)

func validateUserSystemConfig(cfg harukiConfig.Config) error {
	provider := strings.ToLower(strings.TrimSpace(cfg.UserSystem.AuthProvider))
	if provider == "" {
		provider = "kratos"
	}
	if provider != "kratos" {
		return fmt.Errorf("user_system.auth_provider=%q is unsupported; only kratos is supported", strings.TrimSpace(cfg.UserSystem.AuthProvider))
	}
	if strings.TrimSpace(cfg.UserSystem.KratosPublicURL) == "" {
		return fmt.Errorf("user_system.kratos_public_url is required")
	}
	if strings.TrimSpace(cfg.UserSystem.KratosAdminURL) == "" {
		return fmt.Errorf("user_system.kratos_admin_url is required")
	}
	if cfg.UserSystem.AuthProxyEnabled {
		if strings.TrimSpace(cfg.UserSystem.AuthProxyTrustedHeader) == "" {
			return fmt.Errorf("user_system.auth_proxy_trusted_header is required when auth_proxy_enabled=true")
		}
		trustedValue := strings.TrimSpace(cfg.UserSystem.AuthProxyTrustedValue)
		if trustedValue == "" {
			return fmt.Errorf("user_system.auth_proxy_trusted_value is required when auth_proxy_enabled=true")
		}
		// This single secret is the entire identity-forging trust boundary, so refuse
		// to start with the shipped placeholder or an obviously weak value.
		if trustedValue == "change-me-auth-proxy-secret" || len(trustedValue) < 16 {
			return fmt.Errorf("user_system.auth_proxy_trusted_value must be changed from the placeholder and be at least 16 characters")
		}
		if strings.TrimSpace(cfg.UserSystem.AuthProxySubjectHeader) == "" {
			return fmt.Errorf("user_system.auth_proxy_subject_header is required when auth_proxy_enabled=true")
		}
		if strings.TrimSpace(cfg.UserSystem.AuthProxySessionHeader) == "" {
			return fmt.Errorf("user_system.auth_proxy_session_header is required when auth_proxy_enabled=true")
		}
	}
	return nil
}

func validateOAuth2ProviderConfig(cfg harukiConfig.Config) error {
	provider := strings.ToLower(strings.TrimSpace(cfg.OAuth2.Provider))
	if provider == "" || provider == harukiOAuth2.ProviderHydra {
		if strings.TrimSpace(cfg.OAuth2.HydraPublicURL) == "" {
			return fmt.Errorf("oauth2.hydra_public_url is required when oauth2.provider=hydra")
		}
		if strings.TrimSpace(cfg.OAuth2.HydraAdminURL) == "" {
			return fmt.Errorf("oauth2.hydra_admin_url is required when oauth2.provider=hydra")
		}
		return nil
	}
	return fmt.Errorf("oauth2.provider=%q is unsupported; only hydra is supported", strings.TrimSpace(cfg.OAuth2.Provider))
}

func validateBackendConfig(cfg harukiConfig.Config) error {
	if cfg.Backend.SSL {
		if strings.TrimSpace(cfg.Backend.SSLCert) == "" || strings.TrimSpace(cfg.Backend.SSLKey) == "" {
			return fmt.Errorf("backend.ssl_cert and backend.ssl_key are required when backend.ssl=true")
		}
	}
	if cfg.Backend.EnableTrustProxy {
		if strings.TrimSpace(cfg.Backend.ProxyHeader) == "" {
			return fmt.Errorf("backend.proxy_header is required when backend.enable_trust_proxy=true")
		}
		if len(cfg.Backend.TrustProxies) == 0 {
			return fmt.Errorf("backend.trusted_proxies must contain the exact edge proxy IP when backend.enable_trust_proxy=true")
		}
		for _, rawProxy := range cfg.Backend.TrustProxies {
			proxy := strings.TrimSpace(rawProxy)
			if proxy == "" {
				return fmt.Errorf("backend.trusted_proxies must not contain an empty value")
			}
			if _, err := netip.ParseAddr(proxy); err == nil {
				continue
			}
			prefix, err := netip.ParsePrefix(proxy)
			if err != nil || prefix.Bits() != prefix.Addr().BitLen() {
				return fmt.Errorf("backend.trusted_proxies entry %q must be an exact edge proxy IP (/32 for IPv4 or /128 for IPv6), not a shared network", proxy)
			}
		}
	}
	return nil
}

func validateBotRegistrationConfig(cfg harukiConfig.Config) error {
	if cfg.HarukiBot.EnableRegistration && strings.TrimSpace(cfg.HarukiBot.CredentialSignToken) == "" {
		return fmt.Errorf("haruki_bot.credential_sign_token is required when haruki_bot.enable_registration=true")
	}
	return nil
}

// Device-flow brute-force budget (design §8.3): the expected number of user
// codes guessed per year in the worst case must stay at or below this.
const (
	deviceFlowBudgetMaxHitsPerYear = 1.0
	deviceFlowWindowsPerYear       = 365 * 24 * 6
	deviceFlowLimitWindow          = 10 * time.Minute
	deviceFlowMinSessionSignToken  = 16
)

// validateOAuth2DeviceFlowConfig checks oauth2.device_flow. It does nothing
// while the device flow is disabled, so a deployment that has not set
// user_system.session_sign_token yet keeps starting.
func validateOAuth2DeviceFlowConfig(cfg harukiConfig.Config) error {
	flow := cfg.OAuth2.DeviceFlow
	if !flow.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.OAuth2.HydraPublicURL) == "" || strings.TrimSpace(cfg.OAuth2.HydraAdminURL) == "" || strings.TrimSpace(cfg.OAuth2.HydraBrowserURL) == "" {
		return fmt.Errorf("oauth2.device_flow requires oauth2.hydra_public_url, hydra_admin_url and hydra_browser_url")
	}
	if strings.TrimSpace(cfg.Redis.Host) == "" {
		return fmt.Errorf("oauth2.device_flow requires redis")
	}
	if err := validateDeviceFlowAbsoluteURL("oauth2.device_flow.hydra_issuer_url", deviceFlowIssuerURL(cfg)); err != nil {
		return err
	}
	if err := validateDeviceFlowAbsoluteURL("oauth2.device_flow.verification_url", deviceFlowVerificationURL(cfg)); err != nil {
		return err
	}
	origins := flow.AllowedOrigins
	if len(origins) == 0 {
		origins = []string{deviceFlowOriginOf(cfg.UserSystem.FrontendURL)}
	}
	for _, origin := range origins {
		if !isDeviceFlowPureOrigin(origin) {
			return fmt.Errorf("oauth2.device_flow.allowed_origins entry %q must be a scheme://host[:port] origin (empty uses user_system.frontend_url)", origin)
		}
	}
	for _, clientID := range flow.ClientAllowlist {
		if strings.TrimSpace(clientID) == "" {
			return fmt.Errorf("oauth2.device_flow.client_allowlist must not contain an empty value")
		}
	}

	charset := flow.UserCodeCharset
	if len(charset) < 8 {
		return fmt.Errorf("oauth2.device_flow.user_code_charset must have at least 8 characters, as Hydra requires")
	}
	seen := make(map[rune]struct{}, len(charset))
	for _, r := range charset {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return fmt.Errorf("oauth2.device_flow.user_code_charset may only contain A-Z and 0-9")
		}
		if _, dup := seen[r]; dup {
			return fmt.Errorf("oauth2.device_flow.user_code_charset must not repeat characters")
		}
		seen[r] = struct{}{}
	}
	if flow.UserCodeLength < 6 || flow.UserCodeLength > 12 {
		return fmt.Errorf("oauth2.device_flow.user_code_length must be between 6 and 12")
	}

	limits := flow.Limits
	for name, value := range map[string]int{
		"auth_attempt_unknown_client_warn_per_10m": limits.AuthAttemptUnknownClientWarnPer10m,
		"auth_attempt_client_warn_multiplier":      limits.AuthAttemptClientWarnMultiplier,
		"auth_issued_global_per_10m":               limits.AuthIssuedGlobalPer10m,
		"auth_issued_public_per_10m":               limits.AuthIssuedPublicPer10m,
		"auth_issued_confidential_per_10m":         limits.AuthIssuedConfidentialPer10m,
		"auth_issued_client_default_per_10m":       limits.AuthIssuedClientDefaultPer10m,
		"lookup_user_per_10m":                      limits.LookupUserPer10m,
		"lookup_fail_user_per_10m":                 limits.LookupFailUserPer10m,
		"lookup_fail_user_per_day":                 limits.LookupFailUserPerDay,
		"lookup_fail_global_per_10m":               limits.LookupFailGlobalPer10m,
		"decision_user_per_day":                    limits.DecisionUserPerDay,
		"max_slow_down":                            limits.MaxSlowDown,
		"max_interval_seconds":                     limits.MaxIntervalSeconds,
	} {
		if value <= 0 {
			return fmt.Errorf("oauth2.device_flow.limits.%s must be positive", name)
		}
	}
	if limits.AuthIssuedPublicPer10m+limits.AuthIssuedConfidentialPer10m > limits.AuthIssuedGlobalPer10m {
		return fmt.Errorf("oauth2.device_flow.limits: auth_issued_public_per_10m + auth_issued_confidential_per_10m must not exceed auth_issued_global_per_10m")
	}

	for name, value := range map[string]int{
		"min_poll_interval_seconds":        flow.MinPollIntervalSeconds,
		"claim_ttl_seconds":                flow.ClaimTTLSeconds,
		"approve_lease_seconds":            flow.ApproveLeaseSeconds,
		"approval_timeout_seconds":         flow.ApprovalTimeoutSeconds,
		"min_remaining_seconds_to_approve": flow.MinRemainingSecondsToApprove,
		"max_approve_attempts":             flow.MaxApproveAttempts,
		"record_grace_seconds":             flow.RecordGraceSeconds,
		"reaper_interval_seconds":          flow.ReaperIntervalSeconds,
		"reaper_grace_seconds":             flow.ReaperGraceSeconds,
	} {
		if value <= 0 {
			return fmt.Errorf("oauth2.device_flow.%s must be positive", name)
		}
	}
	if flow.MinPollIntervalSeconds > limits.MaxIntervalSeconds {
		return fmt.Errorf("oauth2.device_flow.min_poll_interval_seconds must not exceed limits.max_interval_seconds")
	}
	// The whole server-driven chain must finish well inside its lease, and an
	// approval must not start with less time left than the chain may take.
	if flow.ApprovalTimeoutSeconds+10 >= flow.ApproveLeaseSeconds {
		return fmt.Errorf("oauth2.device_flow.approval_timeout_seconds + 10 must be less than approve_lease_seconds")
	}
	if flow.MinRemainingSecondsToApprove < flow.ApprovalTimeoutSeconds {
		return fmt.Errorf("oauth2.device_flow.min_remaining_seconds_to_approve must be at least approval_timeout_seconds")
	}

	ttl, err := time.ParseDuration(strings.TrimSpace(flow.UserCodeTTL))
	if err != nil {
		return fmt.Errorf("oauth2.device_flow.user_code_ttl %q is not a duration: %w", flow.UserCodeTTL, err)
	}
	if ttl < time.Minute || ttl > 30*time.Minute {
		return fmt.Errorf("oauth2.device_flow.user_code_ttl must be between 1m and 30m")
	}
	if hits := deviceFlowBruteForceBudget(flow, ttl); hits > deviceFlowBudgetMaxHitsPerYear {
		return fmt.Errorf("oauth2.device_flow limits allow %.2f expected user-code guesses per year (max %.1f); lower lookup_fail_global_per_10m or auth_issued_global_per_10m, or shorten user_code_ttl", hits, deviceFlowBudgetMaxHitsPerYear)
	}

	// The device flow's Redis keys are HMACs under this secret; without it a
	// Redis dump could be brute-forced offline across the user-code space.
	if len(strings.TrimSpace(cfg.UserSystem.SessionSignToken)) < deviceFlowMinSessionSignToken {
		return fmt.Errorf("oauth2.device_flow requires user_system.session_sign_token of at least %d characters", deviceFlowMinSessionSignToken)
	}
	return nil
}

// deviceFlowBruteForceBudget is the worst-case expected number of user codes
// guessed per year: every 10-minute window may spend the global lookup-failure
// budget against the codes alive at once, which are at most the global
// issuance of (ceil(ttl / 10 min) + 1) windows, out of charset^length codes.
func deviceFlowBruteForceBudget(flow harukiConfig.OAuth2DeviceFlowConfig, ttl time.Duration) float64 {
	liveWindows := math.Ceil(float64(ttl)/float64(deviceFlowLimitWindow)) + 1
	codeSpace := math.Pow(float64(utf8.RuneCountInString(flow.UserCodeCharset)), float64(flow.UserCodeLength))
	return deviceFlowWindowsPerYear * float64(flow.Limits.LookupFailGlobalPer10m) * liveWindows * float64(flow.Limits.AuthIssuedGlobalPer10m) / codeSpace
}

// deviceFlowIssuerURL is oauth2.device_flow.hydra_issuer_url, defaulting to
// oauth2.hydra_browser_url.
func deviceFlowIssuerURL(cfg harukiConfig.Config) string {
	if issuer := strings.TrimSpace(cfg.OAuth2.DeviceFlow.HydraIssuerURL); issuer != "" {
		return issuer
	}
	return strings.TrimSpace(cfg.OAuth2.HydraBrowserURL)
}

// deviceFlowVerificationURL is oauth2.device_flow.verification_url, defaulting
// to {user_system.frontend_url}/device.
func deviceFlowVerificationURL(cfg harukiConfig.Config) string {
	if verification := strings.TrimSpace(cfg.OAuth2.DeviceFlow.VerificationURL); verification != "" {
		return verification
	}
	if frontend := strings.TrimRight(strings.TrimSpace(cfg.UserSystem.FrontendURL), "/"); frontend != "" {
		return frontend + "/device"
	}
	return ""
}

func deviceFlowOriginOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func isDeviceFlowPureOrigin(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return false
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return false
	}
	return parsed.Path == "" || parsed.Path == "/"
}

// validateDeviceFlowAbsoluteURL requires an absolute https URL; plain http is
// only accepted for localhost and 127.0.0.1.
func validateDeviceFlowAbsoluteURL(name, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("%s must be an absolute URL, got %q", name, raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if host := parsed.Hostname(); host == "localhost" || host == "127.0.0.1" {
			return nil
		}
	}
	return fmt.Errorf("%s must use https (http only for localhost), got %q", name, raw)
}

// validateOAuth2InternalAPIConfig checks oauth2.internal_api.token_sha256:
// empty disables the internal API, anything else must be 64 hex characters.
func validateOAuth2InternalAPIConfig(cfg harukiConfig.Config) error {
	_, err := oauth2Module.ParseInternalAPIConfig(cfg.OAuth2.InternalAPI.TokenSHA256)
	return err
}
