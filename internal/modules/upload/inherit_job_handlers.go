package upload

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	usercore "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/socialplatforminfo"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"

	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

const (
	newPasswordModeReuse  = "reuse"
	newPasswordModeRandom = "random"

	inheritPasswordMinLen = 8
	inheritPasswordMaxLen = 16
	inheritInputMaxLen    = 64

	wsTicketTTL    = 30 * time.Second
	wsTicketPrefix = "inherit:ws-ticket:"
)

var inheritIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{16}$`)

type createJobRequest struct {
	Server          string `json:"server"`
	UploadType      string `json:"upload_type"`
	InheritID       string `json:"inherit_id"`
	InheritPassword string `json:"inherit_password"`
	NewPasswordMode string `json:"new_password_mode"`
}

type createJobResponse struct {
	JobID string   `json:"job_id"`
	Job   *jobView `json:"job"`
}

// validateCreateJob checks everything that can be checked without the game
// server, so a malformed request never reaches it.
func validateCreateJob(req *createJobRequest) *apiProblem {
	if _, err := harukiUtils.ParseSupportedInheritUploadServer(req.Server); err != nil {
		return errInvalidServer
	}
	switch harukiUtils.UploadDataType(req.UploadType) {
	case harukiUtils.UploadDataTypeSuite, harukiUtils.UploadDataTypeMysekai:
	default:
		return errInvalidUploadType
	}
	req.InheritID = strings.TrimSpace(req.InheritID)
	if !inheritIDPattern.MatchString(req.InheritID) {
		return errInheritIDInvalid
	}
	if req.InheritPassword == "" || len(req.InheritPassword) > inheritInputMaxLen {
		return errInheritPwInvalid
	}
	switch req.NewPasswordMode {
	case newPasswordModeReuse:
		if !validInheritPassword(req.InheritPassword) {
			return errReusePasswordInvalid
		}
	case newPasswordModeRandom:
	default:
		return errNewPwModeInvalid
	}
	return nil
}

// validInheritPassword is what the game accepts for a new code: 8 to 16
// printable ASCII characters.
func validInheritPassword(pw string) bool {
	if len(pw) < inheritPasswordMinLen || len(pw) > inheritPasswordMaxLen {
		return false
	}
	for i := 0; i < len(pw); i++ {
		if pw[i] < 0x21 || pw[i] > 0x7e {
			return false
		}
	}
	return true
}

func captureMeta(c fiber.Ctx) (requestMeta, *apiProblem) {
	userID, err := usercore.CurrentUserID(c)
	if err != nil {
		return requestMeta{}, errUnauthenticated
	}
	role, _ := c.Locals("userRole").(string)
	return requestMeta{
		UserID:        strings.Clone(userID),
		Role:          strings.Clone(role),
		IP:            strings.Clone(c.IP()),
		UserAgent:     strings.Clone(c.Get("User-Agent")),
		Path:          strings.Clone(c.Path()),
		Method:        strings.Clone(c.Method()),
		EmailVerified: usercore.IsCurrentUserEmailVerified(c),
	}, nil
}

func (s *inheritService) handleCreateJob(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	if !s.available() {
		return respondProblem(c, errVaultUnavailable)
	}
	var req createJobRequest
	if err := c.Bind().Body(&req); err != nil {
		return respondProblem(c, errInvalidRequest)
	}
	if p := validateCreateJob(&req); p != nil {
		return respondProblem(c, p)
	}
	ctx := c.Context()
	if req.UploadType == string(harukiUtils.UploadDataTypeMysekai) {
		ok, err := s.hasVerifiedQQ(ctx, meta.UserID)
		if err != nil {
			return respondProblem(c, errStoreUnavailable)
		}
		if !ok {
			return respondProblem(c, errMysekaiRequiresQQ)
		}
	}
	hint, p := s.unlockHint(ctx, meta)
	if p != nil {
		return respondProblem(c, p)
	}
	rate, err := s.api.DBManager.Redis.IncrementWithTTL(ctx, inheritRateKeyPrefix+meta.UserID+":"+strconv.FormatInt(s.now().Unix()/3600, 10), 2*time.Hour)
	if err != nil {
		return respondProblem(c, errStoreUnavailable)
	}
	if rate > inheritJobsPerUserHour {
		return respondProblem(c, errJobRateLimited)
	}

	server := harukiUtils.SupportedInheritUploadServer(req.Server)
	allowed, retryAfter, breakerToken := s.breaker.Allow(server)
	if !allowed {
		c.Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
		return respondProblem(c, errGameDegraded)
	}
	if !s.limiter.acquire(server) {
		s.breaker.ReleaseProbe(server, breakerToken)
		c.Set("Retry-After", strconv.Itoa(retryAfterSeconds(inheritBreakerRetryAfterFloor)))
		return respondProblem(c, errServerBusy)
	}
	admitted := false
	defer func() {
		if !admitted {
			s.limiter.release(server)
			s.breaker.ReleaseProbe(server, breakerToken)
		}
	}()

	job := newInheritJob(meta.UserID, req.Server, req.UploadType, req.NewPasswordMode, s.now())
	job.Unlock = hint
	if p := s.claimActive(ctx, meta.UserID, job.ID); p != nil {
		return respondProblem(c, p)
	}
	if err := s.jobs.save(ctx, job); err != nil {
		s.releaseActive(meta.UserID, job.ID)
		return respondProblem(c, errStoreUnavailable)
	}
	rt := &jobRuntime{
		job:       job,
		code:      harukiSekai.InheritCode{InheritID: strings.Clone(req.InheritID), Password: strings.Clone(req.InheritPassword)},
		confirmCh: make(chan confirmation, 1),
		cancelCh:  make(chan struct{}, 1),
	}
	s.registry.add(rt)
	if !startBackgroundTask(s.runner, s.logger, "inherit-job", func() { s.run(rt, breakerToken) }) {
		s.registry.remove(job.ID)
		s.releaseActive(meta.UserID, job.ID)
		return respondProblem(c, errVaultUnavailable)
	}
	admitted = true
	s.writeAudit(meta, "inherit.job.create", "success", job.ID, map[string]any{
		"server": req.Server, "upload_type": req.UploadType, "new_password_mode": req.NewPasswordMode,
	})
	rt.mu.Lock()
	view := s.view(rt.job.clone())
	rt.mu.Unlock()
	return harukiAPIHelper.Responses.UpdatedDataResponse(c, fiber.StatusAccepted, "job created", &createJobResponse{JobID: job.ID, Job: view})
}

// claimActive takes the owner's one-active-job lock. A lock left by a job
// that has finished, expired, or belonged to a process that has since
// stopped is taken over.
func (s *inheritService) claimActive(ctx context.Context, userID, jobID string) *apiProblem {
	key := inheritActiveKeyPrefix + userID
	acquired, err := s.redis.SetNX(ctx, key, jobID, inheritActiveLockTTL).Result()
	if err != nil {
		return errStoreUnavailable
	}
	if acquired {
		return nil
	}
	holder, err := s.redis.Get(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return errStoreUnavailable
	}
	if holder != "" && s.registry.get(holder) != nil {
		return errJobAlreadyRunning
	}
	if previous, err := s.jobs.load(ctx, holder); err == nil && previous != nil && !previous.Status.terminal() && previous.Instance == inheritInstanceID {
		return errJobAlreadyRunning
	}
	if err := s.redis.Set(ctx, key, jobID, inheritActiveLockTTL).Err(); err != nil {
		return errStoreUnavailable
	}
	return nil
}

func (s *inheritService) releaseActive(userID, jobID string) {
	if s.redis == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := inheritActiveKeyPrefix + userID
	if current, err := s.redis.Get(ctx, key).Result(); err == nil && current == jobID {
		_ = s.redis.Del(ctx, key).Err()
	}
}

func (s *inheritService) queryVerifiedQQ(ctx context.Context, userID string) (bool, error) {
	return s.api.DBManager.DB.SocialPlatformInfo.Query().
		Where(
			socialplatforminfo.UserSocialPlatformInfoEQ(userID),
			socialplatforminfo.PlatformEQ(string(harukiAPIHelper.SocialPlatformQQ)),
			socialplatforminfo.Verified(true),
		).Exist(ctx)
}

func (s *inheritService) handleGetJob(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	job, _, p := s.loadJob(c.Context(), meta.UserID, c.Params("job_id"))
	if p != nil {
		return respondProblem(c, p)
	}
	return harukiAPIHelper.Responses.SuccessResponse(c, "ok", s.view(job))
}

// confirm is the confirm step: the step-up unlock (which also creates the
// owner's key on first use), then the public key goes to the waiting job.
func (s *inheritService) confirm(ctx context.Context, meta requestMeta, jobID string, in unlockInput) (*jobView, *apiProblem) {
	_, rt, p := s.loadJob(ctx, meta.UserID, jobID)
	if p != nil {
		return nil, p
	}
	if rt == nil || !s.awaiting(rt) {
		return nil, errJobNotAwaiting
	}
	unlocked, res, p := s.unlockVault(ctx, meta, in, true)
	if p != nil {
		return nil, p
	}
	conf := confirmation{publicKey: append([]byte(nil), unlocked.PublicKey...), keyVersion: unlocked.KeyVersion}
	unlocked.Close()

	rt.mu.Lock()
	if rt.cancelled || rt.confirmed || rt.job.Status != jobStatusAwaitingConfirm {
		rt.mu.Unlock()
		return nil, errJobNotAwaiting
	}
	rt.confirmed = true
	rt.confirmCh <- conf
	if res.created {
		rt.job.Unlock = &jobUnlock{Kind: rt.job.Unlock.Kind, Initialized: true}
	}
	view := s.view(rt.job.clone())
	rt.mu.Unlock()
	s.writeAudit(meta, "inherit.job.confirm", "success", jobID, map[string]any{"key_created": res.created, "key_rewrapped": res.rewrapped})
	return view, nil
}

func (s *inheritService) awaiting(rt *jobRuntime) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.job.Status == jobStatusAwaitingConfirm && !rt.confirmed && !rt.cancelled
}

// cancel stops a job that has not been confirmed yet; nothing has been
// executed then, so the code stays valid.
func (s *inheritService) cancel(ctx context.Context, meta requestMeta, jobID string) (*jobView, *apiProblem) {
	job, rt, p := s.loadJob(ctx, meta.UserID, jobID)
	if p != nil {
		return nil, p
	}
	if rt == nil || job.Status.terminal() {
		return nil, errCannotCancel
	}
	rt.mu.Lock()
	if rt.confirmed {
		rt.mu.Unlock()
		return nil, errCannotCancel
	}
	rt.cancelled = true
	stop := rt.stopPhase
	select {
	case rt.cancelCh <- struct{}{}:
	default:
	}
	view := s.view(rt.job.clone())
	rt.mu.Unlock()
	if stop != nil {
		stop()
	}
	s.writeAudit(meta, "inherit.job.cancel", "success", jobID, nil)
	return view, nil
}

func (s *inheritService) handleConfirm(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	var in unlockInput
	if err := c.Bind().Body(&in); err != nil {
		return respondProblem(c, errInvalidRequest)
	}
	view, p := s.confirm(c.Context(), meta, c.Params("job_id"), in)
	if p != nil {
		return respondProblem(c, p)
	}
	return harukiAPIHelper.Responses.UpdatedDataResponse(c, fiber.StatusAccepted, "confirmed", view)
}

func (s *inheritService) handleCancel(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	view, p := s.cancel(c.Context(), meta, c.Params("job_id"))
	if p != nil {
		return respondProblem(c, p)
	}
	return harukiAPIHelper.Responses.SuccessResponse(c, "cancel requested", view)
}

type wsTicketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expires_in"`
}

