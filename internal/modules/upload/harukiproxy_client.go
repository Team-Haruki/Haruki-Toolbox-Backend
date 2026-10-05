package upload

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type proxyAttemptKey struct{}
type proxyIngressKey struct{}

var proxyMetadataKey = regexp.MustCompile(`^[a-z_]{1,32}$`)
var proxyMetadataValue = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)

func parseProxyUserAgent(raw string) (platform.ClientMetadata, error) {
	m := platform.ClientMetadata{Name: "HarukiProxy", Format: "legacy", Protocol: "3"}
	invalid := fmt.Errorf("invalid User-Agent format")
	if len(raw) > 512 || !strings.HasPrefix(raw, "HarukiProxy/v") {
		return m, invalid
	}
	for _, b := range []byte(raw) {
		if b < 32 || b > 126 {
			return m, invalid
		}
	}
	product, comment, structured := strings.Cut(raw, " (")
	ver := strings.TrimPrefix(product, "HarukiProxy/v")
	_, channel, err := platform.ParseClientVersion(ver)
	if err != nil {
		return m, invalid
	}
	m.Version = strings.Clone(ver)
	m.Channel = strings.Clone(channel)
	if !structured {
		return m, invalid
	}
	if !strings.HasSuffix(comment, ")") {
		return m, invalid
	}
	comment = strings.TrimSuffix(comment, ")")
	seen := map[string]bool{}
	for _, field := range strings.Split(comment, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok || !proxyMetadataKey.MatchString(key) || !proxyMetadataValue.MatchString(value) || seen[key] {
			return m, invalid
		}
		seen[key] = true
		value = strings.Clone(value)
		switch key {
		case "platform":
			switch value {
			case "Windows", "macOS", "Android", "iOS", "Linux":
				m.Platform = strings.ToLower(value)
			default:
				m.Platform = "unknown"
			}
		case "os_version":
			m.OSVersion = value
		case "os_build":
			m.OSBuild = value
		case "os_arch":
			m.OSArch = normalizeProxyArch(value)
		case "app_arch":
			m.AppArch = normalizeProxyArch(value)
		}
	}
	if !seen["platform"] {
		return m, invalid
	}
	m.Format = "structured"
	return m, nil
}
func normalizeProxyArch(value string) string {
	switch value {
	case "x64", "arm64", "x86", "arm":
		return value
	default:
		return "unknown"
	}
}

func proxyAttempt(c fiber.Ctx) *platform.Attempt {
	if a, ok := c.Locals(proxyAttemptKey{}).(*platform.Attempt); ok {
		return a
	}
	a := &platform.Attempt{RequestID: uuid.NewString(), ReceivedAt: time.Now().UTC(), RequestBytes: int64(len(c.Body()))}
	c.Locals(proxyAttemptKey{}, a)
	c.Set("X-Request-ID", a.RequestID)
	return a
}

type proxyClientPolicyResponse struct {
	Channel        string `json:"channel"`
	MinimumVersion string `json:"minimum_version,omitempty"`
}
type proxyResponseData struct {
	ErrorCode    string                     `json:"error_code,omitempty"`
	RequestID    string                     `json:"request_id"`
	Retryable    bool                       `json:"retryable"`
	ClientPolicy *proxyClientPolicyResponse `json:"client_policy,omitempty"`
}

func proxyResponse(c fiber.Ctx, status int, code, message string, retryable bool, policy *proxyClientPolicyResponse) error {
	a := proxyAttempt(c)
	if c.Locals(proxyIngressKey{}) != "accepted" {
		c.Locals(proxyIngressKey{}, code)
	}
	data := proxyResponseData{ErrorCode: code, RequestID: a.RequestID, Retryable: retryable, ClientPolicy: policy}
	return api.Responses.UpdatedDataResponse(c, status, message, &data)
}

func proxyIngress(helper *api.HarukiToolboxRouterHelpers, protocol string) fiber.Handler {
	return func(c fiber.Ctx) error {
		a := proxyAttempt(c)
		a.Client.Protocol = protocol
		defer func() {
			result, _ := c.Locals(proxyIngressKey{}).(string)
			if result == "" {
				result = "internal_error"
			}
			if helper != nil && helper.DBManager != nil {
				platform.RecordProxyIngress(helper.DBManager.Redis, a.ReceivedAt, protocol, result)
			}
		}()
		return c.Next()
	}
}

func validateProxyV3Client(helper *api.HarukiToolboxRouterHelpers, d Dependencies) fiber.Handler {
	policy := d.HarukiProxyV3ClientPolicy
	if policy == nil {
		policy = platform.DefaultClientPolicy()
	}
	return func(c fiber.Ctx) error {
		m, err := parseProxyUserAgent(c.Get("User-Agent"))
		if err != nil {
			return proxyResponse(c, 400, "invalid_client_metadata", "Invalid User-Agent format", false, nil)
		}
		minimum, allowed := policy.Minimum(m.Channel)
		if !allowed {
			// Only valid bounded channel identifiers are echoed.
			channel := m.Channel
			if len(channel) > 32 {
				channel = "unknown"
			}
			return proxyResponse(c, 400, "client_channel_disabled", "Client channel is disabled", false, &proxyClientPolicyResponse{Channel: channel})
		}
		v, _, _ := platform.ParseClientVersion(m.Version)
		if v.LessThan(minimum) {
			return proxyResponse(c, 400, "client_version_unsupported", "Client version is below minimum required", false, &proxyClientPolicyResponse{Channel: m.Channel, MinimumVersion: minimum.String()})
		}
		m.OAuthClientID, _ = c.Locals("oauth2ClientID").(string)
		proxyAttempt(c).AuthMethod = "oauth2"
		proxyAttempt(c).Client = m
		return c.Next()
	}
}
