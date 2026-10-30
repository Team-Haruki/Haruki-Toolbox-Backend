package upload

import (
	"context"
	"errors"
	"time"

	json "encoding/json/v2"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/inheritvault"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
)

// jobOutcome is what the single completion log line reports.
type jobOutcome struct {
	finalStep string
	status    inheritJobStatus
	code      string
}

// run drives one job from bootstrap to done. It is started by the create
// handler with a concurrency slot and a breaker admission already held for
// the first phase.
func (s *inheritService) run(rt *jobRuntime, breakerToken uint64) {
	started := time.Now()
	server := harukiUtils.SupportedInheritUploadServer(rt.job.Server)
	outcome := s.runPhases(rt, server, breakerToken)

	rt.mu.Lock()
	rt.code = harukiSekai.InheritCode{}
	rt.closeSubscribersLocked()
	rt.mu.Unlock()
	s.registry.remove(rt.job.ID)
	s.releaseActive(rt.job.OwnerUserID, rt.job.ID)
	// The one log line per job: no user, game or inherit identifiers.
	s.logger.Infof("inherit job finished job=%s server=%s upload_type=%s final_step=%s outcome=%s code=%s duration_ms=%d",
		rt.job.ID, rt.job.Server, rt.job.UploadType, outcome.finalStep, outcome.status, outcome.code, time.Since(started).Milliseconds())
}

func (s *inheritService) end(rt *jobRuntime, step string, status inheritJobStatus, code string) jobOutcome {
	s.finish(rt, status, code)
	return jobOutcome{finalStep: step, status: status, code: code}
}

func (s *inheritService) failStep(rt *jobRuntime, step, code string) jobOutcome {
	s.setStep(rt, step, stepFailed, code)
	return s.end(rt, step, jobStatusFailed, code)
}

func (s *inheritService) runPhases(rt *jobRuntime, server harukiUtils.SupportedInheritUploadServer, breakerToken uint64) jobOutcome {
	shutdown := harukiBackground.ShutdownSignal(s.runner)

	// Phase 1: bootstrap and preview. Nothing here consumes the code.
	game, preview, outcome, done := s.runPreviewPhase(rt, server, breakerToken, shutdown)
	if done {
		return outcome
	}

	// Pause until the owner confirms (or cancels, or the wait times out).
	conf, outcome, done := s.awaitConfirmation(rt, shutdown)
	if done {
		return outcome
	}

	// Phase 2: from the executing call on, the code is used up.
	return s.runTransferPhase(rt, server, game, preview.UserID, conf)
}

