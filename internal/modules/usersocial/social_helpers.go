package usersocial

import (
	platformAuthHeader "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/authheader"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"strings"
)

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
