package upload

import (
	"context"
	"strings"
	"time"

	json "encoding/json/v2"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/inheritvault"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/redis/go-redis/v9"
)

// inheritGame is the part of harukiSekai.InheritSession a job drives; tests
// replace it with a fake game.
type inheritGame interface {
	Bootstrap(ctx context.Context) error
	Preview(ctx context.Context, code harukiSekai.InheritCode) (*harukiSekai.InheritPreview, error)
	Execute(ctx context.Context, code harukiSekai.InheritCode) (string, error)
	Login(ctx context.Context, userID int64, credential string) error
	IssueInheritCode(ctx context.Context, password string) (string, error)
	FetchSuite(ctx context.Context) ([]byte, error)
	FetchMysekai(ctx context.Context) ([]byte, error)
	RedactError(err error) error
}

// inheritService runs inherit jobs and serves the vault.
type inheritService struct {
	api      *harukiAPIHelper.HarukiToolboxRouterHelpers
	deps     Dependencies
	vault    *inheritvault.Store
	jobs     jobStore
	redis    *redis.Client
	registry *jobRegistry
	logger   *harukiLogger.Logger
	runner   harukiBackground.Runner
	limiter  *inheritConcurrencyLimiter
	breaker  *inheritCircuitBreaker

	now            func() time.Time
	confirmTimeout time.Duration
	phaseTimeout   time.Duration
	slotWait       time.Duration
	issueBackoff   func(attempt int) time.Duration

	newGame        func(server harukiUtils.SupportedInheritUploadServer, progress harukiSekai.Progress) (inheritGame, error)
	upload         func(ctx context.Context, job *inheritJob, gameUserID int64, suite, mysekai []byte) error
	verifyPassword func(ctx context.Context, identityID, password string) error
	hasPassword    func(ctx context.Context, identityID string) (bool, error)
	hasVerifiedQQ  func(ctx context.Context, userID string) (bool, error)
	userEmail      func(ctx context.Context, userID string) (email, identityID string, err error)
	sendMail       func(to, subject, body string)
}

func newInheritService(api *harukiAPIHelper.HarukiToolboxRouterHelpers, deps Dependencies) *inheritService {
	s := &inheritService{
		api:            api,
		deps:           deps,
		vault:          deps.InheritVault,
		registry:       newJobRegistry(),
		logger:         harukiLogger.NewLoggerFromGlobal("InheritJob"),
		runner:         deps.BackgroundTasks,
		limiter:        inheritLimiter,
		breaker:        inheritBreaker,
		now:            func() time.Time { return time.Now().UTC() },
		confirmTimeout: inheritConfirmTimeout,
		phaseTimeout:   inheritPhaseTimeout,
		slotWait:       inheritSlotWait,
		issueBackoff:   func(attempt int) time.Duration { return time.Duration(attempt) * time.Second },
	}
	if api != nil && api.DBManager != nil && api.DBManager.Redis != nil {
		s.redis = api.DBManager.Redis.Redis
	}
	s.jobs = jobStore{redis: s.redis}
	s.newGame = func(server harukiUtils.SupportedInheritUploadServer, progress harukiSekai.Progress) (inheritGame, error) {
		cfg, err := harukiSekai.NewInheritSessionConfig(deps.SekaiClient, deps.Proxy, server, deps.ServerCryptor, deps.InheritPacing, progress)
		if err != nil {
			return nil, err
		}
		return harukiSekai.NewInheritSession(cfg)
	}
	s.upload = s.uploadData
	s.verifyPassword = s.verifyKratosPassword
	s.hasPassword = s.kratosHasPassword
	s.hasVerifiedQQ = s.queryVerifiedQQ
	s.userEmail = s.queryUserEmail
	s.sendMail = s.sendSMTP
	return s
}

func (s *inheritService) available() bool {
	return s != nil && s.vault.Available() && s.redis != nil
}

// requestMeta is what a handler captures from the request before any work
// that may outlive it (a WebSocket session, a background job). Strings are
// cloned because Fiber reuses request buffers.
type requestMeta struct {
	UserID        string
	Role          string
	IP            string
	UserAgent     string
	Path          string
	Method        string
	EmailVerified bool
}

func (m requestMeta) context(ctx context.Context) context.Context {
	return harukiAPIHelper.WithHTTPRequestMetadata(ctx, m.UserAgent, m.IP)
}

