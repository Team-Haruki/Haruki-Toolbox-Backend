package user

import (
	entSchema "haruki-suite/ent/schema"
	"haruki-suite/utils"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
)

// ====================== Helper Struct ======================

type SessionClaims struct {
	UserID       string `json:"userId"`
	SessionToken string `json:"sessionToken"`
	jwt.RegisteredClaims
}

type SessionHandler struct {
	RedisClient    *redis.Client
	SessionSignKey string
}

type GenericResponse[T any] struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData *T     `json:"updatedData,omitempty"`
}

// ====================== Request Struct ======================

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
	Password string `json:"password"`
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
	Name         string `json:"name"`
	AvatarBase64 string `json:"avatarBase64"`
}

type AuthorizeSocialPlatformPayload struct {
	Platform string `json:"platform"`
	UserID   string `json:"userId"`
	Comment  string `json:"comment"`
}

type GameAccountBindingPayload struct {
	Server  utils.SupportedDataUploadServer       `json:"server"`
	UserID  string                                `json:"userId"`
	Suite   *entSchema.SuiteDataPrivacySettings   `json:"suite"`
	MySekai *entSchema.MysekaiDataPrivacySettings `json:"mysekai"`
}

// ====================== Response Struct ======================

type RegisterOrLoginSuccessResponse struct {
	Status   int                   `json:"status"`
	Message  string                `json:"message"`
	UserData HarukiToolboxUserData `json:"userData"`
}

type HarukiToolboxUserData struct {
	Name                        string                        `json:"name"`
	UserID                      string                        `json:"userId"`
	AvatarPath                  *string                       `json:"avatarPath,omitempty"`
	EmailInfo                   EmailInfo                     `json:"emailInfo"`
	SocialPlatformInfo          *SocialPlatformInfo           `json:"socialPlatformInfo,omitempty"`
	AuthorizeSocialPlatformInfo []AuthorizeSocialPlatformInfo `json:"authorizeSocialPlatformInfo,omitempty"`
	GameAccountBindings         []GameAccountBinding          `json:"gameAccountBindings,omitempty"`
	SessionToken                string                        `json:"sessionToken"`
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
	ID       int    `json:"id"`
	Platform string `json:"platform"`
	UserID   string `json:"userId"`
	Comment  string `json:"comment"`
}

type GameAccountBinding struct {
	ID       int                                   `json:"id"`
	Server   utils.SupportedDataUploadServer       `json:"server"`
	UserID   int                                   `json:"userId"`
	Verified bool                                  `json:"verified"`
	Suite    *entSchema.SuiteDataPrivacySettings   `json:"suite,omitempty"`
	Mysekai  *entSchema.MysekaiDataPrivacySettings `json:"mysekai,omitempty"`
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
