package sekai

import (
	"context"
	"encoding/base64"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"
	"time"
)

type DataRetriever struct {
	client       *HarukiSekaiClient
	uploadType   harukiUtils.UploadDataType
	logger       *harukiLogger.Logger
	isErrorExist bool
	ErrorMessage string
}

type HarukiSekaiDataRetriever = DataRetriever

func NewSekaiDataRetriever(
	server harukiUtils.SupportedInheritUploadServer,
	inherit harukiUtils.InheritInformation,
	uploadType harukiUtils.UploadDataType,
) *HarukiSekaiDataRetriever {
	client := NewSekaiClientWithConfig(ClientConfig{
		Server:          server,
		API:             Api[server],
		VersionURL:      Version[server],
		Inherit:         inherit,
		Headers:         Headers[server],
		Proxy:           thisProxy,
		InheritJWTToken: InheritJWTToken[server],
	})

	return &HarukiSekaiDataRetriever{
		client:       client,
		uploadType:   uploadType,
		logger:       harukiLogger.NewLogger("SekaiDataRetriever", "DEBUG", nil),
		isErrorExist: false,
		ErrorMessage: "",
	}
}

func (r *HarukiSekaiDataRetriever) RetrieveSuite(ctx context.Context) ([]byte, error) {
	if r.isErrorExist {
		return nil, NewDataRetrievalError("suite", "pre-check", r.ErrorMessage, nil)
	}
	serverName := strings.ToUpper(string(r.client.server))
	r.logger.Infof("%s server retrieving suite...", serverName)
	userIDStr := strconv.FormatInt(r.client.userID, 10)
	basePath := fmt.Sprintf("/suite/user/%s", userIDStr)
	suite, status, err := r.client.callAPI(ctx, basePath, "GET", nil, nil)
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
	time.Sleep(1 * time.Second)
	r.logger.Debugf("%s server making follow-up suite calls...", serverName)
	path := basePath + "?isForceAllReload=false&name=user_colorful_pass,user_colorful_pass_v2,user_offline_event"
	if _, _, err := r.client.callAPI(ctx, path, "GET", nil, nil); err != nil {
		r.logger.Warnf("Follow-up suite call failed (non-critical): %v", err)
	}
	time.Sleep(1 * time.Second)
	if _, _, err := r.client.callAPI(ctx, "/system", "GET", nil, nil); err != nil {
		r.logger.Warnf("System call failed (non-critical): %v", err)
	}
	time.Sleep(1 * time.Second)
	unpacked, err := Unpack(suite, harukiUtils.SupportedDataUploadServer(r.client.server))
	if err != nil {
		r.logger.Errorf("Failed to unpack suite response: %v", err)
		return nil, NewDataRetrievalError("suite", "unpack", "failed to unpack response", err)
	}
	unpackedMap, ok := unpacked.(map[string]interface{})
	if !ok {
		r.logger.Errorf("Unexpected suite response type")
		return nil, NewDataRetrievalError("suite", "parse", "unexpected response type", nil)
	}
	hasFriends := false
	if f, ok := unpackedMap["userFriends"]; ok && f != nil {
		hasFriends = true
	}
	if err := r.RefreshHome(ctx, hasFriends, r.client.loginBonus); err != nil {
		r.logger.Warnf("RefreshHome failed (non-critical): %v", err)
	}
	if status == 200 {
		r.logger.Infof("%s server retrieved suite successfully.", serverName)
		return suite, nil
	}
	r.logger.Errorf("Suite API returned non-200 status: %d", status)
	return nil, NewDataRetrievalError("suite", "status", fmt.Sprintf("unexpected status code: %d", status), nil)
}

func (r *HarukiSekaiDataRetriever) RefreshHome(ctx context.Context, friends bool, login bool) error {
	if r.isErrorExist {
		return NewDataRetrievalError("home", "pre-check", r.ErrorMessage, nil)
	}
	serverName := strings.ToUpper(string(r.client.server))
	r.logger.Infof("%s server refreshing home...", serverName)
	userIDStr := strconv.FormatInt(r.client.userID, 10)
	var lastErr error
	if friends {
		if _, _, err := r.client.callAPI(ctx, fmt.Sprintf("/user/%s/invitation", userIDStr), "GET", nil, nil); err != nil {
			lastErr = err
			r.logger.Warnf("Invitation call failed: %v", err)
		}
	}
	if _, _, err := r.client.callAPI(ctx, "/system", "GET", nil, nil); err != nil {
		lastErr = err
		r.logger.Warnf("System call failed: %v", err)
	}
	if _, _, err := r.client.callAPI(ctx, "/information", "GET", nil, nil); err != nil {
		lastErr = err
		r.logger.Warnf("Information call failed: %v", err)
	}
	refreshPath := fmt.Sprintf("/user/%s/home/refresh", userIDStr)
	var refreshData map[string]interface{}
	if login {
		refreshData = RequestDataRefreshLogin
	} else {
		refreshData = RequestDataRefresh
	}
	data, err := Pack(refreshData, harukiUtils.SupportedDataUploadServer(r.client.server))
	if err != nil {
		r.logger.Warnf("Failed to pack refresh data: %v", err)
		return NewDataRetrievalError("home", "pack", "failed to pack refresh data", err)
	}
	if _, _, err := r.client.callAPI(ctx, refreshPath, "PUT", data, nil); err != nil {
		lastErr = err
		r.logger.Warnf("Home refresh call failed: %v", err)
	}
	r.logger.Infof("%s server home refresh completed.", serverName)
	if lastErr != nil {
		return NewDataRetrievalError("home", "refresh", "some refresh calls failed", lastErr)
	}
	return nil
}

