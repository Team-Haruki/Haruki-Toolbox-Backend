package oauth2

import (
	"testing"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
)

func TestBuildHydraOIDCIDTokenClaimsHonorsGrantedScopes(t *testing.T) {
	testCases := []struct {
		name              string
		scopes            []string
		emailVerified     bool
		wantName          bool
		wantEmail         bool
		wantEmailVerified bool
	}{
		{name: "openid only", scopes: []string{harukiOAuth2.ScopeOpenID}},
		{name: "profile", scopes: []string{harukiOAuth2.ScopeOpenID, harukiOAuth2.ScopeProfile}, wantName: true},
		{name: "unverified email", scopes: []string{harukiOAuth2.ScopeOpenID, harukiOAuth2.ScopeEmail}, wantEmail: true},
		{name: "verified email", scopes: []string{harukiOAuth2.ScopeOpenID, harukiOAuth2.ScopeEmail}, emailVerified: true, wantEmail: true, wantEmailVerified: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			claims := buildHydraOIDCIDTokenClaims(" user-1 ", "Haruki", "user@example.com", testCase.emailVerified, testCase.scopes)
			if claims["uid"] != "user-1" {
				t.Fatalf("uid = %#v, want %q", claims["uid"], "user-1")
			}
			_, hasName := claims["name"]
			if hasName != testCase.wantName {
				t.Fatalf("name presence = %v, want %v", hasName, testCase.wantName)
			}
			_, hasEmail := claims["email"]
			if hasEmail != testCase.wantEmail {
				t.Fatalf("email presence = %v, want %v", hasEmail, testCase.wantEmail)
			}
			verified, hasEmailVerified := claims["email_verified"].(bool)
			if hasEmailVerified != testCase.wantEmail {
				t.Fatalf("email_verified presence = %v, want %v", hasEmailVerified, testCase.wantEmail)
			}
			if hasEmailVerified && verified != testCase.wantEmailVerified {
				t.Fatalf("email_verified = %v, want %v", verified, testCase.wantEmailVerified)
			}
		})
	}
}

func TestBuildHydraOIDCIDTokenClaimsOmitsBlankEmail(t *testing.T) {
	claims := buildHydraOIDCIDTokenClaims("user-1", "Haruki", " ", true, []string{
		harukiOAuth2.ScopeOpenID,
		harukiOAuth2.ScopeEmail,
	})
	if _, exists := claims["email"]; exists {
		t.Fatalf("blank email must not be exposed")
	}
	if _, exists := claims["email_verified"]; exists {
		t.Fatalf("email_verified must not be exposed without email")
	}
}
