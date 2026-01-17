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

type HarukiSekaiDataRetriever struct {
	client       *HarukiSekaiClient
	uploadType   harukiUtils.UploadDataType
	logger       *harukiLogger.Logger
	isErrorExist bool
	ErrorMessage string
}

func NewSekaiDataRetriever(
	server harukiUtils.SupportedInheritUploadServer,
	inherit harukiUtils.InheritInformation,
	uploadType harukiUtils.UploadDataType,
) *HarukiSekaiDataRetriever {
	client := NewSekaiClient(struct {
		Server          harukiUtils.SupportedInheritUploadServer
		API             string
		VersionURL      string
		Inherit         harukiUtils.InheritInformation
		Headers         map[string]string
		Proxy           string
		InheritJWTToken string
	}{
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
		return nil, fmt.Errorf(r.ErrorMessage)
	}
	r.logger.Infof("%s server retrieving suite...", strings.ToUpper(string(r.client.server)))
	basePath := fmt.Sprintf("/suite/user/%s", strconv.FormatInt(r.client.userID, 10))

	suite, status, err := r.client.callAPI(ctx, basePath, "GET", nil, nil)
	if err != nil {
		r.logger.Errorf("Failed to call suite API: %v", err)
		return nil, err
	}
	if suite == nil {
		r.isErrorExist = true
		r.ErrorMessage = "failed to retrieve suite, API response timeout."
		r.logger.Errorf(r.ErrorMessage)
		return nil, fmt.Errorf(r.ErrorMessage)
	}

	time.Sleep(1 * time.Second)
	r.logger.Infof("%s server calling suite...", strings.ToUpper(string(r.client.server)))

	path := basePath + "?isForceAllReload=false&name=user_colorful_pass,user_colorful_pass_v2,user_offline_event"
	_, _, _ = r.client.callAPI(ctx, path, "GET", nil, nil)
	time.Sleep(1 * time.Second)
	_, _, _ = r.client.callAPI(ctx, "/system", "GET", nil, nil)
	time.Sleep(1 * time.Second)

	unpacked, err := Unpack(suite, harukiUtils.SupportedDataUploadServer(r.client.server))
	if err != nil {
		r.logger.Errorf("Failed to unpack suite response: %v", err)
		return nil, err
	}
	unpackedMap, ok := unpacked.(map[string]interface{})
	if !ok {
		r.logger.Errorf("Unexpected unpack type for suite response")
		return nil, fmt.Errorf("unexpected unpack type")
	}

	friend := false
	if f, ok := unpackedMap["userFriends"]; ok && f != nil {
		friend = true
	}

	if r.client.loginBonus {
		if friend {
			_ = r.RefreshHome(ctx, true, true)
		} else {
			_ = r.RefreshHome(ctx, false, true)
		}
	} else {
		if friend {
			_ = r.RefreshHome(ctx, true, false)
		} else {
			_ = r.RefreshHome(ctx, false, false)
		}
	}

	if status == 200 {
		r.logger.Infof("%s server retrieved suite.", strings.ToUpper(string(r.client.server)))
		return suite, nil
	}
	r.logger.Errorf("Suite API returned non-200 status: %d", status)
	return nil, fmt.Errorf("suite api returned non-200 status")
}

func (r *HarukiSekaiDataRetriever) RefreshHome(ctx context.Context, friends bool, login bool) error {
	if r.isErrorExist {
		return fmt.Errorf(r.ErrorMessage)
	}
	r.logger.Infof("%s server refreshing home...", strings.ToUpper(string(r.client.server)))

	if friends {
		_, _, _ = r.client.callAPI(ctx, fmt.Sprintf("/user/%s/invitation", strconv.FormatInt(r.client.userID, 10)), "GET", nil, nil)
		_, _, _ = r.client.callAPI(ctx, "/system", "GET", nil, nil)
		_, _, _ = r.client.callAPI(ctx, "/information", "GET", nil, nil)
	} else {
		_, _, _ = r.client.callAPI(ctx, "/system", "GET", nil, nil)
		_, _, _ = r.client.callAPI(ctx, "/information", "GET", nil, nil)
	}

	refreshPath := fmt.Sprintf("/user/%s/home/refresh", strconv.FormatInt(r.client.userID, 10))
	if login {
		data, _ := Pack(RequestDataRefreshLogin, harukiUtils.SupportedDataUploadServer(r.client.server))
		_, _, _ = r.client.callAPI(ctx, refreshPath, "PUT", data, nil)
	} else {
		data, _ := Pack(RequestDataRefresh, harukiUtils.SupportedDataUploadServer(r.client.server))
		_, _, _ = r.client.callAPI(ctx, refreshPath, "PUT", data, nil)
	}
	return nil
}

func (r *HarukiSekaiDataRetriever) RetrieveMysekai(ctx context.Context) ([]byte, error) {
	if r.isErrorExist {
		return nil, fmt.Errorf(r.ErrorMessage)
	}

	r.logger.Infof("%s server checking MySekai availability...", strings.ToUpper(string(r.client.server)))
	resp, status, err := r.client.callAPI(ctx, "/module-maintenance/MYSEKAI", "GET", nil, nil)
	if err != nil || status != 200 {
		r.logger.Warnf("MySekai maintenance check failed or status not 200: %v, status: %d", err, status)
		return nil, err
	}
	unpacked, _ := Unpack(resp, harukiUtils.SupportedDataUploadServer(r.client.server))
	if m, ok := unpacked.(map[string]interface{}); ok && m["isOngoing"] == true {
		r.logger.Infof("MySekai is under maintenance")
		return nil, nil
	}

	resp, _, _ = r.client.callAPI(ctx, "/module-maintenance/MYSEKAI_ROOM", "GET", nil, nil)
	unpacked, _ = Unpack(resp, harukiUtils.SupportedDataUploadServer(r.client.server))
	if m, ok := unpacked.(map[string]interface{}); ok && m["isOngoing"] == true {
		r.logger.Infof("MySekai Room is under maintenance")
		return nil, nil
	}

	r.logger.Infof("%s server retrieving MySekai data...", strings.ToUpper(string(r.client.server)))
	general, _ := base64.StdEncoding.DecodeString(RequestDataGeneral)
	mysekai, status, _ := r.client.callAPI(ctx,
		fmt.Sprintf("/user/%s/mysekai?isForceAllReloadOnlyMySekai=True", strconv.FormatInt(r.client.userID, 10)),
		"POST", general, nil)

	roomReq, _ := Pack(RequestDataMySekaiRoom, harukiUtils.SupportedDataUploadServer(r.client.server))
	_, _, _ = r.client.callAPI(ctx,
		fmt.Sprintf("/user/%s/mysekai/%s/room", strconv.FormatInt(r.client.userID, 10), strconv.FormatInt(r.client.userID, 10)),
		"POST", roomReq, nil)

	_, _, _ = r.client.callAPI(ctx,
		fmt.Sprintf("/user/%s/diarkis-auth?diarkisServerType=mysekai", strconv.FormatInt(r.client.userID, 10)),
		"Get", nil, nil)

	if status == 200 {
		r.logger.Infof("%s server retrieved MySekai data.", strings.ToUpper(string(r.client.server)))
		return mysekai, nil
	}
	r.logger.Errorf("Failed to retrieve MySekai data, status: %d", status)
	return nil, fmt.Errorf("failed to retrieve mysekai")
}

func (r *HarukiSekaiDataRetriever) Run(ctx context.Context) (*harukiUtils.SekaiInheritDataRetrieverResponse, error) {
	if err := r.client.Init(ctx); err != nil {
		r.isErrorExist = true
		r.ErrorMessage = err.Error()
		r.logger.Errorf("Client init failed: %v", err)
		return nil, err
	}
	if r.client.isErrorExist {
		r.isErrorExist = true
		r.ErrorMessage = r.client.errorMessage
		r.logger.Errorf("Client has error: %s", r.client.errorMessage)
		return nil, fmt.Errorf(r.ErrorMessage)
	}

	suite, _ := r.RetrieveSuite(ctx)
	_ = r.RefreshHome(ctx, false, false)

	var mysekai []byte
	if r.uploadType == harukiUtils.UploadDataTypeMysekai {
		mysekai, _ = r.RetrieveMysekai(ctx)
	}

	return &harukiUtils.SekaiInheritDataRetrieverResponse{
		Server:  string(r.client.server),
		UserID:  r.client.userID,
		Suite:   suite,
		Mysekai: mysekai,
	}, nil
}
