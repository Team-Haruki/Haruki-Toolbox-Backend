package sekai

import (
	"context"
	"fmt"
)

func (r *HarukiSekaiDataRetriever) RetrieveSuite(ctx context.Context) ([]byte, error) {
	if err := r.ensureReady("suite", "pre-check"); err != nil {
		return nil, err
	}

	serverName := upperServerName(r.client.server)
	r.logger.Infof("%s server retrieving suite...", serverName)

	basePath := suiteInitialPath(r.client.server, r.client.userID)
	suite, status, err := r.client.callAPI(ctx, basePath, httpMethodGet, nil, nil)
	if err != nil {
		r.logger.Errorf("Suite API call failed: %v", err)
		return nil, NewDataRetrievalError("suite", "api_call", "failed to call suite API", err)
	}
	if suite == nil {
		r.isErrorExist = true
		r.ErrorMessage = "suite API returned nil response"
		r.logger.Errorf("%s", r.ErrorMessage)
		return nil, NewDataRetrievalError("suite", "api_response", r.ErrorMessage, nil)
	}

	if err := r.runSuiteFollowupCalls(ctx); err != nil {
		r.logger.Warnf("Suite retrieval interrupted: %v", err)
		return nil, NewDataRetrievalError("suite", "pause", "interrupted between suite calls", err)
	}

	unpackedMap, err := unpackResponseToMap(r.client.serverCryptor, suite, r.client.server)
	if err != nil {
		r.logger.Errorf("Failed to unpack suite response: %v", err)
		return nil, NewDataRetrievalError("suite", "unpack", "failed to unpack response", err)
	}

	if err := r.RefreshHome(ctx, hasUserFriends(unpackedMap), r.client.loginBonus); err != nil {
		r.logger.Warnf("RefreshHome failed (non-critical): %v", err)
	}
	if status == statusCodeOK {
		r.logger.Infof("%s server retrieved suite successfully.", serverName)
		return suite, nil
	}

	r.logger.Errorf("Suite API returned non-200 status: %d", status)
	return nil, NewDataRetrievalError("suite", "status", fmt.Sprintf("unexpected status code: %d", status), nil)
}

// runSuiteFollowupCalls makes the calls the real client makes after the suite
// request, paced like it. Their failures are not critical; only the context
// ending during a pause stops the inherit.
func (r *HarukiSekaiDataRetriever) runSuiteFollowupCalls(ctx context.Context) error {
	wait := r.client.pacing.SuiteFollowup
	if err := pause(ctx, wait); err != nil {
		return err
	}
	if err := callAndIgnoreError(ctx, r.client, suiteFollowupPath(r.client.userID), httpMethodGet, nil); err != nil {
		r.logger.Warnf("Follow-up suite call failed (non-critical): %v", err)
	}
	if err := pause(ctx, wait); err != nil {
		return err
	}
	if err := callAndIgnoreError(ctx, r.client, retrieverSystemPath, httpMethodGet, nil); err != nil {
		r.logger.Warnf("System call failed (non-critical): %v", err)
	}
	return pause(ctx, wait)
}