func wsTicketKey(ticket string) string {
	sum := sha256.Sum256([]byte(ticket))
	return wsTicketPrefix + hex.EncodeToString(sum[:])
}

// handleWSTicket issues a one-time ticket for the job's WebSocket. Browsers
// cannot set headers on a WebSocket, and cookies alone would let any site
// open one in the owner's name; the ticket can only be read by a same-origin
// authenticated request.
func (s *inheritService) handleWSTicket(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	jobID := c.Params("job_id")
	if _, _, p := s.loadJob(c.Context(), meta.UserID, jobID); p != nil {
		return respondProblem(c, p)
	}
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	if err := s.redis.Set(c.Context(), wsTicketKey(ticket), meta.UserID+"|"+jobID, wsTicketTTL).Err(); err != nil {
		return respondProblem(c, errStoreUnavailable)
	}
	return harukiAPIHelper.Responses.SuccessResponse(c, "ok", &wsTicketResponse{Ticket: ticket, ExpiresIn: int(wsTicketTTL / time.Second)})
}

// redeemWSTicket consumes a ticket; it must have been issued to userID for
// jobID.
func (s *inheritService) redeemWSTicket(ctx context.Context, ticket, userID, jobID string) bool {
	if ticket == "" || len(ticket) > 128 {
		return false
	}
	value, err := s.redis.GetDel(ctx, wsTicketKey(ticket)).Result()
	if errors.Is(err, redis.Nil) || err != nil {
		return false
	}
	return value == userID+"|"+jobID
}
