package webhook

import (
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const TokenHeaderName = "X-Haruki-Suite-Webhook-Token"

func SignWebhookToken(secret, webhookID, credential string) (string, error) {
	claims := jwt.MapClaims{
		"_id":        strings.TrimSpace(webhookID),
		"credential": strings.TrimSpace(credential),
		"iat":        time.Now().UTC().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(strings.TrimSpace(secret)))
}
