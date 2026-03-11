package api

import (
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils"
	"haruki-suite/utils/database/postgresql"
	"strings"
)

func BuildUserDataFromDBUser(user *postgresql.User, sessionToken *string) HarukiToolboxUserData {
	emailInfo := buildEmailInfoFromUser(user)
	socialPlatformInfo := buildSocialPlatformInfoFromUser(user)
	authorizeSocialPlatformInfo := buildAuthorizeSocialPlatformInfoFromUser(user)
	gameAccountBindings := buildGameAccountBindingsFromUser(user)
	avatarURL := buildAvatarURLFromUser(user)
	iosUploadCode := buildIOSUploadCodeFromUser(user)
	role := string(user.Role)

	return HarukiToolboxUserData{
		Name:                        &user.Name,
		UserID:                      &user.ID,
		Role:                        &role,
		AvatarPath:                  &avatarURL,
		AllowCNMysekai:              &user.AllowCnMysekai,
		IOSUploadCode:               iosUploadCode,
		EmailInfo:                   &emailInfo,
		SocialPlatformInfo:          socialPlatformInfo,
		AuthorizeSocialPlatformInfo: &authorizeSocialPlatformInfo,
		GameAccountBindings:         &gameAccountBindings,
		SessionToken:                sessionToken,
	}
}

func buildIOSUploadCodeFromUser(user *postgresql.User) *string {
	if user.Edges.IosScriptCode != nil {
		return &user.Edges.IosScriptCode.UploadCode
	}
	return nil
}

func buildEmailInfoFromUser(user *postgresql.User) EmailInfo {
	return EmailInfo{
		Email:    user.Email,
		Verified: resolveUserEmailVerified(user),
	}
}

func resolveUserEmailVerified(user *postgresql.User) bool {
	if user == nil || strings.TrimSpace(user.Email) == "" {
		return false
	}
	if user.EmailVerified != nil {
		return *user.EmailVerified
	}
	if user.KratosIdentityID != nil && strings.TrimSpace(*user.KratosIdentityID) != "" {
		return false
	}
	return true
}

func buildSocialPlatformInfoFromUser(user *postgresql.User) *SocialPlatformInfo {
	if user.Edges.SocialPlatformInfo != nil {
		return &SocialPlatformInfo{
			Platform: user.Edges.SocialPlatformInfo.Platform,
			UserID:   user.Edges.SocialPlatformInfo.PlatformUserID,
			Verified: user.Edges.SocialPlatformInfo.Verified,
		}
	}
	return nil
}

func buildAuthorizeSocialPlatformInfoFromUser(user *postgresql.User) []AuthorizeSocialPlatformInfo {
	var result []AuthorizeSocialPlatformInfo
	if len(user.Edges.AuthorizedSocialPlatforms) > 0 {
		result = make([]AuthorizeSocialPlatformInfo, 0, len(user.Edges.AuthorizedSocialPlatforms))
		for _, a := range user.Edges.AuthorizedSocialPlatforms {
			result = append(result, AuthorizeSocialPlatformInfo{
				ID:       a.ID,
				Platform: a.Platform,
				UserID:   a.PlatformUserID,
				Comment:  a.Comment,
			})
		}
	}
	return result
}

func buildGameAccountBindingsFromUser(user *postgresql.User) []GameAccountBinding {
	var result []GameAccountBinding
	if len(user.Edges.GameAccountBindings) > 0 {
		result = make([]GameAccountBinding, 0, len(user.Edges.GameAccountBindings))
		for _, g := range user.Edges.GameAccountBindings {
			result = append(result, GameAccountBinding{
				Server:   utils.SupportedDataUploadServer(g.Server),
				UserID:   g.GameUserID,
				Verified: g.Verified,
				Suite:    g.Suite,
				Mysekai:  g.Mysekai,
			})
		}
	}
	return result
}

func buildAvatarURLFromUser(user *postgresql.User) string {
	if user.AvatarPath != nil {
		return fmt.Sprintf("%s/avatars/%s", config.Cfg.UserSystem.AvatarURL, *user.AvatarPath)
	}
	return ""
}
