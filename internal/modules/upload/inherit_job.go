package upload

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"time"

	json "encoding/json/v2"

	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Inherit jobs run a transfer-code upload in the background so the owner can
// confirm the account before the code is used, and so the account is handed
// back (a new transfer code, and the credential kept in the vault) even when
// something fails afterwards. See docs/inherit-jobs.md for the API contract.

const (
	inheritJobTTL            = time.Hour
	inheritConfirmTimeout    = 5 * time.Minute
	inheritPhaseTimeout      = 90 * time.Second
	inheritSlotWait          = 30 * time.Second
	inheritActiveLockTTL     = 15 * time.Minute
	inheritJobsPerUserHour   = 10
	inheritIssueAttempts     = 3
	inheritJobKeyPrefix      = "inherit:job:"
	inheritActiveKeyPrefix   = "inherit:jobs:active:"
	inheritRateKeyPrefix     = "inherit:jobs:rate:"
	inheritRandomPasswordLen = 12
)

type inheritJobStatus string

const (
	jobStatusRunning         inheritJobStatus = "running"
	jobStatusAwaitingConfirm inheritJobStatus = "awaiting_confirm"
	jobStatusSucceeded       inheritJobStatus = "succeeded"
	jobStatusFailed          inheritJobStatus = "failed"
	jobStatusCancelled       inheritJobStatus = "cancelled"
)

func (s inheritJobStatus) terminal() bool {
	return s == jobStatusSucceeded || s == jobStatusFailed || s == jobStatusCancelled
}

// Job steps, in order.
const (
	stepBootstrap            = "bootstrap"
	stepPreview              = "preview"
	stepAwaitingConfirm      = "awaiting_confirm"
	stepExecute              = "execute"
	stepVaultStoreCredential = "vault_store_credential"
	stepLogin                = "login"
	stepIssueNewCode         = "issue_new_code"
	stepVaultStoreCode       = "vault_store_code"
	stepFetchSuite           = "fetch_suite"
	stepFetchMysekai         = "fetch_mysekai"
	stepUpload               = "upload"
	stepDone                 = "done"
)

var inheritJobSteps = []string{
	stepBootstrap, stepPreview, stepAwaitingConfirm, stepExecute, stepVaultStoreCredential,
	stepLogin, stepIssueNewCode, stepVaultStoreCode, stepFetchSuite, stepFetchMysekai, stepUpload, stepDone,
}

type stepStatus string

const (
	stepPending stepStatus = "pending"
	stepRunning stepStatus = "running"
	stepOK      stepStatus = "done"
	stepFailed  stepStatus = "failed"
	stepSkipped stepStatus = "skipped"
)

type jobSubstep struct {
	Name   string     `json:"name"`
	Status string     `json:"status"`
	Note   string     `json:"note,omitempty"`
	At     *time.Time `json:"at"`
}

type jobStep struct {
	Name       string       `json:"name"`
	Status     stepStatus   `json:"status"`
	StartedAt  *time.Time   `json:"started_at,omitempty"`
	FinishedAt *time.Time   `json:"finished_at,omitempty"`
	ErrorCode  string       `json:"error_code,omitempty"`
	Substeps   []jobSubstep `json:"substeps,omitempty"`
}

type jobProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type jobRestriction struct {
	IsRestrictDeviceTransfer bool  `json:"is_restrict_device_transfer"`
	RestrictEndAt            int64 `json:"restrict_end_at"`
	RestTransferCount        int64 `json:"rest_transfer_count"`
	IsWorldBloomChapter      bool  `json:"is_world_bloom_chapter"`
	RestrictRank             int64 `json:"restrict_rank"`
	GameCharacterID          int64 `json:"game_character_id"`
}

type jobPreview struct {
	Nickname         string          `json:"nickname"`
	Rank             int64           `json:"rank"`
	GameUserIDMasked string          `json:"game_user_id_masked"`
	Restriction      *jobRestriction `json:"transfer_restriction,omitempty"`
}

// jobUnlock tells the client which secret the confirm step needs.
type jobUnlock struct {
	// Kind is "password" (the Toolbox password) or "pin" (a vault PIN, for
	// identities without a password).
	Kind string `json:"kind"`
	// Initialized is false until the owner's vault key exists; the confirm
	// step then creates it from the same input.
	Initialized bool `json:"initialized"`
}