func (s *inheritService) runPreviewPhase(rt *jobRuntime, server harukiUtils.SupportedInheritUploadServer, breakerToken uint64, shutdown <-chan struct{}) (inheritGame, *harukiSekai.InheritPreview, jobOutcome, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), s.phaseTimeout)
	defer cancel()
	if shutdown != nil {
		go func() {
			select {
			case <-shutdown:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	rt.mu.Lock()
	rt.stopPhase = cancel
	code := rt.code
	rt.mu.Unlock()

	var gameErr error
	defer func() {
		s.limiter.release(server)
		s.breaker.RecordResult(server, breakerToken, inheritFailureIsUpstreamDegradation(gameErr))
	}()

	game, err := s.newGame(server, s.progressFor(rt))
	if err != nil {
		return nil, nil, s.failStep(rt, stepBootstrap, codeInternal), true
	}
	cancelledOutcome := func(step string) (inheritGame, *harukiSekai.InheritPreview, jobOutcome, bool) {
		s.setStep(rt, step, stepSkipped, codeCancelled)
		if shutdownRequested(shutdown) {
			return nil, nil, s.end(rt, step, jobStatusFailed, codeJobInterrupted), true
		}
		return nil, nil, s.end(rt, step, jobStatusCancelled, codeCancelled), true
	}

	s.setStep(rt, stepBootstrap, stepRunning, "")
	if err := game.Bootstrap(ctx); err != nil {
		if s.isCancelled(rt) || shutdownRequested(shutdown) {
			return cancelledOutcome(stepBootstrap)
		}
		gameErr = err
		return nil, nil, s.failStep(rt, stepBootstrap, gameFailureCode(err)), true
	}
	s.setStep(rt, stepBootstrap, stepOK, "")

	s.setStep(rt, stepPreview, stepRunning, "")
	preview, err := game.Preview(ctx, code)
	if err != nil {
		if s.isCancelled(rt) || shutdownRequested(shutdown) {
			return cancelledOutcome(stepPreview)
		}
		gameErr = err
		return nil, nil, s.failStep(rt, stepPreview, gameFailureCode(err)), true
	}
	view := &jobPreview{Nickname: preview.Name, Rank: preview.Rank, GameUserIDMasked: inheritvault.MaskGameUserID(preview.UserID)}
	if r := preview.Restriction; r != nil {
		view.Restriction = &jobRestriction{
			IsRestrictDeviceTransfer: r.IsRestrictDeviceTransfer, RestrictEndAt: r.RestrictEndAt, RestTransferCount: r.RestTransferCount,
			IsWorldBloomChapter: r.IsWorldBloomChapter, RestrictRank: r.RestrictRank, GameCharacterID: r.GameCharacterID,
		}
	}
	s.update(rt, nil, func(j *inheritJob) { j.Preview = view })
	if r := preview.Restriction; r != nil && r.IsRestrictDeviceTransfer && r.RestTransferCount <= 0 {
		return nil, nil, s.failStep(rt, stepPreview, codeTransferRestricted), true
	}
	s.setStep(rt, stepPreview, stepOK, "")
	return game, preview, jobOutcome{}, false
}

func shutdownRequested(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (s *inheritService) isCancelled(rt *jobRuntime) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.cancelled
}

func (s *inheritService) awaitConfirmation(rt *jobRuntime, shutdown <-chan struct{}) (confirmation, jobOutcome, bool) {
	deadline := s.now().Add(s.confirmTimeout)
	s.setStep(rt, stepAwaitingConfirm, stepRunning, "")
	s.update(rt, nil, func(j *inheritJob) {
		j.Status = jobStatusAwaitingConfirm
		j.ConfirmDeadline = &deadline
	})
	timer := time.NewTimer(s.confirmTimeout)
	defer timer.Stop()
	stopWaiting := func(code string, status inheritJobStatus) (confirmation, jobOutcome, bool) {
		rt.mu.Lock()
		if rt.confirmed {
			// The confirmation won the race; take it.
			rt.mu.Unlock()
			return s.takeConfirmation(rt)
		}
		rt.cancelled = true
		rt.mu.Unlock()
		s.setStep(rt, stepAwaitingConfirm, stepSkipped, code)
		return confirmation{}, s.end(rt, stepAwaitingConfirm, status, code), true
	}
	select {
	case conf := <-rt.confirmCh:
		return s.confirmed(rt, conf)
	case <-rt.cancelCh:
		return stopWaiting(codeCancelled, jobStatusCancelled)
	case <-timer.C:
		return stopWaiting(codeConfirmTimeout, jobStatusCancelled)
	case <-shutdown:
		return stopWaiting(codeJobInterrupted, jobStatusFailed)
	}
}

func (s *inheritService) takeConfirmation(rt *jobRuntime) (confirmation, jobOutcome, bool) {
	return s.confirmed(rt, <-rt.confirmCh)
}

func (s *inheritService) confirmed(rt *jobRuntime, conf confirmation) (confirmation, jobOutcome, bool) {
	s.setStep(rt, stepAwaitingConfirm, stepOK, "")
	s.update(rt, nil, func(j *inheritJob) {
		j.Status = jobStatusRunning
		j.ConfirmDeadline = nil
	})
	return conf, jobOutcome{}, false
}

// acquireSlot waits up to slotWait for a concurrency slot on server.
func (s *inheritService) acquireSlot(server harukiUtils.SupportedInheritUploadServer) bool {
	deadline := time.Now().Add(s.slotWait)
	for {
		if s.limiter.acquire(server) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (s *inheritService) runTransferPhase(rt *jobRuntime, server harukiUtils.SupportedInheritUploadServer, game inheritGame, gameUserID int64, conf confirmation) jobOutcome {
	allowed, _, breakerToken := s.breaker.Allow(server)
	if !allowed {
		return s.failStep(rt, stepExecute, codeGameDegraded)
	}
	if !s.acquireSlot(server) {
		s.breaker.ReleaseProbe(server, breakerToken)
		return s.failStep(rt, stepExecute, codeServerBusy)
	}
	slotHeld := true
	var gameErr error
	breakerRecorded := false
	releaseGame := func() {
		if !breakerRecorded {
			breakerRecorded = true
			s.breaker.RecordResult(server, breakerToken, inheritFailureIsUpstreamDegradation(gameErr))
		}
		if slotHeld {
			slotHeld = false
			s.limiter.release(server)
		}
	}
	defer releaseGame()

	ctx, cancel := context.WithTimeout(context.Background(), s.phaseTimeout)
	defer cancel()
	job := rt.job
	rt.mu.Lock()
	code := rt.code
	rt.mu.Unlock()

	// execute
	s.setStep(rt, stepExecute, stepRunning, "")
	credential, err := game.Execute(ctx, code)
	rt.mu.Lock()
	rt.code = harukiSekai.InheritCode{}
	rt.mu.Unlock()
	if err != nil {
		gameErr = err
		failure := gameFailureCode(err)
		if failure == codeGameUnavailable {
			// The request may have reached the game: the code is possibly used.
			failure = codeExecuteUncertain
		}
		return s.failStep(rt, stepExecute, failure)
	}
	s.setStep(rt, stepExecute, stepOK, "")

	// vault_store_credential: committed before any further network call.
	entryID := inheritvault.NewEntryID()
	s.setStep(rt, stepVaultStoreCredential, stepRunning, "")
	location, err := s.vault.PutCredential(context.Background(), inheritvault.CredentialInput{
		EntryID: entryID, UserID: job.OwnerUserID, Server: job.Server, GameUserID: gameUserID, JobID: job.ID,
		KeyVersion: conf.keyVersion, PublicKey: conf.publicKey, Credential: []byte(credential),
	})
	credentialStored := err == nil
	switch {
	case err != nil:
		// Both PostgreSQL and Redis refused it. Stopping here would lose the
		// account for certain (the original device is already logged out),
		// so the job goes on to issue a new code the owner can copy.
		s.setStep(rt, stepVaultStoreCredential, stepFailed, codeVaultStoreFailed)
		s.addWarning(rt, codeVaultStoreFailed)
	case location == inheritvault.LocationFallback:
		s.addWarning(rt, codeVaultFallback)
		fallthrough
	default:
		s.update(rt, nil, func(j *inheritJob) { j.VaultEntryID = entryID })
		s.setStep(rt, stepVaultStoreCredential, stepOK, "")
	}
	s.writeAudit(requestMeta{UserID: job.OwnerUserID}, "inherit.vault.store", auditResult(credentialStored), entryID, map[string]any{
		"job_id": job.ID, "server": job.Server, "game_user_id_masked": inheritvault.MaskGameUserID(gameUserID), "location": string(location),
	})

	// login
	s.setStep(rt, stepLogin, stepRunning, "")
	if err := game.Login(ctx, gameUserID, credential); err != nil {
		gameErr = err
		return s.failStep(rt, stepLogin, codeLoginFailed)
	}
	s.setStep(rt, stepLogin, stepOK, "")

	// issue_new_code, before any data is fetched.
	newPassword := randomInheritPassword()
	if job.NewPasswordMode == newPasswordModeReuse {
		newPassword = code.Password
	}
	s.setStep(rt, stepIssueNewCode, stepRunning, "")
	newInheritID, err := s.issueWithRetry(ctx, game, newPassword)
	if err != nil {
		gameErr = err
		s.setStep(rt, stepIssueNewCode, stepFailed, codeIssueCodeFailed)
		s.addWarning(rt, codeIssueCodeFailed)
		s.setStep(rt, stepVaultStoreCode, stepSkipped, "")
	} else {
		expires := s.now().Add(inheritvault.CodeLifetime)
		plain, _ := json.Marshal(liveCode{InheritID: newInheritID, Password: newPassword})
		sealed := s.vault.Master().SealLive(plain, job.ID, job.OwnerUserID)
		clear(plain)
		s.update(rt, nil, func(j *inheritJob) {
			j.LiveCode = sealed
			j.CodeExpiresAt = &expires
		})
		s.setStep(rt, stepIssueNewCode, stepOK, "")

		s.setStep(rt, stepVaultStoreCode, stepRunning, "")
		if !credentialStored {
			s.setStep(rt, stepVaultStoreCode, stepSkipped, codeVaultStoreFailed)
		} else if loc, err := s.vault.PutCode(context.Background(), inheritvault.CodeInput{
			EntryID: entryID, UserID: job.OwnerUserID, Server: job.Server, KeyVersion: conf.keyVersion, PublicKey: conf.publicKey,
			InheritID: newInheritID, Password: newPassword,
		}); err != nil {
			s.setStep(rt, stepVaultStoreCode, stepFailed, codeVaultStoreFailed)
			s.addWarning(rt, codeVaultStoreFailed)
		} else {
			if loc == inheritvault.LocationFallback && location != inheritvault.LocationFallback {
				s.addWarning(rt, codeVaultFallback)
			}
			s.setStep(rt, stepVaultStoreCode, stepOK, "")
		}
	}

	// fetch_suite / fetch_mysekai
	s.setStep(rt, stepFetchSuite, stepRunning, "")
	suite, err := game.FetchSuite(ctx)
	if err != nil {
		gameErr = err
		s.recordRetrievalFailure(job, gameUserID, err)
		failure := codeFetchFailed
		if harukiSekai.IsMaintenanceError(err) {
			failure = codeGameMaintenance
		}
		return s.failStep(rt, stepFetchSuite, failure)
	}
	s.setStep(rt, stepFetchSuite, stepOK, "")
	var mysekai []byte
	if job.UploadType == string(harukiUtils.UploadDataTypeMysekai) {
		s.setStep(rt, stepFetchMysekai, stepRunning, "")
		mysekai, err = game.FetchMysekai(ctx)
		if err == nil && len(mysekai) == 0 {
			err = harukiSekai.NewDataRetrievalError("mysekai", "api_response", "empty response", nil)
		}
		if err != nil {
			gameErr = err
			s.recordRetrievalFailure(job, gameUserID, err)
			failure := codeMysekaiUnavailable
			if harukiSekai.IsMaintenanceError(err) {
				failure = codeGameMaintenance
			}
			return s.failStep(rt, stepFetchMysekai, failure)
		}
		s.setStep(rt, stepFetchMysekai, stepOK, "")
	} else {
		s.setStep(rt, stepFetchMysekai, stepSkipped, "")
	}
	releaseGame()

	// upload
	s.setStep(rt, stepUpload, stepRunning, "")
	uploadCtx, cancelUpload := context.WithTimeout(context.Background(), asyncUploadTimeout)
	defer cancelUpload()
	if err := s.upload(uploadCtx, job, gameUserID, suite, mysekai); err != nil {
		failure := codeUploadFailed
		if errors.Is(err, errUploadOwnershipMismatch) {
			failure = codeUploadForbidden
		}
		return s.failStep(rt, stepUpload, failure)
	}
	s.setStep(rt, stepUpload, stepOK, "")
	return s.end(rt, stepDone, jobStatusSucceeded, "")
}

func auditResult(ok bool) string {
	if ok {
		return "success"
	}
	return "failure"
}

// issueWithRetry asks for a new code a few times with a growing pause.
func (s *inheritService) issueWithRetry(ctx context.Context, game inheritGame, password string) (string, error) {
	var lastErr error
	for attempt := range inheritIssueAttempts {
		if attempt > 0 {
			timer := time.NewTimer(s.issueBackoff(attempt))
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return "", errors.Join(lastErr, ctx.Err())
			}
		}
		id, err := game.IssueInheritCode(ctx, password)
		if err == nil {
			return id, nil
		}
		lastErr = err
	}
	return "", lastErr
}

// uploadData stores the fetched data like any inherit upload: no Toolbox
// ownership is required, since presenting the transfer code proved it, but
// the attempt is attributed to the job owner.
func (s *inheritService) uploadData(ctx context.Context, job *inheritJob, gameUserID int64, suite, mysekai []byte) error {
	server := harukiUtils.SupportedDataUploadServer(job.Server)
	deps := s.deps
	deps.BackgroundTasks = harukiBackground.InlineRunner{Shutdown: harukiBackground.ShutdownSignal(s.deps.BackgroundTasks)}
	if mysekai != nil {
		attempt := newAttempt(harukiUtils.UploadMethodInherit, len(mysekai))
		attempt.ActorUserID = job.OwnerUserID
		if _, err := HandleUpload(ctx, mysekai, server, harukiUtils.UploadDataTypeMysekai, &gameUserID, nil, s.api, deps, harukiUtils.UploadMethodInherit, attempt); err != nil {
			return err
		}
	}
	attempt := newAttempt(harukiUtils.UploadMethodInherit, len(suite))
	attempt.ActorUserID = job.OwnerUserID
	_, err := HandleUpload(ctx, suite, server, harukiUtils.UploadDataTypeSuite, &gameUserID, nil, s.api, deps, harukiUtils.UploadMethodInherit, attempt)
	return err
}

func (s *inheritService) recordRetrievalFailure(job *inheritJob, gameUserID int64, err error) {
	if s.api == nil || s.api.DBManager == nil {
		return
	}
	recordInheritRetrievalFailure(s.api, s.deps, harukiUtils.SupportedDataUploadServer(job.Server), harukiUtils.UploadDataType(job.UploadType), gameUserID, err)
}
