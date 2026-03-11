package oauth2

const (
	oauthResponseTypeCode = "code"

	oauthClientTypePublic       = "public"
	oauthClientTypeConfidential = "confidential"

	oauthGrantTypeAuthorizationCode = "authorization_code"
	oauthGrantTypeRefreshToken      = "refresh_token"

	oauthPKCEChallengeMethodS256 = "S256"
	oauthTokenTypeBearer         = "Bearer"

	oauthTokenTypeHintAccessToken  = "access_token"
	oauthTokenTypeHintRefreshToken = "refresh_token"
)
