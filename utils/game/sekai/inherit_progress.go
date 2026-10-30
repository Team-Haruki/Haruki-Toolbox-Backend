package sekai

// ProgressStatus is the state a sub-step reports.
type ProgressStatus string

const (
	ProgressRunning ProgressStatus = "running"
	ProgressDone    ProgressStatus = "done"
	// ProgressWarning marks a non-critical call that failed; the step goes on.
	ProgressWarning ProgressStatus = "warning"
	ProgressFailed  ProgressStatus = "failed"
)

// ProgressEvent is one sub-step transition. Note never carries a secret or
// the game user ID: it is a short reason such as "status 503".
type ProgressEvent struct {
	Step   string
	Status ProgressStatus
	Note   string
}

// Progress receives every sub-step transition of an InheritSession, in order,
// on the goroutine driving the session. A nil Progress discards them.
type Progress func(ProgressEvent)

// Sub-step names reported by InheritSession.
const (
	SubStepCookies          = "cookies"
	SubStepVersion          = "version"
	SubStepInheritCheck     = "inherit_check"
	SubStepInheritExecute   = "inherit_execute"
	SubStepAuth             = "auth"
	SubStepIssueCode        = "issue_code"
	SubStepSuite            = "suite"
	SubStepSuiteFollowup    = "suite_followup"
	SubStepHomeRefresh      = "home_refresh"
	SubStepMaintenanceCheck = "maintenance_check"
	SubStepMysekai          = "mysekai"
	SubStepMysekaiFollowup  = "mysekai_followup"
)

func (s *InheritSession) report(step string, status ProgressStatus, note string) {
	if s.progress != nil {
		s.progress(ProgressEvent{Step: step, Status: status, Note: note})
	}
}

// track reports step as running, then done or failed according to err, and
// returns err unchanged.
func (s *InheritSession) track(step string, run func() error) error {
	s.report(step, ProgressRunning, "")
	if err := run(); err != nil {
		s.report(step, ProgressFailed, progressNote(err))
		return err
	}
	s.report(step, ProgressDone, "")
	return nil
}

// progressNote turns an error into a short, secret-free reason.
func progressNote(err error) string {
	switch {
	case err == nil:
		return ""
	case IsMaintenanceError(err):
		return "maintenance"
	}
	if apiErr, ok := asAPIError(err); ok {
		if apiErr.StatusCode == 0 {
			return "network error"
		}
		return "status " + itoa(apiErr.StatusCode)
	}
	return "error"
}
