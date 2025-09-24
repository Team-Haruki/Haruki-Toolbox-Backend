package sekai

import (
	"context"
	"encoding/base64"
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"
	"time"
)

const (
	JP harukiUtils.SupportedInheritUploadServer = "jp"
	EN harukiUtils.SupportedInheritUploadServer = "en"
)

var thisProxy = harukiConfig.Cfg.Proxy
var (
	Api = map[harukiUtils.SupportedInheritUploadServer]string{
		JP: fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.JPServerAPIHost),
		EN: fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.ENServerAPIHost),
	}

	Headers = map[harukiUtils.SupportedInheritUploadServer]map[string]string{
		JP: harukiConfig.Cfg.SekaiClient.JPServerInheritClientHeaders,
		EN: harukiConfig.Cfg.SekaiClient.ENServerInheritClientHeaders,
	}

	Version = map[harukiUtils.SupportedInheritUploadServer]string{
		JP: harukiConfig.Cfg.SekaiClient.JPServerAppVersionUrl,
		EN: harukiConfig.Cfg.SekaiClient.ENServerAppVersionUrl,
	}

	InheritJWTToken = map[harukiUtils.SupportedInheritUploadServer]string{
		JP: harukiConfig.Cfg.SekaiClient.JPServerInheritToken,
		EN: harukiConfig.Cfg.SekaiClient.ENServerInheritToken,
	}
)

type HarukiSekaiDataRetriever struct {
	client       *Client
	policy       harukiUtils.UploadPolicy
	uploadType   harukiUtils.UploadDataType
	logger       *harukiLogger.Logger
	isErrorExist bool
	ErrorMessage string
}

func NewSekaiDataRetriever(
	server harukiUtils.SupportedInheritUploadServer,
	inherit harukiUtils.InheritInformation,
	policy harukiUtils.UploadPolicy,
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
		policy:       policy,
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
		return nil, err
	}
	if suite == nil {
		r.isErrorExist = true
		r.ErrorMessage = "failed to retrieve suite, API response timeout."
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
		return nil, err
	}
	unpackedMap, ok := unpacked.(map[string]interface{})
	if !ok {
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
	return nil, fmt.Errorf("suite api returned non-200 status")
}

func (r *HarukiSekaiDataRetriever) RefreshHome(ctx context.Context, friends bool, login bool) error {
	if r.isErrorExist {
		return fmt.Errorf(r.ErrorMessage)
	}
	r.logger.Infof("%s server refreshing home...", strings.ToUpper(string(r.client.server)))

	if friends {
		r.client.callAPI(ctx, fmt.Sprintf("/user/%s/invitation", strconv.FormatInt(r.client.userID, 10)), "GET", nil, nil)
		r.client.callAPI(ctx, "/system", "GET", nil, nil)
		r.client.callAPI(ctx, "/information", "GET", nil, nil)
	} else {
		r.client.callAPI(ctx, "/system", "GET", nil, nil)
		r.client.callAPI(ctx, "/information", "GET", nil, nil)
	}

	refreshPath := fmt.Sprintf("/user/%s/home/refresh", strconv.FormatInt(r.client.userID, 10))
	if login {
		data, _ := Pack(RequestDataRefreshLogin, harukiUtils.SupportedDataUploadServer(r.client.server))
		r.client.callAPI(ctx, refreshPath, "PUT", data, nil)
	} else {
		data, _ := Pack(RequestDataRefresh, harukiUtils.SupportedDataUploadServer(r.client.server))
		r.client.callAPI(ctx, refreshPath, "PUT", data, nil)
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
		return nil, err
	}
	unpacked, _ := Unpack(resp, harukiUtils.SupportedDataUploadServer(r.client.server))
	if m, ok := unpacked.(map[string]interface{}); ok && m["isOngoing"] == true {
		return nil, nil
	}

	resp, status, _ = r.client.callAPI(ctx, "/module-maintenance/MYSEKAI_ROOM", "GET", nil, nil)
	unpacked, _ = Unpack(resp, harukiUtils.SupportedDataUploadServer(r.client.server))
	if m, ok := unpacked.(map[string]interface{}); ok && m["isOngoing"] == true {
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
		"POST", nil, nil)

	if status == 200 {
		r.logger.Infof("%s server retrieved MySekai data.", strings.ToUpper(string(r.client.server)))
		return mysekai, nil
	}
	return nil, fmt.Errorf("failed to retrieve mysekai")
}

func (r *HarukiSekaiDataRetriever) Run(ctx context.Context) (*harukiUtils.SekaiInheritDataRetrieverResponse, error) {
	if err := r.client.Init(ctx); err != nil {
		r.isErrorExist = true
		r.ErrorMessage = err.Error()
		return nil, err
	}
	if r.client.isErrorExist {
		r.isErrorExist = true
		r.ErrorMessage = r.client.errorMessage
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
		Policy:  string(r.policy),
	}, nil
}
