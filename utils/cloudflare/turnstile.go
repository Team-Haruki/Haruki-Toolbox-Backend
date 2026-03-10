package cloudflare

import (
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
		return nil, fmt.Errorf("request failed: %w", err)
	}
	var result TurnstileResponse
	if err := sonic.Unmarshal(resp.Body(), &result); err != nil {
		harukiLogger.Errorf("Turnstile response decode failed: %v, body: %s", err, string(resp.Body()))
		return nil, fmt.Errorf("decode failed: %w", err)
	}
	return &result, nil
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
