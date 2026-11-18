package usersocial

import (
	"strings"
	"unicode"
	"unicode/utf8"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platformAuthHeader "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/authheader"
)

func validSocialVerificationUserID(id string) bool {
	return len(id) > 0 && len(id) <= 128 && utf8.ValidString(id) &&
		strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func statusTokenOwnedByUser(ownerUserID, currentUserID string) bool {
	ownerUserID = strings.TrimSpace(ownerUserID)
	currentUserID = strings.TrimSpace(currentUserID)
	return ownerUserID != "" && currentUserID != "" && ownerUserID == currentUserID
}

func isSupportedSocialPlatform(platform harukiAPIHelper.SocialPlatform) bool {
	switch platform {
	case harukiAPIHelper.SocialPlatformQQ,
		harukiAPIHelper.SocialPlatformQQBot,
		harukiAPIHelper.SocialPlatformDiscord,
		harukiAPIHelper.SocialPlatformTelegram:
		return true
	default:
		return false
	}
}

func extractBearerToken(authHeader string) (string, bool) {
	return platformAuthHeader.ExtractBearerToken(authHeader)
}
