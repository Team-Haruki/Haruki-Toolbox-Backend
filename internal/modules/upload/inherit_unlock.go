package upload

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/inheritvault"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

const (
	unlockAttemptLimit  = 10
	unlockAttemptWindow = 15 * time.Minute
	unlockAttemptPrefix = "inherit:vault:unlock:"
	vaultPINMinLen      = 8
	vaultPINMaxLen      = 128
)

// unlockInput is the step-up secret every vault operation carries. Owners
// whose Kratos identity has a password send it as password; identities
// without one (social or passkey sign-in) use a vault PIN instead.
// previous_password carries a key over after the Toolbox password changed.
type unlockInput struct {
	Password         string `json:"password"`
	PreviousPassword string `json:"previous_password"`
	VaultPIN         string `json:"vault_pin"`
}

// unlockResult says what unlocking had to do besides opening the key.
type unlockResult struct {
	created   bool
	rewrapped bool
}

// unlockVault is the step-up check of the inherit vault. It verifies the
// owner's secret and returns their private key in memory (Close it). When
// allowCreate is set and the owner has no key yet, the same input creates
// one. Failed and successful attempts alike count against a per-user limit;
// a success resets it.
func (s *inheritService) unlockVault(ctx context.Context, meta requestMeta, in unlockInput, allowCreate bool) (*inheritvault.Unlocked, unlockResult, *apiProblem) {
	var res unlockResult
	if !s.available() {
		return nil, res, errVaultUnavailable
	}
	attemptKey := unlockAttemptPrefix + meta.UserID
	count, err := s.api.DBManager.Redis.IncrementWithTTL(ctx, attemptKey, unlockAttemptWindow)
	if err != nil {
		return nil, res, errStoreUnavailable
	}
	if count > unlockAttemptLimit {
		return nil, res, errUnlockRateLimited
	}
	unlocked, res, p := s.unlockOrCreate(ctx, meta, in, allowCreate)
	if p == nil {
		_ = s.redis.Del(ctx, attemptKey).Err()
	}
	return unlocked, res, p
}

func (s *inheritService) unlockOrCreate(ctx context.Context, meta requestMeta, in unlockInput, allowCreate bool) (*inheritvault.Unlocked, unlockResult, *apiProblem) {
	var res unlockResult
	info, err := s.vault.KeyInfo(ctx, meta.UserID)
	if errors.Is(err, inheritvault.ErrNoKey) {
		if !allowCreate {
			return nil, res, errVaultNotInitialized
		}
		kind, p := s.secretKindFor(ctx, meta)
		if p != nil {
			return nil, res, p
		}
		secret, p := s.checkSecret(ctx, meta, kind, in)
		if p != nil {
			return nil, res, p
		}
		u, err := s.vault.CreateKey(ctx, meta.UserID, kind, []byte(secret))
		if err != nil {
			return nil, res, errInternal
		}
		res.created = true
		s.writeAudit(meta, "inherit.vault.key.create", "success", meta.UserID, map[string]any{"secret_kind": kind})
		return u, res, nil
	}
	if err != nil {
		return nil, res, errStoreUnavailable
	}
	secret, p := s.checkSecret(ctx, meta, info.SecretKind, in)
	if p != nil {
		return nil, res, p
	}
	u, err := s.vault.Unlock(ctx, meta.UserID, []byte(secret))
	switch {
	case err == nil:
		return u, res, nil
	case !errors.Is(err, inheritvault.ErrDecrypt):
		return nil, res, errInternal
	case info.SecretKind == inheritvault.SecretKindPIN:
		return nil, res, errPINInvalid
	case in.PreviousPassword == "":
		// Kratos accepted the password but it does not open the key: the
		// password changed since the key was made.
		return nil, res, errVaultPasswordChanged
	}
	u, err = s.vault.Rewrap(ctx, meta.UserID, []byte(in.PreviousPassword), []byte(secret))
	if errors.Is(err, inheritvault.ErrDecrypt) {
		return nil, res, errPreviousPwInvalid
	}
	if err != nil {
		return nil, res, errInternal
	}
	res.rewrapped = true
	s.writeAudit(meta, "inherit.vault.key.rewrap", "success", meta.UserID, nil)
	return u, res, nil
}

