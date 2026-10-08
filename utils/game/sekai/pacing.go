package sekai

import (
	"context"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
)

// InheritPacing holds the pauses an inherit makes between game API calls. The
// calls themselves and their order are fixed; only these waits are
// configurable (sekai_client.inherit_pacing).
type InheritPacing struct {
	// AfterInheritCheck separates the checking inherit call from the
	// executing one.
	AfterInheritCheck time.Duration
	// BeforeLogin separates the executing inherit call from the login.
	BeforeLogin time.Duration
	// SuiteFollowup is each of the three pauses around the suite follow-up
	// calls.
	SuiteFollowup time.Duration
}

// InheritPacingFromConfig converts the configured milliseconds, keeping the
// default for any pause that is unset (0 or less). An unloaded config
// therefore still paces like production.
func InheritPacingFromConfig(cfg harukiConfig.SekaiInheritPacingConfig) InheritPacing {
	n := cfg.Normalized()
	return InheritPacing{
		AfterInheritCheck: time.Duration(n.AfterInheritCheckMS) * time.Millisecond,
		BeforeLogin:       time.Duration(n.BeforeLoginMS) * time.Millisecond,
		SuiteFollowup:     time.Duration(n.SuiteFollowupMS) * time.Millisecond,
	}
}

// DefaultInheritPacing is the pacing the inherit flow has always used.
func DefaultInheritPacing() InheritPacing {
	return InheritPacingFromConfig(harukiConfig.SekaiInheritPacingConfig{})
}

// withDefaults fills every unset (0 or less) pause with its default, so a
// caller that does not wire pacing in still paces like production rather than
// not pausing at all.
func (p InheritPacing) withDefaults() InheritPacing {
	d := DefaultInheritPacing()
	if p.AfterInheritCheck <= 0 {
		p.AfterInheritCheck = d.AfterInheritCheck
	}
	if p.BeforeLogin <= 0 {
		p.BeforeLogin = d.BeforeLogin
	}
	if p.SuiteFollowup <= 0 {
		p.SuiteFollowup = d.SuiteFollowup
	}
	return p
}

// pause waits d, or less if ctx ends first, in which case it returns ctx's
// error. A variable so tests can observe the waits without spending them.
var pause = sleepContext

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
