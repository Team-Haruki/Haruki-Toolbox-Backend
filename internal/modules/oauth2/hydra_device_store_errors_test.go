package oauth2

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
)

// deviceStoreCall runs one store operation and returns its error.
type deviceStoreCall struct {
	name string
	run  func(ctx context.Context, s *deviceFlowStore) error
}

// deviceStoreCalls covers every store operation that talks to Redis.
func deviceStoreCalls() []deviceStoreCall {
	counters := []deviceRateCounter{{Key: "haruki:test:rate", Limit: 3, Window: time.Minute}}
	actor := deviceDecisionActor{UserID: testUserID, SessionHash: "session-hash", FlowHandle: deviceFlowHandlePrefix + "handle"}
	return []deviceStoreCall{
		{"create", func(ctx context.Context, s *deviceFlowStore) error {
			_, err := s.create(ctx, deviceFlowRecord{FlowID: "flow", WrappedDeviceCode: "hdc_x", NormalizedUserCode: "BCDFGHJK", ExpiresIn: time.Minute}, time.Minute)
			return err
		}},
		{"flowIDForDeviceCode", func(ctx context.Context, s *deviceFlowStore) error {
			_, _, err := s.flowIDForDeviceCode(ctx, "hdc_x")
			return err
		}},
		{"poll", func(ctx context.Context, s *deviceFlowStore) error {
			_, err := s.poll(ctx, "flow", testPublicClientID, 60, 30)
			return err
		}},
		{"settle", func(ctx context.Context, s *deviceFlowStore) error {
			_, err := s.settle(ctx, "flow", "hdc_x", deviceHydraResultPending, "")
			return err
		}},
		{"removeUnredeemed", func(ctx context.Context, s *deviceFlowStore) error {
			return s.removeUnredeemed(ctx, "flow")
		}},
		{"dueUnredeemed", func(ctx context.Context, s *deviceFlowStore) error {
			_, err := s.dueUnredeemed(ctx, 0, 10)
			return err
		}},
		{"reap", func(ctx context.Context, s *deviceFlowStore) error {
			_, _, err := s.reap(ctx, "flow")
			return err
		}},
		{"markExpired", func(ctx context.Context, s *deviceFlowStore) error {
			return s.markExpired(ctx, "flow")
		}},
		{"requeueUnredeemed", func(ctx context.Context, s *deviceFlowStore) error {
			return s.requeueUnredeemed(ctx, "flow", testConsentRequestID, 0)
		}},
		{"claim", func(ctx context.Context, s *deviceFlowStore) error {
			_, err := s.claim(ctx, "BCDFGHJK", actor.FlowHandle, actor.UserID, actor.SessionHash, time.Minute)
			return err
		}},
		{"flowIDForHandle", func(ctx context.Context, s *deviceFlowStore) error {
			_, _, err := s.flowIDForHandle(ctx, actor.FlowHandle)
			return err
		}},
		{"snapshot", func(ctx context.Context, s *deviceFlowStore) error {
			_, _, err := s.snapshot(ctx, "flow")
			return err
		}},
		{"beginApprove", func(ctx context.Context, s *deviceFlowStore) error {
			_, _, err := s.beginApprove(ctx, "flow", actor, DeviceFlowConfig{}.approvalTimings(), "nonce")
			return err
		}},
		{"recordConsent", func(ctx context.Context, s *deviceFlowStore) error {
			_, err := s.recordConsent(ctx, "flow", "nonce", testConsentRequestID, "subject")
			return err
		}},
		{"finishApprove", func(ctx context.Context, s *deviceFlowStore) error {
			_, _, err := s.finishApprove(ctx, "flow", "nonce", deviceFinishApproved, "label", deviceLabelSourceUser, 3)
			return err
		}},
		{"deny", func(ctx context.Context, s *deviceFlowStore) error {
			_, _, err := s.deny(ctx, "flow", actor, deviceDenyReasonUser)
			return err
		}},
		{"fail", func(ctx context.Context, s *deviceFlowStore) error {
			_, err := s.fail(ctx, "flow")
			return err
		}},
		{"reserve", func(ctx context.Context, s *deviceFlowStore) error {
			_, err := s.reserve(ctx, counters)
			return err
		}},
		{"release", func(ctx context.Context, s *deviceFlowStore) error {
			return s.release(ctx, counters)
		}},
		{"increment", func(ctx context.Context, s *deviceFlowStore) error {
			_, err := s.increment(ctx, "haruki:test:counter", time.Minute)
			return err
		}},
	}
}

