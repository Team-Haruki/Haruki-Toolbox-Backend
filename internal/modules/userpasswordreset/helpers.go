package userpasswordreset

import (
	"fmt"
	"net/url"
	"strings"
)

func buildResetPasswordURL(frontendURL, resetSecret, email string) string {
	return fmt.Sprintf(
		"%s/user/reset-password/%s?email=%s",
		strings.TrimRight(frontendURL, "/"),
		resetSecret,
		url.QueryEscape(strings.TrimSpace(email)),
	)
}
