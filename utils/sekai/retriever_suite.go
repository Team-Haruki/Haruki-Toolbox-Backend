package sekai

import (
	"context"
	"fmt"
	"time"
)

func (r *HarukiSekaiDataRetriever) RetrieveSuite(ctx context.Context) ([]byte, error) {
	if err := r.ensureReady("suite", "pre-check"); err != nil {
		return nil, err
	}

	serverName := upperServerName(r.client.server)
	r.logger.Infof("%s server retrieving suite...", serverName)

	basePath := suiteBasePath(r.client.userID)
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

	r.runSuiteFollowupCalls(ctx)

	unpackedMap, err := unpackResponseToMap(suite, r.client.server)
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

func (r *HarukiSekaiDataRetriever) runSuiteFollowupCalls(ctx context.Context) {
	retrieverSleep(1 * time.Second)
	r.logger.Debugf("%s server making follow-up suite calls...", upperServerName(r.client.server))

	if err := callAndIgnoreError(ctx, r.client, suiteFollowupPath(r.client.userID), httpMethodGet, nil); err != nil {
		r.logger.Warnf("Follow-up suite call failed (non-critical): %v", err)
	}
	retrieverSleep(1 * time.Second)
	if err := callAndIgnoreError(ctx, r.client, retrieverSystemPath, httpMethodGet, nil); err != nil {
		r.logger.Warnf("System call failed (non-critical): %v", err)
	}
	retrieverSleep(1 * time.Second)
}
