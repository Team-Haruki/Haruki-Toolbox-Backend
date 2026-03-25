package api

import (
	"haruki-suite/ent/schema"
	"haruki-suite/utils"
)

type HarukiToolboxGameAccountPrivacySettings struct {
	Suite   *schema.SuiteDataPrivacySettings   `json:"suite"`
	Mysekai *schema.MysekaiDataPrivacySettings `json:"mysekai"`
}

type SocialPlatform string

const (
	SocialPlatformQQ       SocialPlatform = "qq"
	SocialPlatformQQBot    SocialPlatform = "qqbot"
	SocialPlatformDiscord  SocialPlatform = "discord"
	SocialPlatformTelegram SocialPlatform = "telegram"
)

type RegisterPayload struct {
	Name            string `json:"name"`
	Email           string `json:"email"`
	Password        string `json:"password"`
	OneTimePassword string `json:"oneTimePassword"`
	ChallengeToken  string `json:"challengeToken"`
}

type LoginPayload struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	ChallengeToken string `json:"challengeToken"`
}

type SendResetPasswordPayload struct {
	Email          string `json:"email"`
	ChallengeToken string `json:"challengeToken"`
}

type ResetPasswordPayload struct {
	Email         string `json:"email"`
	OneTimeSecret string `json:"oneTimeSecret"`
	Password      string `json:"password"`
}

type ChangePasswordPayload struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

type SendEmailPayload struct {
	Email          string `json:"email"`
	ChallengeToken string `json:"challengeToken"`
}

type VerifyEmailPayload struct {
	Email           string `json:"email"`
	OneTimePassword string `json:"oneTimePassword"`
}

type SendQQMailPayload struct {
	QQ             string `json:"qq"`
	ChallengeToken string `json:"challengeToken"`
}

type VerifyQQMailPayload struct {
	QQ              string `json:"qq"`
	OneTimePassword string `json:"oneTimePassword"`
}

type GenerateSocialPlatformCodePayload struct {
	Platform SocialPlatform `json:"platform"`
	UserID   string         `json:"userId"`
}

type UpdateProfilePayload struct {
	AvatarBase64 *string `json:"avatarBase64"`
}

type AuthorizeSocialPlatformPayload struct {
	Platform              string `json:"platform"`
	UserID                string `json:"userId"`
	Comment               string `json:"comment"`
	AllowFastVerification bool   `json:"allowFastVerification"`
}

type GameAccountBindingPayload struct {
	Server  utils.SupportedDataUploadServer    `json:"server"`
	UserID  string                             `json:"userId"`
	Suite   *schema.SuiteDataPrivacySettings   `json:"suite"`
	MySekai *schema.MysekaiDataPrivacySettings `json:"mysekai"`
}

type CreateGameAccountBindingPayload struct {
	Suite   *schema.SuiteDataPrivacySettings   `json:"suite"`
	MySekai *schema.MysekaiDataPrivacySettings `json:"mysekai"`
}

type RegisterOrLoginSuccessResponse struct {
	Status   int                   `json:"status"`
	Message  string                `json:"message"`
	UserData HarukiToolboxUserData `json:"userData"`
}

type HarukiToolboxUserData struct {
	Name                        *string                        `json:"name,omitempty"`
	UserID                      *string                        `json:"userId,omitempty"`
	Role                        *string                        `json:"role,omitempty"`
	AvatarPath                  *string                        `json:"avatarPath,omitempty"`
	AllowCNMysekai              *bool                          `json:"allowCNMysekai,omitempty"`
	IOSUploadCode               *string                        `json:"iosUploadCode,omitempty"`
	EmailInfo                   *EmailInfo                     `json:"emailInfo,omitempty"`
	SocialPlatformInfo          *SocialPlatformInfo            `json:"socialPlatformInfo,omitempty"`
	AuthorizeSocialPlatformInfo *[]AuthorizeSocialPlatformInfo `json:"authorizeSocialPlatformInfo,omitempty"`
	GameAccountBindings         *[]GameAccountBinding          `json:"gameAccountBindings,omitempty"`
	SessionToken                *string                        `json:"sessionToken,omitempty"`
}

type EmailInfo struct {
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
}

type SocialPlatformInfo struct {
	Platform string `json:"platform"`
	UserID   string `json:"userId"`
	Verified bool   `json:"verified"`
}

type HarukiBotVerifySocialPlatformPayload struct {
	Platform        SocialPlatform `json:"platform"`
	UserID          string         `json:"userId"`
	OneTimePassword string         `json:"oneTimePassword"`
}

type AuthorizeSocialPlatformInfo struct {
	PlatformID            int    `json:"platformId"`
	Platform              string `json:"platform"`
	UserID                string `json:"userId"`
	Comment               string `json:"comment"`
	AllowFastVerification bool   `json:"allowFastVerification"`
}

type GameAccountBinding struct {
	Server   utils.SupportedDataUploadServer    `json:"server"`
	UserID   string                             `json:"userId"`
	Verified bool                               `json:"verified"`
	Suite    *schema.SuiteDataPrivacySettings   `json:"suite,omitempty"`
	Mysekai  *schema.MysekaiDataPrivacySettings `json:"mysekai,omitempty"`
}

type GenerateSocialPlatformCodeResponse struct {
	Status          int    `json:"status"`
	Message         string `json:"message"`
	StatusToken     string `json:"statusToken"`
	OneTimePassword string `json:"oneTimePassword"`
}

type GenerateGameAccountCodeResponse struct {
	Status          int    `json:"status"`
	Message         string `json:"message"`
	OneTimePassword string `json:"oneTimePassword"`
}
