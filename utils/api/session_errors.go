package api

import "errors"

var (
	errSessionUnauthorized         = errors.New("session unauthorized")
	errSessionStoreUnavailable     = errors.New("session store unavailable")
	errIdentityProviderUnavailable = errors.New("identity provider unavailable")
	errUserStoreUnavailable        = errors.New("user store unavailable")
	errKratosIdentityUnmapped      = errors.New("kratos identity unmapped")
	errKratosSessionNotFound       = errors.New("kratos session not found")
	errKratosInvalidCredentials    = errors.New("kratos invalid credentials")
	errKratosIdentityConflict      = errors.New("kratos identity conflict")
	errKratosInvalidInput          = errors.New("kratos invalid input")
)

func IsSessionStoreUnavailableError(err error) bool {
	return errors.Is(err, errSessionStoreUnavailable)
}

func IsIdentityProviderUnavailableError(err error) bool {
	return errors.Is(err, errIdentityProviderUnavailable)
}

func IsKratosIdentityUnmappedError(err error) bool {
	return errors.Is(err, errKratosIdentityUnmapped)
}

func IsKratosSessionNotFoundError(err error) bool {
	return errors.Is(err, errKratosSessionNotFound)
}

func IsKratosInvalidCredentialsError(err error) bool {
	return errors.Is(err, errKratosInvalidCredentials)
}

func IsKratosIdentityConflictError(err error) bool {
	return errors.Is(err, errKratosIdentityConflict)
}

func IsKratosInvalidInputError(err error) bool {
	return errors.Is(err, errKratosInvalidInput)
}
