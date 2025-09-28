package cloudflare

import (
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/go-resty/resty/v2"
)

const verifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

func VerifyTurnstile(secret, response, remoteIP string) (*TurnstileResponse, error) {
	payload := map[string]string{
		"secret":   secret,
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
		return nil, fmt.Errorf("request failed: %w", err)
	}

	var result TurnstileResponse
	if err := sonic.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}
	return &result, nil
}
