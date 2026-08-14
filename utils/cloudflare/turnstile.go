package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/bytedance/sonic"
	"github.com/go-resty/resty/v2"
)

const (
	verifyURL      = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	defaultTimeout = 5 * time.Second
)

var ErrTurnstileUnavailable = errors.New("turnstile service unavailable")

type Config struct {
	Secret  string
	Bypass  bool
	Proxy   string
	Timeout time.Duration
}

type Verifier interface {
	Verify(ctx context.Context, response, remoteIP string) (*TurnstileResponse, error)
}

type Client struct {
	secret     string
	bypass     bool
	httpClient *resty.Client
}

type TurnstileResponse struct {
	Success     bool     `json:"success"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
	ErrorCodes  []string `json:"error-codes"`
	Action      string   `json:"action,omitempty"`
	Cdata       string   `json:"cdata,omitempty"`
}

func NewClient(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	httpClient := resty.New().SetTimeout(timeout)
	if proxy := strings.TrimSpace(cfg.Proxy); proxy != "" {
		httpClient.SetProxy(proxy)
	}
	return &Client{
		secret:     cfg.Secret,
		bypass:     cfg.Bypass,
		httpClient: httpClient,
	}
}

func IsTurnstileUnavailable(err error) bool {
	return errors.Is(err, ErrTurnstileUnavailable)
}

// Verify delegates to verifier while treating a missing route dependency as an
// unavailable upstream instead of allowing an interface method call to panic.
func Verify(ctx context.Context, verifier Verifier, response, remoteIP string) (*TurnstileResponse, error) {
	if verifier == nil {
		return nil, fmt.Errorf("%w: verifier is not configured", ErrTurnstileUnavailable)
	}
	return verifier.Verify(ctx, response, remoteIP)
}

func (c *Client) Verify(ctx context.Context, response, remoteIP string) (*TurnstileResponse, error) {
	if c == nil || c.httpClient == nil {
		return nil, fmt.Errorf("%w: client is not configured", ErrTurnstileUnavailable)
	}
	if c.bypass {
		return &TurnstileResponse{Success: true}, nil
	}
	payload := map[string]string{
		"secret":   c.secret,
		"response": response,
	}
	if remoteIP != "" {
		payload["remoteip"] = remoteIP
	}
	body, _ := sonic.Marshal(payload)
	request := c.httpClient.R().
		SetHeader("Content-Type", "application/json").
		SetBody(body)
	if ctx != nil {
		request.SetContext(ctx)
	}
	resp, err := request.Post(verifyURL)
	if err != nil {
		harukiLogger.Errorf("Turnstile request failed: %v", err)
		return nil, fmt.Errorf("%w: request failed: %v", ErrTurnstileUnavailable, err)
	}
	if resp.StatusCode() != 200 {
		harukiLogger.Errorf("Turnstile returned unexpected status %d: %s", resp.StatusCode(), string(resp.Body()))
		return nil, fmt.Errorf("%w: unexpected status %d", ErrTurnstileUnavailable, resp.StatusCode())
	}
	var result TurnstileResponse
	if err := sonic.Unmarshal(resp.Body(), &result); err != nil {
		harukiLogger.Errorf("Turnstile response decode failed: %v, body: %s", err, string(resp.Body()))
		return nil, fmt.Errorf("%w: decode failed: %v", ErrTurnstileUnavailable, err)
	}
	if !result.Success && isTurnstileServiceFailure(result.ErrorCodes) {
		return &result, fmt.Errorf("%w: turnstile service rejected request", ErrTurnstileUnavailable)
	}
	return &result, nil
}

func isTurnstileServiceFailure(errorCodes []string) bool {
	for _, code := range errorCodes {
		switch strings.TrimSpace(code) {
		case "internal-error", "missing-input-secret", "invalid-input-secret":
			return true
		}
	}
	return false
}