// secretKindFor picks the secret a new key is wrapped with.
func (s *inheritService) secretKindFor(ctx context.Context, meta requestMeta) (string, *apiProblem) {
	_, identityID, err := s.userEmail(ctx, meta.UserID)
	if err != nil {
		return "", errIdentityUnavailable
	}
	hasPassword, err := s.hasPassword(meta.context(ctx), identityID)
	if err != nil {
		return "", errIdentityUnavailable
	}
	if hasPassword {
		return inheritvault.SecretKindPassword, nil
	}
	return inheritvault.SecretKindPIN, nil
}

// checkSecret validates the input for kind. A password must be accepted by
// Kratos (that is the step-up); a PIN is checked by opening the key.
func (s *inheritService) checkSecret(ctx context.Context, meta requestMeta, kind string, in unlockInput) (string, *apiProblem) {
	if kind == inheritvault.SecretKindPIN {
		if in.VaultPIN == "" {
			return "", errPINRequired
		}
		if n := utf8.RuneCountInString(in.VaultPIN); n < vaultPINMinLen || n > vaultPINMaxLen {
			return "", errPINFormat
		}
		return in.VaultPIN, nil
	}
	if in.Password == "" {
		return "", errPasswordRequired
	}
	_, identityID, err := s.userEmail(ctx, meta.UserID)
	if err != nil {
		return "", errIdentityUnavailable
	}
	if err := s.verifyPassword(meta.context(ctx), identityID, in.Password); err != nil {
		if errors.Is(err, errPasswordRejected) {
			return "", errPasswordInvalid
		}
		return "", errIdentityUnavailable
	}
	return in.Password, nil
}

// unlockHint tells a new job which secret its confirm step will need.
func (s *inheritService) unlockHint(ctx context.Context, meta requestMeta) (*jobUnlock, *apiProblem) {
	info, err := s.vault.KeyInfo(ctx, meta.UserID)
	if err == nil {
		return &jobUnlock{Kind: info.SecretKind, Initialized: true}, nil
	}
	if !errors.Is(err, inheritvault.ErrNoKey) {
		return nil, errStoreUnavailable
	}
	kind, p := s.secretKindFor(ctx, meta)
	if p != nil {
		return nil, p
	}
	return &jobUnlock{Kind: kind, Initialized: false}, nil
}

// errPasswordRejected means Kratos refused the password (as opposed to
// Kratos being unreachable).
var errPasswordRejected = errors.New("password rejected")

func (s *inheritService) verifyKratosPassword(ctx context.Context, identityID, password string) error {
	if s.api == nil || s.api.SessionHandler == nil || !s.api.SessionHandler.UsesKratosProvider() {
		return errors.New("kratos is not configured")
	}
	err := s.api.SessionHandler.VerifyKratosPasswordByIdentityID(ctx, identityID, password)
	if harukiAPIHelper.IsKratosInvalidCredentialsError(err) || harukiAPIHelper.IsKratosInvalidInputError(err) {
		return errPasswordRejected
	}
	return err
}

func (s *inheritService) kratosHasPassword(ctx context.Context, identityID string) (bool, error) {
	if s.api == nil || s.api.SessionHandler == nil || !s.api.SessionHandler.UsesKratosProvider() {
		return false, errors.New("kratos is not configured")
	}
	return s.api.SessionHandler.KratosIdentityHasPassword(ctx, identityID)
}

func (s *inheritService) queryUserEmail(ctx context.Context, userID string) (string, string, error) {
	u, err := s.api.DBManager.DB.User.Query().Where(userSchema.IDEQ(userID)).Only(ctx)
	if err != nil {
		return "", "", err
	}
	identityID := ""
	if u.KratosIdentityID != nil {
		identityID = strings.TrimSpace(*u.KratosIdentityID)
	}
	if identityID == "" {
		return u.Email, "", errors.New("identity not linked")
	}
	return u.Email, identityID, nil
}