// Without Redis every operation answers errDeviceStoreUnavailable, which the
// handlers turn into 503, and nothing panics on the nil client.
func TestDeviceFlowStoreWithoutRedis(t *testing.T) {
	stores := map[string]*deviceFlowStore{
		"nil manager":   newDeviceFlowStore(nil),
		"no redis":      newDeviceFlowStore(&database.HarukiToolboxDBManager{}),
		"nil store ptr": nil,
	}
	for storeName, store := range stores {
		for _, call := range deviceStoreCalls() {
			t.Run(storeName+"/"+call.name, func(t *testing.T) {
				if err := call.run(t.Context(), store); !errors.Is(err, errDeviceStoreUnavailable) {
					t.Fatalf("err = %v, want errDeviceStoreUnavailable", err)
				}
			})
		}
	}
	store := newDeviceFlowStore(nil)
	if _, err := store.hashUserCode("BCDFGHJK"); !errors.Is(err, errDeviceStoreUnavailable) {
		t.Fatalf("hashUserCode err = %v", err)
	}
	if _, err := store.handleHash(deviceFlowHandlePrefix + "handle"); !errors.Is(err, errDeviceStoreUnavailable) {
		t.Fatalf("handleHash err = %v", err)
	}
	if got := store.retryAfterSeconds(t.Context(), "haruki:test:rate", 10*time.Minute); got != 600 {
		t.Fatalf("retryAfterSeconds without Redis = %d, want the full window", got)
	}
}

// A Redis failure surfaces as an error from every operation (never as a
// "missing" or "refused" answer the handlers would act on).
func TestDeviceFlowStoreRedisErrors(t *testing.T) {
	env := newDeviceTestEnv(t)
	env.redis.SetError("LOADING Redis is loading the dataset in memory")
	t.Cleanup(func() { env.redis.SetError("") })
	for _, call := range deviceStoreCalls() {
		t.Run(call.name, func(t *testing.T) {
			err := call.run(t.Context(), env.store)
			if err == nil || errors.Is(err, errDeviceStoreUnavailable) || !strings.Contains(err.Error(), "LOADING") {
				t.Fatalf("err = %v, want the Redis error", err)
			}
		})
	}
	if got := env.store.retryAfterSeconds(t.Context(), "haruki:test:rate", 10*time.Minute); got != 600 {
		t.Fatalf("retryAfterSeconds on a Redis error = %d, want the full window", got)
	}
}

// claimedTestFlow issues a flow and claims it through the store, returning the
// flow ID, its normalized user code and the actor that claimed it.
func claimedTestFlow(t *testing.T, env *deviceTestEnv) (string, string, deviceDecisionActor) {
	t.Helper()
	_, flowID := env.issue(testPublicClientID)
	env.hydra.mu.Lock()
	userCode := env.hydra.userCodes[len(env.hydra.userCodes)-1]
	env.hydra.mu.Unlock()
	normalized, ok := normalizeDeviceUserCode(userCode, testUserCodeCharset, 8)
	if !ok {
		t.Fatalf("Hydra's user code %q does not normalize", userCode)
	}
	actor := deviceDecisionActor{UserID: testUserID, SessionHash: "session-hash", FlowHandle: newDeviceFlowHandle()}
	claim, err := env.store.claim(t.Context(), normalized, actor.FlowHandle, actor.UserID, actor.SessionHash, time.Minute)
	if err != nil || claim.Code != deviceClaimNew || claim.FlowID != flowID {
		t.Fatalf("claim = %+v, err = %v", claim, err)
	}
	return flowID, normalized, actor
}

