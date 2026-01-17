package api

import (
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils"
	"haruki-suite/utils/database/postgresql"
)

// BuildUserDataFromDBUser constructs HarukiToolboxUserData from a database User entity
// This function centralizes the user data building logic to avoid code duplication
func BuildUserDataFromDBUser(user *postgresql.User, sessionToken *string) HarukiToolboxUserData {
	emailInfo := buildEmailInfoFromUser(user)
	socialPlatformInfo := buildSocialPlatformInfoFromUser(user)
	authorizeSocialPlatformInfo := buildAuthorizeSocialPlatformInfoFromUser(user)
	gameAccountBindings := buildGameAccountBindingsFromUser(user)
	avatarURL := buildAvatarURLFromUser(user)
	iosUploadCode := buildIOSUploadCodeFromUser(user)

	return HarukiToolboxUserData{
		Name:                        &user.Name,
		UserID:                      &user.ID,
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

// buildIOSUploadCodeFromUser extracts iOS upload code from user edges
func buildIOSUploadCodeFromUser(user *postgresql.User) *string {
	if user.Edges.IosScriptCode != nil {
		return &user.Edges.IosScriptCode.UploadCode
	}
	return nil
}

// buildEmailInfoFromUser extracts email info from user edges
func buildEmailInfoFromUser(user *postgresql.User) EmailInfo {
	if user.Edges.EmailInfo != nil {
		return EmailInfo{
			Email:    user.Edges.EmailInfo.Email,
			Verified: user.Edges.EmailInfo.Verified,
		}
	}
	return EmailInfo{
		Email:    user.Email,
		Verified: false,
	}
}

// buildSocialPlatformInfoFromUser extracts social platform info from user edges
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

// buildAuthorizeSocialPlatformInfoFromUser extracts authorized social platforms from user edges
func buildAuthorizeSocialPlatformInfoFromUser(user *postgresql.User) []AuthorizeSocialPlatformInfo {
	var result []AuthorizeSocialPlatformInfo
	if user.Edges.AuthorizedSocialPlatforms != nil && len(user.Edges.AuthorizedSocialPlatforms) > 0 {
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

// buildGameAccountBindingsFromUser extracts game account bindings from user edges
func buildGameAccountBindingsFromUser(user *postgresql.User) []GameAccountBinding {
	var result []GameAccountBinding
	if user.Edges.GameAccountBindings != nil && len(user.Edges.GameAccountBindings) > 0 {
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

// buildAvatarURLFromUser constructs the avatar URL from user data
func buildAvatarURLFromUser(user *postgresql.User) string {
	if user.AvatarPath != nil {
		return fmt.Sprintf("%s/avatars/%s", config.Cfg.UserSystem.AvatarURL, *user.AvatarPath)
	}
	return ""
}
