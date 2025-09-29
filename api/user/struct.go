package user

import "haruki-suite/utils"

// ====================== Request Struct ======================

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
	OneTimePassword string `json:"oneTimePassword"`
}

type SendQQMailPayload struct {
	QQ             string `json:"qq"`
	ChallengeToken string `json:"challengeToken"`
}

type VerifyQQMailPayload struct {
	OneTimePassword string `json:"oneTimePassword"`
}

type GenerateSocialPlatformCodePayload struct {
	Platform string `json:"platform"` // qq | qqbot | discord | telegram
	UserID   string `json:"userId"`
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
	Server utils.SupportedDataUploadServer `json:"server"`
	UserID int                             `json:"userId"`
}

type GenerateGameAccountCodePayload struct {
	Server utils.SupportedDataUploadServer `json:"server"`
	UserID string                          `json:"userId"`
}

// ====================== Response Struct ======================

type APIResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

type RegisterOrLoginSuccessResponse struct {
	Status   int      `json:"status"`
	Message  string   `json:"message"`
	UserData UserData `json:"userData"`
}

type UserData struct {
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

type AuthorizeSocialPlatformInfo struct {
	ID       int    `json:"id"`
	Platform string `json:"platform"`
	UserID   string `json:"userId"`
	Comment  string `json:"comment"`
}

type GameAccountBinding struct {
	ID       int                             `json:"id"`
	Server   utils.SupportedDataUploadServer `json:"server"`
	UserID   int                             `json:"userId"`
	Verified bool                            `json:"verified"`
	Suite    *SuiteDataPrivacySettings       `json:"suite,omitempty"`
	Mysekai  *MysekaiDataPrivacySettings     `json:"mysekai,omitempty"`
}

type SuiteDataPrivacySettings struct {
	AllowPublicApi bool `json:"allowPublicApi"`
	AllowSakura    bool `json:"allowSakura"`
	Allow8823      bool `json:"allow8823"`
	AllowResona    bool `json:"allowResona"`
}

type MysekaiDataPrivacySettings struct {
	AllowPublicApi  bool `json:"allowPublicApi"`
	AllowFixtureApi bool `json:"allowFixtureApi"`
	Allow8823       bool `json:"allow8823"`
	AllowResona     bool `json:"allowResona"`
}

type VerifyEmailResponse struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData struct {
		Email EmailInfo `json:"email"`
	} `json:"updatedData"`
}

type VerifyQQMailResponse struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData struct {
		SocialPlatform SocialPlatformInfo `json:"socialPlatform"`
	} `json:"updatedData"`
}

type GenerateSocialPlatformCodeResponse struct {
	Status          int    `json:"status"`
	Message         string `json:"message"`
	StatusToken     string `json:"statusToken"`
	OneTimePassword string `json:"oneTimePassword"`
}

type SocialPlatformVerificationStatusResponse struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData *struct {
		SocialPlatform SocialPlatformInfo `json:"socialPlatform"`
	} `json:"updatedData,omitempty"`
}

type UpdateProfileResponse struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData struct {
		Name       string `json:"name"`
		AvatarPath string `json:"avatarPath"`
	} `json:"updatedData"`
}

type AuthorizeSocialPlatformResponse struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData struct {
		AuthorizeSocialPlatformInfo []AuthorizeSocialPlatformInfo `json:"authorizeSocialPlatformInfo"`
	} `json:"updatedData"`
}

type GameAccountBindingResponse struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData struct {
		GameAccountBindings []GameAccountBinding `json:"gameAccountBindings"`
	} `json:"updatedData"`
}

type GenerateGameAccountCodeResponse struct {
	Status          int    `json:"status"`
	Message         string `json:"message"`
	StatusToken     string `json:"statusToken"`
	OneTimePassword string `json:"oneTimePassword"`
}

type GameAccountVerificationStatusResponse struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData *struct {
		GameAccountBindings []GameAccountBinding `json:"gameAccountBindings"`
	} `json:"updatedData,omitempty"`
}