// Records whose fields were corrupted are reported as errors rather than
// decoded into zero values.
func TestDeviceFlowStoreRejectsCorruptRecords(t *testing.T) {
	t.Run("poll of a missing flow", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		result, err := env.store.poll(t.Context(), "no-such-flow", testPublicClientID, 60, 30)
		if err != nil || result.Outcome != devicePollMissing {
			t.Fatalf("poll = %+v, err = %v", result, err)
		}
	})
	t.Run("poll with a fractional interval", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		_, flowID := env.issue(testPublicClientID)
		// An early poll slows down to ivl+5, which is not an integer here.
		env.setFlow(flowID, "ivl", "2.5", "lpoll", strconv.FormatInt(env.now().UnixMilli(), 10))
		_, err := env.store.poll(t.Context(), flowID, testPublicClientID, 60, 30)
		if err == nil || !strings.Contains(err.Error(), "invalid slow_down interval") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("poll with a corrupt expiry", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		_, flowID := env.issue(testPublicClientID)
		env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
		env.setFlow(flowID, "exp", "not-a-number")
		_, err := env.store.poll(t.Context(), flowID, testPublicClientID, 60, 30)
		if err == nil || !strings.Contains(err.Error(), "invalid flow expiry") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("claim with corrupt timestamps", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		_, flowID := env.issue(testPublicClientID)
		env.setFlow(flowID, "crt", "yesterday")
		env.hydra.mu.Lock()
		userCode := env.hydra.userCodes[0]
		env.hydra.mu.Unlock()
		_, err := env.store.claim(t.Context(), userCode, newDeviceFlowHandle(), testUserID, "session-hash", time.Minute)
		if err == nil || err.Error() != "invalid device flow timestamps" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("begin approve with a corrupt attempt counter", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		flowID, _, actor := claimedTestFlow(t, env)
		env.setFlow(flowID, "att", "0.5")
		_, _, err := env.store.beginApprove(t.Context(), flowID, actor, env.cfg.approvalTimings(), "nonce")
		if err == nil || !strings.Contains(err.Error(), "invalid approve attempt") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("snapshot of a missing flow", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		snapshot, found, err := env.store.snapshot(t.Context(), "no-such-flow")
		if err != nil || found || snapshot != (deviceFlowSnapshot{}) {
			t.Fatalf("snapshot = %+v, found = %v, err = %v", snapshot, found, err)
		}
	})
}

// The decision scripts round-trip through the store wrappers: a claimed flow
// is approved once, finished, and then refuses a second decision.
func TestDeviceFlowStoreDecisionRoundTrip(t *testing.T) {
	env := newDeviceTestEnv(t)
	flowID, userCode, actor := claimedTestFlow(t, env)
	ctx := t.Context()

	flow, found, err := env.store.snapshot(ctx, flowID)
	if err != nil || !found || flow.ClientID != testPublicClientID || flow.Scope != testDeviceScope {
		t.Fatalf("snapshot = %+v, found = %v, err = %v", flow, found, err)
	}
	if hash, _ := env.store.hashUserCode(userCode); hash != flow.UserCodeHash {
		t.Fatal("the snapshot's user code hash differs from hashUserCode")
	}
	if got, ok, err := env.store.flowIDForHandle(ctx, actor.FlowHandle); err != nil || !ok || got != flowID {
		t.Fatalf("flowIDForHandle = %q %v %v", got, ok, err)
	}
	if _, ok, err := env.store.flowIDForHandle(ctx, newDeviceFlowHandle()); err != nil || ok {
		t.Fatalf("an unknown handle resolved: ok = %v, err = %v", ok, err)
	}

	stranger := actor
	stranger.UserID = testOtherUserID
	if code, _, err := env.store.beginApprove(ctx, flowID, stranger, env.cfg.approvalTimings(), "nonce-x"); err != nil || code != deviceDecisionNotClaimer {
		t.Fatalf("stranger beginApprove = %q, %v", code, err)
	}
	code, attempt, err := env.store.beginApprove(ctx, flowID, actor, env.cfg.approvalTimings(), "nonce-1")
	if err != nil || code != deviceDecisionOK || attempt != 1 {
		t.Fatalf("beginApprove = %q %d %v", code, attempt, err)
	}
	if ok, err := env.store.recordConsent(ctx, flowID, "nonce-other", testConsentRequestID, "kratos-4242"); err != nil || ok {
		t.Fatalf("recordConsent with a stale nonce = %v, %v", ok, err)
	}
	if ok, err := env.store.recordConsent(ctx, flowID, "nonce-1", testConsentRequestID, "kratos-4242"); err != nil || !ok {
		t.Fatalf("recordConsent = %v, %v", ok, err)
	}
	if !env.inUnredeemed(flowID) || env.field(flowID, "crid") != testConsentRequestID {
		t.Fatal("recordConsent did not register the flow for the reaper")
	}
	code, state, err := env.store.finishApprove(ctx, flowID, "nonce-1", deviceFinishApproved, "rack-01", deviceLabelSourceUser, 3)
	if err != nil || code != deviceDecisionOK || state != deviceFlowStateApproved {
		t.Fatalf("finishApprove = %q %q %v", code, state, err)
	}
	if code, crid, err := env.store.deny(ctx, flowID, actor, deviceDenyReasonUser); err != nil || code != deviceDecisionHandled || crid != "" {
		t.Fatalf("deny after approval = %q %q %v", code, crid, err)
	}
	if state, err := env.store.fail(ctx, flowID); err != nil || state != deviceFlowStateApproved {
		t.Fatalf("fail of an approved flow = %q %v", state, err)
	}
	if state, err := env.store.fail(ctx, "no-such-flow"); err != nil || state != "" {
		t.Fatalf("fail of a missing flow = %q %v", state, err)
	}
}
