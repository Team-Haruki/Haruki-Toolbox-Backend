package oauth2

import (
	"bytes"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func normalizeChallenge(bodyChallenge string, queryChallenge string) string {
	if strings.TrimSpace(bodyChallenge) != "" {
		return strings.TrimSpace(bodyChallenge)
	}
	return strings.TrimSpace(queryChallenge)
}

func bindBodyIfPresent(c fiber.Ctx, payload any) error {
	if len(bytes.TrimSpace(c.Body())) == 0 {
		return nil
	}
	return c.Bind().Body(payload)
}
