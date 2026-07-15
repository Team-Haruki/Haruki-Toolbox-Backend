package sekai

import (
	"context"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
)

func (r *HarukiSekaiDataRetriever) Run(ctx context.Context) (*harukiUtils.SekaiInheritDataRetrieverResponse, error) {
	// Bound the entire inherit sequence so a degraded upstream cannot hang the
	// caller for the unbounded sum of every per-call timeout. This does not make
	// the handler asynchronous (a separate change) — it only caps how long one
	// inherit can occupy the request goroutine.
	ctx, cancel := context.WithTimeout(ctx, retrieverRunTimeout)
	defer cancel()
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

	result := &harukiUtils.SekaiInheritDataRetrieverResponse{
		Server: string(r.client.server),
		UserID: r.client.userID,
	}

	suite, suiteErr := r.RetrieveSuite(ctx)
	if suiteErr != nil {
		r.logger.Errorf("Suite retrieval failed: %v", suiteErr)
		return result, suiteErr
	}
	result.Suite = suite

	if err := r.RefreshHome(ctx, false, false); err != nil {
		r.logger.Warnf("Final home refresh failed (non-critical): %v", err)
	}

	if shouldRetrieveMysekai(r.uploadType) {
		mysekai, mysekaiErr := r.RetrieveMysekai(ctx)
		if mysekaiErr != nil {
			r.logger.Errorf("MySekai retrieval failed: %v", mysekaiErr)
			return result, mysekaiErr
		}
		result.Mysekai = mysekai
	}

	return result, nil
}
