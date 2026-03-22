package oauth2

import (
	"github.com/gofiber/fiber/v3"
	"haruki-suite/utils/database/postgresql"
	"slices"
)

func shouldCreateAuthorizationOnLookupErr(err error) (bool, error) {
	if err == nil {
		return false, nil
	}
	if postgresql.IsNotFound(err) {
		return true, nil
	}
	return false, err
}

func buildConsentDeniedRedirectURL(redirectURI, state string, registeredRedirectURIs []string) (string, error) {
	if !slices.Contains(registeredRedirectURIs, redirectURI) {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid redirect_uri")
	}
	return buildRedirectURL(redirectURI, state, map[string]string{
		"error":             "access_denied",
		"error_description": "user denied the request",
	}), nil
}