// inheritJob is the job record kept in Redis for an hour. It holds no
// plaintext secret: the new transfer code is only kept sealed under the
// server master key (LiveCode) for the job's lifetime.
type inheritJob struct {
	ID              string           `json:"id"`
	OwnerUserID     string           `json:"owner_user_id"`
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
	CodeExpiresAt   *time.Time       `json:"code_expires_at,omitempty"`
	LiveCode        []byte           `json:"live_code,omitempty"`
	Instance        string           `json:"instance"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	FinishedAt      *time.Time       `json:"finished_at,omitempty"`
}

func newInheritJob(ownerUserID, server, uploadType, mode string, now time.Time) *inheritJob {
	job := &inheritJob{
		ID: uuid.NewString(), OwnerUserID: ownerUserID, Server: server, UploadType: uploadType, NewPasswordMode: mode,
		Status: jobStatusRunning, CurrentStep: stepBootstrap, Instance: inheritInstanceID, CreatedAt: now, UpdatedAt: now,
	}
	for _, name := range inheritJobSteps {
		job.Steps = append(job.Steps, jobStep{Name: name, Status: stepPending})
	}
	return job
}

func (j *inheritJob) step(name string) *jobStep {
	for i := range j.Steps {
		if j.Steps[i].Name == name {
			return &j.Steps[i]
		}
	}
	return nil
}

func (j *inheritJob) clone() *inheritJob {
	c := *j
	c.Steps = make([]jobStep, len(j.Steps))
	for i, s := range j.Steps {
		s.Substeps = append([]jobSubstep(nil), s.Substeps...)
		c.Steps[i] = s
	}
	c.Warnings = append([]jobProblem(nil), j.Warnings...)
	c.LiveCode = append([]byte(nil), j.LiveCode...)
	return &c
}

// inheritInstanceID tells this process's jobs apart from those of a process
// that has since stopped: a non-final job owned by another instance can no
// longer progress.
var inheritInstanceID = uuid.NewString()

// jobStore persists job records in Redis.
type jobStore struct {
	redis *redis.Client
}

func (s jobStore) save(ctx context.Context, job *inheritJob) error {
	if s.redis == nil {
		return errors.New("job store unavailable")
	}
	body, err := json.Marshal(job)
	if err != nil {
		return err
	}
	ttl := time.Until(job.CreatedAt.Add(inheritJobTTL))
	if ttl <= 0 {
		ttl = time.Second
	}
	return s.redis.Set(ctx, inheritJobKeyPrefix+job.ID, body, ttl).Err()
}

func (s jobStore) load(ctx context.Context, id string) (*inheritJob, error) {
	if s.redis == nil {
		return nil, errors.New("job store unavailable")
	}
	body, err := s.redis.Get(ctx, inheritJobKeyPrefix+id).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var job inheritJob
	if err := json.Unmarshal(body, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// confirmation is what the confirm step hands the waiting job: the public
// key to seal the credential to. The private key never reaches the job.
type confirmation struct {
	publicKey  []byte
	keyVersion int
}

// jobRuntime is a job running in this process. The inherit code the owner
// entered lives only here, in memory, and is dropped after the executing
// call (or when the job ends).
type jobRuntime struct {
	mu        sync.Mutex
	job       *inheritJob
	code      harukiSekai.InheritCode
	confirmed bool
	cancelled bool
	confirmCh chan confirmation
	cancelCh  chan struct{}
	stopPhase context.CancelFunc
	subs      map[int]chan []byte
	nextSub   int
}

// jobRegistry holds the jobs running in this process.
type jobRegistry struct {
	mu   sync.Mutex
	jobs map[string]*jobRuntime
}

func newJobRegistry() *jobRegistry { return &jobRegistry{jobs: map[string]*jobRuntime{}} }

func (r *jobRegistry) add(rt *jobRuntime) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[rt.job.ID] = rt
}

func (r *jobRegistry) get(id string) *jobRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[id]
}

func (r *jobRegistry) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.jobs, id)
}

// subscribe returns a channel of encoded WebSocket messages for the job and
// a function that ends the subscription.
func (rt *jobRuntime) subscribe() (<-chan []byte, func()) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.subs == nil {
		rt.subs = map[int]chan []byte{}
	}
	id := rt.nextSub
	rt.nextSub++
	ch := make(chan []byte, 64)
	rt.subs[id] = ch
	return ch, func() {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if c, ok := rt.subs[id]; ok {
			delete(rt.subs, id)
			close(c)
		}
	}
}

// publishLocked sends msg to every subscriber; a subscriber too slow to keep
// up is dropped (its socket then closes and the client reconnects).
func (rt *jobRuntime) publishLocked(msg []byte) {
	for id, ch := range rt.subs {
		select {
		case ch <- msg:
		default:
			delete(rt.subs, id)
			close(ch)
		}
	}
}

func (rt *jobRuntime) closeSubscribersLocked() {
	for id, ch := range rt.subs {
		delete(rt.subs, id)
		close(ch)
	}
}

const randomPasswordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// randomInheritPassword returns 12 characters from [A-Za-z0-9], uniformly.
func randomInheritPassword() string {
	out := make([]byte, 0, inheritRandomPasswordLen)
	buf := make([]byte, 32)
	for len(out) < inheritRandomPasswordLen {
		_, _ = rand.Read(buf)
		for _, b := range buf {
			// 248 = 62*4: reject the tail so every character is equally likely.
			if b < 248 && len(out) < inheritRandomPasswordLen {
				out = append(out, randomPasswordAlphabet[int(b)%len(randomPasswordAlphabet)])
			}
		}
	}
	return string(out)
}
