package sekai

import (
	"context"
	"encoding/base64"
)

const (
	suiteFollowupQuery = "?isForceAllReload=false&name=user_colorful_pass,user_colorful_pass_v2,user_offline_event"

	// SubStepHomeRefreshFinal is the second home refresh, after the suite.
	SubStepHomeRefreshFinal = "home_refresh_final"
)

var (
	refreshPayload = map[string]any{
		"refreshableTypes": []string{
			"new_pending_friend_request",
			"user_report_thanks_message",
			"streaming_virtual_live_reward_status",
		},
	}
	refreshPayloadLogin = map[string]any{
		"refreshableTypes": []string{
			"new_pending_friend_request",
			"login_bonus",
			"user_report_thanks_message",
			"streaming_virtual_live_reward_status",
		},
	}
	mysekaiRoomPayload = map[string]any{
		"roomProperty": map[string]any{
			"isRSend": 1,
			"values":  map[string]any{},
		},
	}
)

// FetchSuite loads the account's suite the way the game client does after a
// login: the suite request, its paced follow-up calls, a home refresh, and a
// second home refresh. Only the suite request itself and the pauses (which
// stop on cancellation) can fail the step; the follow-up calls are reported
// as warnings.
func (s *InheritSession) FetchSuite(ctx context.Context) ([]byte, error) {
	var suite []byte
	err := s.track(SubStepSuite, func() error {
		body, _, err := s.call(ctx, "GET", userPath("/suite/user/%s", s.userID)+s.region.suiteQuery, nil, nil)
		if err != nil {
			return NewDataRetrievalError("suite", "api_call", "failed to call suite API", err)
		}
		if body == nil {
			return NewDataRetrievalError("suite", "api_response", "suite API returned nil response", nil)
		}
		suite = body
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.track(SubStepSuiteFollowup, func() error { return s.suiteFollowup(ctx) }); err != nil {
		return nil, NewDataRetrievalError("suite", "pause", "interrupted between suite calls", err)
	}
	unpacked, err := s.unpack(suite)
	if err != nil {
		return nil, NewDataRetrievalError("suite", "unpack", "failed to unpack response", err)
	}
	friends := unpacked["userFriends"] != nil
	s.refreshHome(ctx, SubStepHomeRefresh, friends, s.loginBonus)
	s.refreshHome(ctx, SubStepHomeRefreshFinal, false, false)
	return suite, nil
}

// suiteFollowup makes the calls the real client makes after the suite
// request, paced like it. Their failures are not critical; only the context
// ending during a pause stops the step.
func (s *InheritSession) suiteFollowup(ctx context.Context) error {
	wait := s.pacing.SuiteFollowup
	if err := pause(ctx, wait); err != nil {
		return err
	}
	_, _, _ = s.call(ctx, "GET", userPath("/suite/user/%s", s.userID)+suiteFollowupQuery, nil, nil)
	if err := pause(ctx, wait); err != nil {
		return err
	}
	_, _, _ = s.call(ctx, "GET", "/system", nil, nil)
	return pause(ctx, wait)
}

// refreshHome is the client's home refresh. It never fails the job: a failed
// call is reported as a warning on the sub-step.
func (s *InheritSession) refreshHome(ctx context.Context, step string, friends, login bool) {
	s.report(step, ProgressRunning, "")
	var failed error
	note := func(err error) {
		if err != nil && failed == nil {
			failed = err
		}
	}
	if friends {
		_, _, err := s.call(ctx, "GET", userPath("/user/%s/invitation", s.userID), nil, nil)
		note(err)
	}
	_, _, err := s.call(ctx, "GET", "/system", nil, nil)
	note(err)
	_, _, err = s.call(ctx, "GET", "/information", nil, nil)
	note(err)
	payload := refreshPayload
	if login {
		payload = refreshPayloadLogin
	}
	packed, err := s.pack(payload)
	if err == nil {
		_, _, err = s.call(ctx, "PUT", userPath("/user/%s/home/refresh", s.userID), packed, nil)
	}
	note(err)
	if failed != nil {
		s.report(step, ProgressWarning, progressNote(failed))
		return
	}
	s.report(step, ProgressDone, "")
}

// FetchMysekai checks that MySekai is not in maintenance, loads it, and makes
// the client's room and Diarkis follow-up calls (non-critical).
func (s *InheritSession) FetchMysekai(ctx context.Context) ([]byte, error) {
	if err := s.track(SubStepMaintenanceCheck, func() error { return s.mysekaiMaintenance(ctx) }); err != nil {
		return nil, err
	}
	var mysekai []byte
	err := s.track(SubStepMysekai, func() error {
		general, _ := base64.StdEncoding.DecodeString(requestDataGeneral)
		body, _, err := s.call(ctx, "POST", userPath("/user/%s/mysekai?isForceAllReloadOnlyMySekai=True", s.userID), general, nil)
		if err != nil {
			return NewDataRetrievalError("mysekai", "api_call", "failed to call MySekai API", err)
		}
		mysekai = body
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.report(SubStepMysekaiFollowup, ProgressRunning, "")
	var failed error
	if room, err := s.pack(mysekaiRoomPayload); err != nil {
		failed = err
	} else if _, _, err := s.call(ctx, "POST", userPath("/user/%[1]s/mysekai/%[1]s/room", s.userID), room, nil); err != nil {
		failed = err
	}
	if _, _, err := s.call(ctx, "GET", userPath("/user/%s/diarkis-auth?diarkisServerType=mysekai", s.userID), nil, nil); err != nil && failed == nil {
		failed = err
	}
	if failed != nil {
		s.report(SubStepMysekaiFollowup, ProgressWarning, progressNote(failed))
	} else {
		s.report(SubStepMysekaiFollowup, ProgressDone, "")
	}
	return mysekai, nil
}

// mysekaiMaintenance fails when MySekai is in maintenance or its status cannot
// be read. The room module is checked too, but only an ongoing maintenance
// there stops the step.
func (s *InheritSession) mysekaiMaintenance(ctx context.Context) error {
	for _, module := range []struct {
		name     string
		required bool
	}{{"MYSEKAI", true}, {"MYSEKAI_ROOM", false}} {
		body, _, err := s.call(ctx, "GET", "/module-maintenance/"+module.name, nil, nil)
		if err != nil {
			if module.required {
				return NewDataRetrievalError("mysekai", "maintenance_check", "failed to check maintenance status", err)
			}
			continue
		}
		unpacked, err := s.unpack(body)
		if err != nil {
			continue
		}
		if unpacked["isOngoing"] == true {
			return ErrMaintenance
		}
	}
	return nil
}