// jobView is the job as the owner sees it.
type jobView struct {
	ID              string           `json:"id"`
	Server          string           `json:"server"`
	UploadType      string           `json:"upload_type"`
	NewPasswordMode string           `json:"new_password_mode"`
	Status          inheritJobStatus `json:"status"`
	CurrentStep     string           `json:"current_step"`
	Steps           []jobStep        `json:"steps"`
	Preview         *jobPreview      `json:"preview,omitempty"`
	Unlock          *jobUnlock       `json:"unlock,omitempty"`
	Error           *jobProblem      `json:"error,omitempty"`
	Warnings        []jobProblem     `json:"warnings,omitempty"`
	ConfirmDeadline *time.Time       `json:"confirm_deadline,omitempty"`
	VaultEntryID    string           `json:"vault_entry_id,omitempty"`
	NewCode         *newCodeView     `json:"new_code,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	FinishedAt      *time.Time       `json:"finished_at,omitempty"`
}

type newCodeView struct {
	InheritID string     `json:"inherit_id"`
	Password  string     `json:"password"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type liveCode struct {
	InheritID string `json:"inherit_id"`
	Password  string `json:"password"`
}

// view renders the job for its owner, opening the live code (sealed under
// the master key, bound to this job and owner) while the record exists.
func (s *inheritService) view(job *inheritJob) *jobView {
	v := &jobView{
		ID: job.ID, Server: job.Server, UploadType: job.UploadType, NewPasswordMode: job.NewPasswordMode,
		Status: job.Status, CurrentStep: job.CurrentStep, Steps: job.Steps, Preview: job.Preview, Unlock: job.Unlock,
		Error: job.Error, Warnings: job.Warnings, ConfirmDeadline: job.ConfirmDeadline, VaultEntryID: job.VaultEntryID,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, FinishedAt: job.FinishedAt,
	}
	if len(job.LiveCode) > 0 && s.vault.Master() != nil {
		if plain, err := s.vault.Master().OpenLive(job.LiveCode, job.ID, job.OwnerUserID); err == nil {
			var code liveCode
			if json.Unmarshal(plain, &code) == nil {
				v.NewCode = &newCodeView{InheritID: code.InheritID, Password: code.Password, ExpiresAt: job.CodeExpiresAt}
			}
			clear(plain)
		}
	}
	return v
}

// wsMessage is every server-to-client WebSocket frame: "snapshot" (on
// connect), "event" (a step or sub-step changed; job is the new snapshot),
// "ack" / "error" (answers to confirm and cancel) and "ping".
type wsMessage struct {
	Type    string     `json:"type"`
	Job     *jobView   `json:"job,omitempty"`
	Event   *jobEvent  `json:"event,omitempty"`
	Action  string     `json:"action,omitempty"`
	Code    string     `json:"code,omitempty"`
	Message string     `json:"message,omitempty"`
	At      *time.Time `json:"at,omitempty"`
}

// jobEvent is one step (or sub-step) transition.
type jobEvent struct {
	Step    string     `json:"step"`
	Substep string     `json:"substep,omitempty"`
	Status  string     `json:"status"`
	Note    string     `json:"note,omitempty"`
	Code    string     `json:"error_code,omitempty"`
	At      *time.Time `json:"at"`
}

// update changes the job under its lock, then stores it and pushes the event
// and the new snapshot to every WebSocket subscriber.
func (s *inheritService) update(rt *jobRuntime, event *jobEvent, change func(*inheritJob)) {
	rt.mu.Lock()
	change(rt.job)
	rt.job.UpdatedAt = s.now()
	snap := rt.job.clone()
	if msg, err := json.Marshal(wsMessage{Type: "event", Event: event, Job: s.view(snap)}); err == nil {
		rt.publishLocked(msg)
	}
	rt.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.jobs.save(ctx, snap)
}

func (s *inheritService) setStep(rt *jobRuntime, name string, status stepStatus, code string) {
	now := s.now()
	s.update(rt, &jobEvent{Step: name, Status: string(status), Code: code, At: &now}, func(j *inheritJob) {
		st := j.step(name)
		if st == nil {
			return
		}
		st.Status = status
		switch status {
		case stepRunning:
			st.StartedAt = &now
			j.CurrentStep = name
		case stepOK, stepFailed, stepSkipped:
			if st.StartedAt == nil && status != stepSkipped {
				st.StartedAt = &now
			}
			st.FinishedAt = &now
			st.ErrorCode = code
		}
	})
}

// progressFor turns session sub-step reports into job events on the step
// that is running.
func (s *inheritService) progressFor(rt *jobRuntime) harukiSekai.Progress {
	return func(e harukiSekai.ProgressEvent) {
		now := s.now()
		rt.mu.Lock()
		step := rt.job.CurrentStep
		rt.mu.Unlock()
		s.update(rt, &jobEvent{Step: step, Substep: e.Step, Status: string(e.Status), Note: e.Note, At: &now}, func(j *inheritJob) {
			st := j.step(step)
			if st == nil {
				return
			}
			for i := range st.Substeps {
				if st.Substeps[i].Name == e.Step {
					st.Substeps[i].Status, st.Substeps[i].Note, st.Substeps[i].At = string(e.Status), e.Note, &now
					return
				}
			}
			st.Substeps = append(st.Substeps, jobSubstep{Name: e.Step, Status: string(e.Status), Note: e.Note, At: &now})
		})
	}
}

func (s *inheritService) addWarning(rt *jobRuntime, code string) {
	s.update(rt, nil, func(j *inheritJob) {
		j.Warnings = append(j.Warnings, *jobProblemFor(code))
	})
}

// finish ends the job. Steps that never ran are marked skipped.
func (s *inheritService) finish(rt *jobRuntime, status inheritJobStatus, code string) {
	now := s.now()
	s.update(rt, &jobEvent{Step: stepDone, Status: string(status), Code: code, At: &now}, func(j *inheritJob) {
		j.Status = status
		j.FinishedAt = &now
		j.ConfirmDeadline = nil
		if code != "" {
			j.Error = jobProblemFor(code)
		}
		for i := range j.Steps {
			switch {
			case j.Steps[i].Name == stepDone && status == jobStatusSucceeded:
				j.Steps[i].Status, j.Steps[i].StartedAt, j.Steps[i].FinishedAt = stepOK, &now, &now
				j.CurrentStep = stepDone
			case j.Steps[i].Status == stepPending:
				j.Steps[i].Status = stepSkipped
			case j.Steps[i].Status == stepRunning:
				j.Steps[i].Status, j.Steps[i].FinishedAt = stepFailed, &now
			}
		}
	})
}

// loadJob returns the job if it exists and belongs to userID; a job owned by
// someone else is reported as missing. A job left unfinished by a stopped
// process is shown as interrupted.
func (s *inheritService) loadJob(ctx context.Context, userID, jobID string) (*inheritJob, *jobRuntime, *apiProblem) {
	if rt := s.registry.get(jobID); rt != nil {
		rt.mu.Lock()
		job := rt.job.clone()
		rt.mu.Unlock()
		if job.OwnerUserID != userID {
			return nil, nil, errJobNotFound
		}
		return job, rt, nil
	}
	job, err := s.jobs.load(ctx, jobID)
	if err != nil {
		return nil, nil, errStoreUnavailable
	}
	if job == nil || job.OwnerUserID != userID {
		return nil, nil, errJobNotFound
	}
	if !job.Status.terminal() && job.Instance != inheritInstanceID {
		now := s.now()
		job.Status, job.FinishedAt, job.Error, job.ConfirmDeadline = jobStatusFailed, &now, jobProblemFor(codeJobInterrupted), nil
		for i := range job.Steps {
			if job.Steps[i].Status == stepPending || job.Steps[i].Status == stepRunning {
				job.Steps[i].Status = stepSkipped
			}
		}
		_ = s.jobs.save(ctx, job)
	}
	return job, nil, nil
}

// writeAudit records a system log row for a vault or job action. It never
// carries a secret: only IDs, the server and the masked game user ID.
func (s *inheritService) writeAudit(meta requestMeta, action, result, targetID string, metadata map[string]any) {
	if s.api == nil {
		return
	}
	targetType := "inherit_vault_entry"
	if strings.HasPrefix(action, "inherit.job.") {
		targetType = "inherit_job"
	}
	if strings.HasPrefix(action, "inherit.vault.key.") {
		targetType = "user"
	}
	var target *string
	if targetID != "" {
		t := targetID
		target = &t
	}
	user, role := meta.UserID, meta.Role
	if role == "" {
		role = "user"
	}
	actorType := harukiAPIHelper.SystemLogActorTypeUser
	if user == "" {
		actorType = harukiAPIHelper.SystemLogActorTypeSystem
	}
	entry := harukiAPIHelper.SystemLogEntry{
		ActorUserID: &user, ActorRole: &role, ActorType: actorType, Action: action, TargetType: &targetType, TargetID: target,
		Result: result, Metadata: metadata,
	}
	if meta.IP != "" {
		entry.IP, entry.UserAgent, entry.Method, entry.Path = &meta.IP, &meta.UserAgent, &meta.Method, &meta.Path
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = harukiAPIHelper.WriteSystemLog(ctx, s.api, entry)
}
