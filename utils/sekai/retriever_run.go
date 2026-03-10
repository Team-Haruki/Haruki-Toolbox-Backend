package sekai

import (
	"context"
	harukiUtils "haruki-suite/utils"
)

func (r *HarukiSekaiDataRetriever) Run(ctx context.Context) (*harukiUtils.SekaiInheritDataRetrieverResponse, error) {
	if err := r.client.Init(ctx); err != nil {
		r.isErrorExist = true
		r.ErrorMessage = err.Error()
		r.logger.Errorf("Client initialization failed: %v", err)
		return nil, NewDataRetrievalError("run", "init", "client initialization failed", err)
	}
	if r.client.isErrorExist {
		r.isErrorExist = true
		r.ErrorMessage = r.client.errorMessage
		r.logger.Errorf("Client error: %s", r.client.errorMessage)
		return nil, NewDataRetrievalError("run", "client_error", r.client.errorMessage, nil)
	}

	suite, suiteErr := r.RetrieveSuite(ctx)
	if suiteErr != nil {
		r.logger.Warnf("Suite retrieval failed: %v", suiteErr)
	}
	if err := r.RefreshHome(ctx, false, false); err != nil {
		r.logger.Warnf("Final home refresh failed (non-critical): %v", err)
	}

	var mysekai []byte
	var mysekaiErr error
	if shouldRetrieveMysekai(r.uploadType) {
		mysekai, mysekaiErr = r.RetrieveMysekai(ctx)
		if mysekaiErr != nil && !IsMaintenanceError(mysekaiErr) {
			r.logger.Warnf("MySekai retrieval failed: %v", mysekaiErr)
		}
	}

	return &harukiUtils.SekaiInheritDataRetrieverResponse{
		Server:  string(r.client.server),
		UserID:  r.client.userID,
		Suite:   suite,
		Mysekai: mysekai,
	}, nil
}
