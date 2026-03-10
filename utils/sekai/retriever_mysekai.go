package sekai

import (
	"context"
	"fmt"
	harukiUtils "haruki-suite/utils"
)

func (r *HarukiSekaiDataRetriever) RetrieveMysekai(ctx context.Context) ([]byte, error) {
	if err := r.ensureReady("mysekai", "pre-check"); err != nil {
		return nil, err
	}

	serverName := upperServerName(r.client.server)
	r.logger.Infof("%s server checking MySekai availability...", serverName)

	if err := r.ensureMysekaiNotInMaintenance(ctx); err != nil {
		return nil, err
	}

	r.logger.Infof("%s server retrieving MySekai data...", serverName)
	general, err := decodeGeneralRequestData()
	if err != nil {
		return nil, NewDataRetrievalError("mysekai", "decode", "failed to decode request data", err)
	}

	mysekai, status, err := r.client.callAPI(ctx, mysekaiPath(r.client.userID), httpMethodPost, general, nil)
	if err != nil {
		r.logger.Errorf("MySekai API call failed: %v", err)
		return nil, NewDataRetrievalError("mysekai", "api_call", "failed to call MySekai API", err)
	}

	r.runMysekaiFollowupCalls(ctx)

	if status == statusCodeOK {
		r.logger.Infof("%s server retrieved MySekai data successfully.", serverName)
		return mysekai, nil
	}

	r.logger.Errorf("MySekai API returned non-200 status: %d", status)
	return nil, NewDataRetrievalError("mysekai", "status", fmt.Sprintf("unexpected status code: %d", status), nil)
}

func (r *HarukiSekaiDataRetriever) ensureMysekaiNotInMaintenance(ctx context.Context) error {
	if err := r.checkModuleMaintenance(ctx, moduleMySekai, "MySekai", true); err != nil {
		return err
	}
	if err := r.checkModuleMaintenance(ctx, moduleMySekaiRoom, "MySekai Room", false); err != nil {
		return err
	}
	return nil
}

func (r *HarukiSekaiDataRetriever) runMysekaiFollowupCalls(ctx context.Context) {
	roomReq, err := Pack(RequestDataMySekaiRoom, harukiUtils.SupportedDataUploadServer(r.client.server))
	if err != nil {
		r.logger.Warnf("Failed to pack room request: %v", err)
	} else if err := callAndIgnoreError(ctx, r.client, mysekaiRoomPath(r.client.userID), httpMethodPost, roomReq); err != nil {
		r.logger.Warnf("Room call failed (non-critical): %v", err)
	}

	if err := callAndIgnoreError(ctx, r.client, mysekaiDiarkisPath(r.client.userID), httpMethodGet, nil); err != nil {
		r.logger.Warnf("Diarkis auth call failed (non-critical): %v", err)
	}
}

func (r *HarukiSekaiDataRetriever) checkModuleMaintenance(
	ctx context.Context,
	module string,
	displayName string,
	required bool,
) error {
	resp, status, err := r.client.callAPI(ctx, moduleMaintenancePath(module), httpMethodGet, nil, nil)
	if err != nil {
		if required {
			r.logger.Warnf("%s maintenance check failed: %v", displayName, err)
			return NewDataRetrievalError("mysekai", "maintenance_check", "failed to check maintenance status", err)
		}
		r.logger.Warnf("%s maintenance check failed: %v", displayName, err)
		return nil
	}
	if status != statusCodeOK {
		return NewDataRetrievalError("mysekai", "maintenance_check", fmt.Sprintf("unexpected status: %d", status), nil)
	}

	ongoing, err := checkMaintenanceFromBody(resp, r.client.server)
	if err != nil {
		r.logger.Warnf("Failed to unpack %s maintenance response: %v", displayName, err)
		return nil
	}
	if ongoing {
		r.logger.Infof("%s is under maintenance", displayName)
		return ErrMaintenance
	}
	return nil
}