func (r *HarukiSekaiDataRetriever) RetrieveMysekai(ctx context.Context) ([]byte, error) {
	if r.isErrorExist {
		return nil, NewDataRetrievalError("mysekai", "pre-check", r.ErrorMessage, nil)
	}
	serverName := strings.ToUpper(string(r.client.server))
	r.logger.Infof("%s server checking MySekai availability...", serverName)
	resp, status, err := r.client.callAPI(ctx, "/module-maintenance/MYSEKAI", "GET", nil, nil)
	if err != nil {
		r.logger.Warnf("MySekai maintenance check failed: %v", err)
		return nil, NewDataRetrievalError("mysekai", "maintenance_check", "failed to check maintenance status", err)
	}
	if status != 200 {
		return nil, NewDataRetrievalError("mysekai", "maintenance_check", fmt.Sprintf("unexpected status: %d", status), nil)
	}
	unpacked, err := Unpack(resp, harukiUtils.SupportedDataUploadServer(r.client.server))
	if err != nil {
		r.logger.Warnf("Failed to unpack maintenance response: %v", err)
	} else if m, ok := unpacked.(map[string]interface{}); ok && m["isOngoing"] == true {
		r.logger.Infof("MySekai is under maintenance")
		return nil, ErrMaintenance
	}
	resp, _, err = r.client.callAPI(ctx, "/module-maintenance/MYSEKAI_ROOM", "GET", nil, nil)
	if err != nil {
		r.logger.Warnf("MySekai Room maintenance check failed: %v", err)
	} else {
		unpacked, err = Unpack(resp, harukiUtils.SupportedDataUploadServer(r.client.server))
		if err != nil {
			r.logger.Warnf("Failed to unpack room maintenance response: %v", err)
		} else if m, ok := unpacked.(map[string]interface{}); ok && m["isOngoing"] == true {
			r.logger.Infof("MySekai Room is under maintenance")
			return nil, ErrMaintenance
		}
	}
	r.logger.Infof("%s server retrieving MySekai data...", serverName)
	userIDStr := strconv.FormatInt(r.client.userID, 10)
	general, err := base64.StdEncoding.DecodeString(RequestDataGeneral)
	if err != nil {
		return nil, NewDataRetrievalError("mysekai", "decode", "failed to decode request data", err)
	}
	mysekaiPath := fmt.Sprintf("/user/%s/mysekai?isForceAllReloadOnlyMySekai=True", userIDStr)
	mysekai, status, err := r.client.callAPI(ctx, mysekaiPath, "POST", general, nil)
	if err != nil {
		r.logger.Errorf("MySekai API call failed: %v", err)
		return nil, NewDataRetrievalError("mysekai", "api_call", "failed to call MySekai API", err)
	}
	roomReq, err := Pack(RequestDataMySekaiRoom, harukiUtils.SupportedDataUploadServer(r.client.server))
	if err != nil {
		r.logger.Warnf("Failed to pack room request: %v", err)
	} else {
		roomPath := fmt.Sprintf("/user/%s/mysekai/%s/room", userIDStr, userIDStr)
		if _, _, err := r.client.callAPI(ctx, roomPath, "POST", roomReq, nil); err != nil {
			r.logger.Warnf("Room call failed (non-critical): %v", err)
		}
	}
	diarkisPath := fmt.Sprintf("/user/%s/diarkis-auth?diarkisServerType=mysekai", userIDStr)
	if _, _, err := r.client.callAPI(ctx, diarkisPath, "GET", nil, nil); err != nil {
		r.logger.Warnf("Diarkis auth call failed (non-critical): %v", err)
	}
	if status == 200 {
		r.logger.Infof("%s server retrieved MySekai data successfully.", serverName)
		return mysekai, nil
	}
	r.logger.Errorf("MySekai API returned non-200 status: %d", status)
	return nil, NewDataRetrievalError("mysekai", "status", fmt.Sprintf("unexpected status code: %d", status), nil)
}

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
		// Continue to try other data retrieval
	}
	if err := r.RefreshHome(ctx, false, false); err != nil {
		r.logger.Warnf("Final home refresh failed (non-critical): %v", err)
	}
	var mysekai []byte
	var mysekaiErr error
	if r.uploadType == harukiUtils.UploadDataTypeMysekai {
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
