package cloudflare

import (
	"fmt"
	"haruki-suite/config"
	harukiLogger "haruki-suite/utils/logger"
	"time"

	"github.com/bytedance/sonic"
	"github.com/go-resty/resty/v2"
)

const verifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

type TurnstileResponse struct {
	Success     bool     `json:"success"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
	ErrorCodes  []string `json:"error-codes"`
	Action      string   `json:"action,omitempty"`
	Cdata       string   `json:"cdata,omitempty"`
}

func ValidateTurnstile(response, remoteIP string) (*TurnstileResponse, error) {
	payload := map[string]string{
		"secret":   config.Cfg.UserSystem.CloudflareSecret,
		"response": response,
	}
	if remoteIP != "" {
		payload["remoteip"] = remoteIP
	}
	body, _ := sonic.Marshal(payload)
	client := resty.New().SetTimeout(5 * time.Second)
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
