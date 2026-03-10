package cloudflare

import (
	"errors"
	"fmt"
	"haruki-suite/config"
	harukiLogger "haruki-suite/utils/logger"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/go-resty/resty/v2"
)

const verifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

var ErrTurnstileUnavailable = errors.New("turnstile service unavailable")

var (
	turnstileClientMu    sync.RWMutex
	turnstileClient      *resty.Client
	turnstileClientProxy string
)

type TurnstileResponse struct {
	Success     bool     `json:"success"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
	ErrorCodes  []string `json:"error-codes"`
	Action      string   `json:"action,omitempty"`
	Cdata       string   `json:"cdata,omitempty"`
}

func IsTurnstileUnavailable(err error) bool {
	return errors.Is(err, ErrTurnstileUnavailable)
}

func ValidateTurnstile(response, remoteIP string) (*TurnstileResponse, error) {
	if config.Cfg.UserSystem.TurnstileBypass {
		return &TurnstileResponse{
			Success: true,
		}, nil
	}
	payload := map[string]string{
		"secret":   config.Cfg.UserSystem.CloudflareSecret,
		"response": response,
	}
	if remoteIP != "" {
		payload["remoteip"] = remoteIP
	}
	body, _ := sonic.Marshal(payload)
	client := turnstileHTTPClient(config.Cfg.Proxy)
	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(body).
		Post(verifyURL)
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

func turnstileHTTPClient(proxy string) *resty.Client {
	proxy = strings.TrimSpace(proxy)

	turnstileClientMu.RLock()
	if turnstileClient != nil && turnstileClientProxy == proxy {
		client := turnstileClient
		turnstileClientMu.RUnlock()
		return client
	}
	turnstileClientMu.RUnlock()

	client := resty.New().SetTimeout(5 * time.Second)
	if proxy != "" {
		client.SetProxy(proxy)
	}

	turnstileClientMu.Lock()
	defer turnstileClientMu.Unlock()
	if turnstileClient == nil || turnstileClientProxy != proxy {
		turnstileClient = client
		turnstileClientProxy = proxy
	}
	return turnstileClient
}
