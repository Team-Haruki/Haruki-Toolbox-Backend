package upload

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
)

// errBirthdaySubscriptionStale marks a notification the receiver refused with
// 409 Conflict because the subscription is no longer current (inactive,
// expired, or a newer version replaced it). Retrying cannot succeed.
var errBirthdaySubscriptionStale = errors.New("birthday subscription is stale")

// maxNotifyErrorBodyBytes bounds how much of a failed response body is kept in
// the error and therefore in logs.
const maxNotifyErrorBodyBytes = 256

// deliverBirthdayEvent notifies the push gateway about a stored event. The
// first attempt runs in the caller; when it fails for any reason other than a
// stale subscription, the remaining attempts are handed to a separate
// background task so the caller returns without waiting for the backoff.
func (h *DataHandler) deliverBirthdayEvent(event *BirthdayMonitorEvent) {
	if event == nil {
		return
	}
	err := h.notifyBirthdayEventAttempt(event)
	if h.finishBirthdayNotifyAttempt(event, 1, err) {
		return
	}
	delays := h.BirthdaySubscription.retryDelays()
	if len(delays) == 0 {
		h.logBirthdayNotifyGaveUp(event, 1, err)
		return
	}
	h.Logger.Infof("birthday subscription notify failed, scheduling retry: event=%s subscription=%s version=%s attempt=1 err=%v", event.EventID, event.SubscriptionID, event.SubscriptionVersion, err)
	if !h.submitBackgroundTask("birthday-subscription-notify-retry", func() {
		h.retryBirthdayEventNotify(event, delays, err)
	}) {
		h.Logger.Warnf("birthday subscription notify retry dropped: shutdown in progress event=%s subscription=%s version=%s attempts=1 err=%v", event.EventID, event.SubscriptionID, event.SubscriptionVersion, err)
	}
}

// retryBirthdayEventNotify waits for each delay in turn and retries the
// notification until it succeeds, the subscription turns out to be stale, or
// the schedule is exhausted. Attempt 1 already happened in the caller and
// failed with err. When the application starts shutting down during a
// backoff, the event is dropped so the retry does not hold up the drain.
func (h *DataHandler) retryBirthdayEventNotify(event *BirthdayMonitorEvent, delays []time.Duration, err error) {
	shutdown := harukiBackground.ShutdownSignal(h.BackgroundTasks)
	attempt := 1
	for i, delay := range delays {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-shutdown:
			timer.Stop()
			h.Logger.Warnf("birthday subscription notify retry dropped: shutdown in progress event=%s subscription=%s version=%s attempts=%d err=%v", event.EventID, event.SubscriptionID, event.SubscriptionVersion, attempt, err)
			return
		}
		attempt++
		err = h.notifyBirthdayEventAttempt(event)
		if h.finishBirthdayNotifyAttempt(event, attempt, err) {
			return
		}
		if i < len(delays)-1 {
			h.Logger.Debugf("birthday subscription notify retry failed: event=%s subscription=%s version=%s attempt=%d err=%v", event.EventID, event.SubscriptionID, event.SubscriptionVersion, attempt, err)
		}
	}
	h.logBirthdayNotifyGaveUp(event, attempt, err)
}

// finishBirthdayNotifyAttempt reports whether the delivery is finished: true
// on success or a stale subscription (logged at Info, since the receiver
// rejected it on purpose), false when the attempt should be retried.
func (h *DataHandler) finishBirthdayNotifyAttempt(event *BirthdayMonitorEvent, attempt int, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, errBirthdaySubscriptionStale):
		h.Logger.Infof("birthday subscription notify dropped: subscription is stale event=%s subscription=%s version=%s attempt=%d", event.EventID, event.SubscriptionID, event.SubscriptionVersion, attempt)
		return true
	default:
		return false
	}
}

func (h *DataHandler) logBirthdayNotifyGaveUp(event *BirthdayMonitorEvent, attempts int, err error) {
	h.Logger.Warnf("birthday subscription notify failed: event=%s subscription=%s version=%s attempts=%d err=%v", event.EventID, event.SubscriptionID, event.SubscriptionVersion, attempts, err)
}

// notifyBirthdayEventAttempt runs one notification with its own timeout, so
// neither the upload's context nor time spent in earlier attempts or backoff
// shortens it.
func (h *DataHandler) notifyBirthdayEventAttempt(event *BirthdayMonitorEvent) error {
	ctx, cancel := context.WithTimeout(context.Background(), h.BirthdaySubscription.timeout())
	defer cancel()
	return h.notifyHMESBirthdayEvent(ctx, event)
}

func (h *DataHandler) notifyHMESBirthdayEvent(ctx context.Context, event *BirthdayMonitorEvent) error {
	cfg := h.BirthdaySubscription
	if event == nil || strings.TrimSpace(cfg.hmesInternalBaseURL) == "" {
		if event != nil {
			h.Logger.Warnf("birthday subscription HMES notify skipped: hmes_internal_base_url is not configured event=%s subscription=%s", event.EventID, event.SubscriptionID)
		}
		return nil
	}
	body, err := json.Marshal(hmesEventNotifyRequest{
		EventID:             event.EventID,
		SubscriptionID:      event.SubscriptionID,
		SubscriptionVersion: event.SubscriptionVersion,
		PayloadRef:          event.PayloadRef,
		EmptyResult:         event.EmptyResult,
	})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(cfg.hmesInternalBaseURL, "/") + "/internal/events"
	status, _, respBody, err := h.HttpClient.Request(ctx, "POST", endpoint, subscriptionJSONHeaders(cfg.hmesInternalToken, cfg.userAgent), body)
	if err != nil {
		return err
	}
	if status == http.StatusConflict {
		return fmt.Errorf("%w: receiver returned status %d: %s", errBirthdaySubscriptionStale, status, truncateNotifyErrorBody(respBody))
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("hmes returned status %d: %s", status, truncateNotifyErrorBody(respBody))
	}
	h.Logger.Infof("birthday subscription HMES notify sent: event=%s subscription=%s version=%s empty_result=%t", event.EventID, event.SubscriptionID, event.SubscriptionVersion, event.EmptyResult)
	return nil
}

func truncateNotifyErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > maxNotifyErrorBodyBytes {
		text = strings.ToValidUTF8(text[:maxNotifyErrorBodyBytes], "") + "..."
	}
	return text
}

func subscriptionHeaders(token, userAgent string) map[string]string {
	headers := map[string]string{
		"User-Agent": strings.TrimSpace(userAgent),
	}
	if headers["User-Agent"] == "" {
		headers["User-Agent"] = "Haruki-Toolbox-Backend"
	}
	if auth := bearerAuth(token); auth != "" {
		headers["Authorization"] = auth
	}
	return headers
}

func subscriptionJSONHeaders(token, userAgent string) map[string]string {
	headers := subscriptionHeaders(token, userAgent)
	headers["Content-Type"] = "application/json"
	return headers
}

func bearerAuth(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return token
	}
	return "Bearer " + token
}
