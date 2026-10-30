package upload

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/inheritvault"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailnotify"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailtemplate"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiSMTP "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"

	"github.com/gofiber/fiber/v3"
)

const reissueTimeout = 60 * time.Second

type vaultKeyView struct {
	Initialized         bool       `json:"initialized"`
	Kind                string     `json:"kind"`
	KeyVersion          int        `json:"key_version,omitempty"`
	IdentityHasPassword *bool      `json:"identity_has_password,omitempty"`
	CreatedAt           *time.Time `json:"created_at,omitempty"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty"`
}

type vaultEntryView struct {
	ID                  string     `json:"id"`
	Server              string     `json:"server"`
	GameUserIDMasked    string     `json:"game_user_id_masked"`
	JobID               string     `json:"job_id,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	CredentialExpiresAt time.Time  `json:"credential_expires_at"`
	HasCode             bool       `json:"has_code"`
	CodeIssuedAt        *time.Time `json:"code_issued_at,omitempty"`
	CodeExpiresAt       *time.Time `json:"code_expires_at,omitempty"`
	Readable            bool       `json:"readable"`
	Pending             bool       `json:"pending"`
}

type revealResponse struct {
	InheritID     string     `json:"inherit_id"`
	Password      string     `json:"password"`
	CodeExpiresAt *time.Time `json:"code_expires_at,omitempty"`
}

type reissueRequest struct {
	Password         string `json:"password"`
	PreviousPassword string `json:"previous_password"`
	VaultPIN         string `json:"vault_pin"`
	NewPasswordMode  string `json:"new_password_mode"`
}

func (r reissueRequest) input() unlockInput {
	return unlockInput{Password: r.Password, PreviousPassword: r.PreviousPassword, VaultPIN: r.VaultPIN}
}

type reissueResponse struct {
	InheritID     string       `json:"inherit_id"`
	Password      string       `json:"password"`
	CodeExpiresAt *time.Time   `json:"code_expires_at"`
	Warnings      []jobProblem `json:"warnings,omitempty"`
}

type resetRequest struct {
	Password string `json:"password"`
	Confirm  bool   `json:"confirm"`
}

type resetResponse struct {
	DestroyedEntries int `json:"destroyed_entries"`
}

func (s *inheritService) handleVaultKey(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	if !s.available() {
		return respondProblem(c, errVaultUnavailable)
	}
	ctx := c.Context()
	info, err := s.vault.KeyInfo(ctx, meta.UserID)
	if err == nil {
		return harukiAPIHelper.Responses.SuccessResponse(c, "ok", &vaultKeyView{
			Initialized: true, Kind: info.SecretKind, KeyVersion: info.KeyVersion, CreatedAt: &info.CreatedAt, UpdatedAt: &info.UpdatedAt,
		})
	}
	if !errors.Is(err, inheritvault.ErrNoKey) {
		return respondProblem(c, errStoreUnavailable)
	}
	kind, p := s.secretKindFor(ctx, meta)
	if p != nil {
		return respondProblem(c, p)
	}
	hasPassword := kind == inheritvault.SecretKindPassword
	return harukiAPIHelper.Responses.SuccessResponse(c, "ok", &vaultKeyView{Kind: kind, IdentityHasPassword: &hasPassword})
}

// handleVaultReset discards the owner's key and every entry. It is the way
// out after a password reset (the old password is gone) or a forgotten PIN.
// Owners with a Toolbox password must confirm with it; identities without
// one only have their session and an explicit confirm flag.
func (s *inheritService) handleVaultReset(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	if !s.available() {
		return respondProblem(c, errVaultUnavailable)
	}
	var req resetRequest
	if err := c.Bind().Body(&req); err != nil {
		return respondProblem(c, errInvalidRequest)
	}
	if !req.Confirm {
		return respondProblem(c, errResetConfirmation)
	}
	ctx := c.Context()
	kind, p := s.secretKindFor(ctx, meta)
	if p != nil {
		return respondProblem(c, p)
	}
	if kind == inheritvault.SecretKindPassword {
		count, err := s.api.DBManager.Redis.IncrementWithTTL(ctx, unlockAttemptPrefix+meta.UserID, unlockAttemptWindow)
		if err != nil {
			return respondProblem(c, errStoreUnavailable)
		}
		if count > unlockAttemptLimit {
			return respondProblem(c, errUnlockRateLimited)
		}
		if _, p := s.checkSecret(ctx, meta, kind, unlockInput{Password: req.Password}); p != nil {
			return respondProblem(c, p)
		}
	}
	n, err := s.vault.ResetKey(ctx, meta.UserID)
	if err != nil {
		s.writeAudit(meta, "inherit.vault.key.reset", "failure", meta.UserID, nil)
		return respondProblem(c, errStoreUnavailable)
	}
	s.writeAudit(meta, "inherit.vault.key.reset", "success", meta.UserID, map[string]any{"destroyed_entries": n})
	return harukiAPIHelper.Responses.SuccessResponse(c, "vault reset", &resetResponse{DestroyedEntries: n})
}

func (s *inheritService) handleVaultList(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	if !s.available() {
		return respondProblem(c, errVaultUnavailable)
	}
	ctx := c.Context()
	entries, err := s.vault.List(ctx, meta.UserID)
	if err != nil {
		return respondProblem(c, errStoreUnavailable)
	}
	keyVersion := 0
	if info, err := s.vault.KeyInfo(ctx, meta.UserID); err == nil {
		keyVersion = info.KeyVersion
	}
	now := s.now()
	out := make([]vaultEntryView, 0, len(entries))
	for _, e := range entries {
		v := vaultEntryView{
			ID: e.ID, Server: e.Server, GameUserIDMasked: inheritvault.MaskGameUserID(e.GameUserID), JobID: e.JobID,
			CreatedAt: e.CreatedAt, CredentialExpiresAt: e.CredentialExpiresAt, HasCode: e.CodeLive(now),
			Readable: e.KeyVersion == keyVersion, Pending: e.Pending,
		}
		if v.HasCode {
			v.CodeIssuedAt, v.CodeExpiresAt = e.CodeIssuedAt, e.CodeExpiresAt
		}
		out = append(out, v)
	}
	return harukiAPIHelper.Responses.SuccessResponse(c, "ok", &out)
}

// openEntry runs the step-up unlock and loads the owner's entry.
func (s *inheritService) openEntry(ctx context.Context, meta requestMeta, entryID string, in unlockInput) (*inheritvault.Unlocked, *inheritvault.Entry, *apiProblem) {
	if !s.available() {
		return nil, nil, errVaultUnavailable
	}
	entry, err := s.vault.Get(ctx, meta.UserID, entryID)
	if errors.Is(err, inheritvault.ErrNotFound) {
		return nil, nil, errEntryNotFound
	}
	if err != nil {
		return nil, nil, errStoreUnavailable
	}
	unlocked, _, p := s.unlockVault(ctx, meta, in, false)
	if p != nil {
		return nil, nil, p
	}
	if entry.KeyVersion != unlocked.KeyVersion {
		unlocked.Close()
		return nil, nil, errEntryStale
	}
	return unlocked, entry, nil
}

func (s *inheritService) handleVaultReveal(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	var body unlockInput
	if err := c.Bind().Body(&body); err != nil {
		return respondProblem(c, errInvalidRequest)
	}
	entryID := strings.Clone(c.Params("entry_id"))
	unlocked, entry, p := s.openEntry(c.Context(), meta, entryID, body)
	if p != nil {
		s.writeAudit(meta, "inherit.vault.reveal", "failure", entryID, map[string]any{"reason": p.Code})
		return respondProblem(c, p)
	}
	defer unlocked.Close()
	if !entry.CodeLive(s.now()) {
		return respondProblem(c, errCodeNotAvailable)
	}
	inheritID, password, err := unlocked.OpenCode(entry)
	if err != nil {
		s.writeAudit(meta, "inherit.vault.reveal", "failure", entryID, map[string]any{"reason": "decrypt_failed"})
		return respondProblem(c, errInternal)
	}
	s.writeAudit(meta, "inherit.vault.reveal", "success", entryID, s.entryAuditMeta(entry))
	s.notify(meta, mailtemplate.InheritCodeRevealed, entry)
	return harukiAPIHelper.Responses.SuccessResponse(c, "ok", &revealResponse{InheritID: inheritID, Password: password, CodeExpiresAt: entry.CodeExpiresAt})
}

// handleVaultReissue logs in with the stored credential and issues a new
// transfer code, which replaces the stored one (the game invalidates the
// previous code).
func (s *inheritService) handleVaultReissue(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	var req reissueRequest
	if err := c.Bind().Body(&req); err != nil {
		return respondProblem(c, errInvalidRequest)
	}
	if req.NewPasswordMode != newPasswordModeReuse && req.NewPasswordMode != newPasswordModeRandom {
		return respondProblem(c, errNewPwModeInvalid)
	}
	entryID := strings.Clone(c.Params("entry_id"))
	fail := func(p *apiProblem) error {
		s.writeAudit(meta, "inherit.vault.reissue", "failure", entryID, map[string]any{"reason": p.Code})
		return respondProblem(c, p)
	}
	unlocked, entry, p := s.openEntry(c.Context(), meta, entryID, req.input())
	if p != nil {
		return fail(p)
	}
	credential, err := unlocked.OpenCredential(entry)
	if err != nil {
		unlocked.Close()
		return fail(errInternal)
	}
	defer clear(credential)
	newPassword := randomInheritPassword()
	if req.NewPasswordMode == newPasswordModeReuse {
		_, previous, err := unlocked.OpenCode(entry)
		if err != nil || !validInheritPassword(previous) {
			unlocked.Close()
			return fail(errNoPreviousPassword)
		}
		newPassword = previous
	}
	publicKey, keyVersion := append([]byte(nil), unlocked.PublicKey...), unlocked.KeyVersion
	unlocked.Close()

	server := harukiUtils.SupportedInheritUploadServer(entry.Server)
	allowed, retryAfter, token := s.breaker.Allow(server)
	if !allowed {
		c.Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
		return fail(errGameDegraded)
	}
	if !s.limiter.acquire(server) {
		s.breaker.ReleaseProbe(server, token)
		return fail(errServerBusy)
	}
	ctx, cancel := context.WithTimeout(context.Background(), reissueTimeout)
	defer cancel()
	inheritID, p, gameErr := s.reissue(ctx, server, entry.GameUserID, string(credential), newPassword)
	s.limiter.release(server)
	s.breaker.RecordResult(server, token, inheritFailureIsUpstreamDegradation(gameErr))
	if p != nil {
		return fail(p)
	}
	expires := s.now().Add(inheritvault.CodeLifetime)
	resp := &reissueResponse{InheritID: inheritID, Password: newPassword, CodeExpiresAt: &expires}
	loc, err := s.vault.PutCode(context.WithoutCancel(c.Context()), inheritvault.CodeInput{
		EntryID: entry.ID, UserID: meta.UserID, Server: entry.Server, KeyVersion: keyVersion, PublicKey: publicKey,
		InheritID: inheritID, Password: newPassword,
	})
	switch {
	case err != nil:
		resp.Warnings = append(resp.Warnings, *jobProblemFor(codeVaultStoreFailed))
	case loc == inheritvault.LocationFallback:
		resp.Warnings = append(resp.Warnings, *jobProblemFor(codeVaultFallback))
	}
	s.writeAudit(meta, "inherit.vault.reissue", "success", entryID, s.entryAuditMeta(entry))
	s.notify(meta, mailtemplate.InheritCodeReissued, entry)
	return harukiAPIHelper.Responses.SuccessResponse(c, "reissued", resp)
}

// reissue logs in with a stored credential and issues a new code. The error
// it returns is the game failure, for the circuit breaker.
func (s *inheritService) reissue(ctx context.Context, server harukiUtils.SupportedInheritUploadServer, gameUserID int64, credential, password string) (string, *apiProblem, error) {
	game, err := s.newGame(server, nil)
	if err != nil {
		return "", errInternal, nil
	}
	if err := game.Bootstrap(ctx); err != nil {
		return "", errReissueFailed, err
	}
	if err := game.Login(ctx, gameUserID, credential); err != nil {
		if code := gameFailureCode(err); code == codeInheritInvalid || code == codeGameError {
			return "", errReissueLoginFailed, err
		}
		return "", errReissueFailed, err
	}
	id, err := s.issueWithRetry(ctx, game, password)
	if err != nil {
		if gameFailureCode(err) == codeGameError {
			return "", errReissueInheritInvalid, err
		}
		return "", errReissueFailed, err
	}
	return id, nil, nil
}

func (s *inheritService) handleVaultDestroy(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	if !s.available() {
		return respondProblem(c, errVaultUnavailable)
	}
	entryID := strings.Clone(c.Params("entry_id"))
	err := s.vault.Destroy(c.Context(), meta.UserID, entryID)
	if errors.Is(err, inheritvault.ErrNotFound) {
		return respondProblem(c, errEntryNotFound)
	}
	if err != nil {
		s.writeAudit(meta, "inherit.vault.destroy", "failure", entryID, nil)
		return respondProblem(c, errStoreUnavailable)
	}
	s.writeAudit(meta, "inherit.vault.destroy", "success", entryID, nil)
	return harukiAPIHelper.Responses.SuccessResponse[string](c, "destroyed", nil)
}

func (s *inheritService) entryAuditMeta(e *inheritvault.Entry) map[string]any {
	return map[string]any{"server": e.Server, "game_user_id_masked": inheritvault.MaskGameUserID(e.GameUserID)}
}

// notify mails the owner when a stored code is shown or replaced, if their
// email is verified. The mail names the action, time, server, masked game
// user ID and IP only; it never carries the code or any credential. Delivery
// is best effort and never delays the response.
func (s *inheritService) notify(meta requestMeta, compose func(mailtemplate.InheritVaultActivity) mailtemplate.Email, e *inheritvault.Entry) {
	if !meta.EmailVerified || s.sendMail == nil {
		return
	}
	activity := mailtemplate.InheritVaultActivity{
		Server:           e.Server,
		MaskedGameUserID: inheritvault.MaskGameUserID(e.GameUserID),
		IP:               meta.IP,
		At:               s.now(),
	}
	userID := meta.UserID
	mailnotify.Dispatch(func(ctx context.Context) {
		email, _, _ := s.userEmail(ctx, userID)
		if email == "" || !strings.Contains(email, "@") {
			return
		}
		msg, err := mailtemplate.Render(compose(activity))
		if err != nil {
			s.logger.Warnf("Inherit vault notification mail could not be rendered: %v", err)
			return
		}
		s.sendMail(email, msg)
	})
}

func (s *inheritService) sendSMTP(to string, msg harukiSMTP.Message) {
	if s.api == nil || s.api.SMTPClient == nil {
		return
	}
	if err := s.api.SMTPClient.SendMessage([]string{to}, msg, ""); err != nil {
		s.logger.Warnf("Inherit vault notification mail failed: %v", err)
	}
}
