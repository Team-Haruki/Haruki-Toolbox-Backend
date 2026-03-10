package sekai

import (
	"context"
	harukiUtils "haruki-suite/utils"
)

func (r *HarukiSekaiDataRetriever) RefreshHome(ctx context.Context, friends bool, login bool) error {
	if err := r.ensureReady("home", "pre-check"); err != nil {
		return err
	}

	serverName := upperServerName(r.client.server)
	r.logger.Infof("%s server refreshing home...", serverName)
	var lastErr error

	if friends {
		if err := callAndIgnoreError(ctx, r.client, invitationPath(r.client.userID), httpMethodGet, nil); err != nil {
			lastErr = err
			r.logger.Warnf("Invitation call failed: %v", err)
		}
	}
	if err := callAndIgnoreError(ctx, r.client, retrieverSystemPath, httpMethodGet, nil); err != nil {
		lastErr = err
		r.logger.Warnf("System call failed: %v", err)
	}
	if err := callAndIgnoreError(ctx, r.client, retrieverInformationPath, httpMethodGet, nil); err != nil {
		lastErr = err
		r.logger.Warnf("Information call failed: %v", err)
	}

	refreshData := selectRefreshPayload(login)
	data, err := Pack(refreshData, harukiUtils.SupportedDataUploadServer(r.client.server))
	if err != nil {
		r.logger.Warnf("Failed to pack refresh data: %v", err)
		return NewDataRetrievalError("home", "pack", "failed to pack refresh data", err)
	}
	if err := callAndIgnoreError(ctx, r.client, homeRefreshPath(r.client.userID), httpMethodPut, data); err != nil {
		lastErr = err
		r.logger.Warnf("Home refresh call failed: %v", err)
	}

	r.logger.Infof("%s server home refresh completed.", serverName)
	if lastErr != nil {
		return NewDataRetrievalError("home", "refresh", "some refresh calls failed", lastErr)
	}
	return nil
}
