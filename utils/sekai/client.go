package sekai

import (
	"context"
	"encoding/base64"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiHttp "haruki-suite/utils/http"
	harukiLogger "haruki-suite/utils/logger"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func NewSekaiClient(cfg struct {
	Server          harukiUtils.SupportedInheritUploadServer
	API             string
	VersionURL      string
	Inherit         harukiUtils.InheritInformation
	Headers         map[string]string
	Proxy           string
	InheritJWTToken string
}) *HarukiSekaiClient {
	http := harukiHttp.NewClient(cfg.Proxy, 15*time.Second)

	return &HarukiSekaiClient{
		server:          cfg.Server,
		api:             cfg.API,
		versionURL:      cfg.VersionURL,
		inherit:         cfg.Inherit,
		headers:         cfg.Headers,
		inheritJWTToken: cfg.InheritJWTToken,
		httpClient:      http,
		logger:          harukiLogger.NewLogger("SekaiClient", "DEBUG", nil),
	}
}

func (c *HarukiSekaiClient) getCookies(ctx context.Context, retries int) error {
	if c.server != harukiUtils.SupportedInheritUploadServerJP {
		return nil
	}

	c.logger.Infof("Parsing JP server cookies...")
	url := "https://issue.sekai.colorfulpalette.org/api/signature"
	for i := 0; i < retries; i++ {
		status, headers, _, err := c.httpClient.Request(ctx, "POST", url, nil, nil)
		if err != nil {
			c.logger.Warnf("HTTP client returned error while parsing cookies: %v, retrying...", err)
			continue
		}
		if status == 200 {
			if cookie, ok := headers["Set-Cookie"]; ok && cookie != "" {
				c.headers["Cookie"] = cookie
				c.logger.Infof("JP server cookies parsed.")
				return nil
			}
			c.logger.Errorf("Failed to parse JP server cookies, empty Set-Cookie header")
			continue
		}
		c.logger.Errorf("Failed to parse JP server cookies, status=%d", status)
	}
	c.isErrorExist = true
	c.errorMessage = "failed to parse cookies after retries"
	c.logger.Errorf(c.errorMessage)
	return fmt.Errorf(c.errorMessage)
}

func (c *HarukiSekaiClient) parseAppVersion(ctx context.Context, retries int) error {
	if c.isErrorExist {
		return fmt.Errorf("client error while parsing cookies")
	}

	c.logger.Infof("Parsing %s server app version...", strings.ToUpper(string(c.server)))
	for i := 0; i < retries; i++ {
		status, _, body, err := c.httpClient.Request(ctx, "GET", c.versionURL, nil, nil)
		if err != nil {
			c.logger.Warnf("HTTP client returned error while parsing %s server version: %v, retrying...", strings.ToUpper(string(c.server)), err)
			continue
		}
		if status == 200 {
			var data struct {
				AppVersion   string `json:"appVersion"`
				AppHash      string `json:"appHash"`
				DataVersion  string `json:"dataVersion"`
				AssetVersion string `json:"assetVersion"`
			}
			if err := sonic.Unmarshal(body, &data); err != nil {
				c.logger.Errorf("Failed to unmarshal %s server app version json: %v", strings.ToUpper(string(c.server)), err)
				continue
			}
			c.headers["X-App-Version"] = data.AppVersion
			c.headers["X-App-Hash"] = data.AppHash
			c.headers["X-Data-Version"] = data.DataVersion
			c.headers["X-Asset-Version"] = data.AssetVersion
			c.logger.Infof("Parsed %s server app version.", strings.ToUpper(string(c.server)))
			return nil
		}
		c.logger.Errorf("Game version API returned error, status=%d", status)
	}
	c.isErrorExist = true
	c.errorMessage = "failed to parse game version after retries"
	c.logger.Errorf(c.errorMessage)
	return fmt.Errorf(c.errorMessage)
}

func (c *HarukiSekaiClient) generateInheritToken() (string, error) {
	inheritPayload := jwt.MapClaims{
		"inheritId": c.inherit.InheritID,
		"password":  c.inherit.InheritPassword,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, inheritPayload)
	return token.SignedString([]byte(c.inheritJWTToken))
}

func (c *HarukiSekaiClient) callAPI(ctx context.Context, path, method string, body []byte, customHeaders map[string]string) ([]byte, int, error) {
	if c.isErrorExist {
		return nil, 0, fmt.Errorf("client in error state: %s", c.errorMessage)
	}

	headers := make(map[string]string)
	for k, v := range c.headers {
		headers[k] = v
	}
	for k, v := range customHeaders {
		headers[k] = v
	}
	headers["X-Request-Id"] = uuid.NewString()

	url := c.api + path
	status, respHeaders, respBody, err := c.httpClient.Request(ctx, method, url, headers, body)
	if err != nil {
		c.logger.Errorf("HTTP request failed for %s: %v", url, err)
		return nil, 0, err
	}

	if status == 200 {
		if st, ok := respHeaders["X-Session-Token"]; ok && st != "" {
			c.headers["X-Session-Token"] = st
		}
		if v, ok := respHeaders["X-Login-Bonus-Status"]; ok && v == "true" {
			c.loginBonus = true
		}
		return respBody, status, nil
	}

	c.logger.Errorf("API returned non-200 status for %s: %d", url, status)
	return respBody, status, fmt.Errorf("API error: %d", status)
}

func (c *HarukiSekaiClient) InheritAccount(ctx context.Context, returnUserID bool) error {
	c.logger.Infof("%s Server Sekai Client generating inherit token...", strings.ToUpper(string(c.server)))
	token, err := c.generateInheritToken()
	if err != nil {
		c.logger.Errorf("Failed to generate inherit token: %v", err)
		return err
	}
	headers := map[string]string{"x-inherit-id-verify-token": token}
	c.logger.Infof("%s Server Sekai Client generated inherit token.", strings.ToUpper(string(c.server)))

	c.logger.Infof("%s Server Sekai Client inheriting account...", strings.ToUpper(string(c.server)))
	path := fmt.Sprintf("/inherit/user/%s?isExecuteInherit=%s",
		c.inherit.InheritID,
		map[bool]string{true: "True", false: "False"}[!returnUserID],
	)

	if c.server == harukiUtils.SupportedInheritUploadServerEN {
		path += "&isAdult=True&tAge=16"
	}

	data, _ := base64.StdEncoding.DecodeString(RequestDataGeneral)

	resp, status, err := c.callAPI(ctx, path, "POST", data, headers)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("inherit account failed, status=%d", status)
	}

	unpackedAny, err := Unpack(resp, harukiUtils.SupportedDataUploadServer(c.server))
	if err != nil {
		c.logger.Errorf("Failed to unpack inherit response: %v", err)
		return err
	}
	unpacked, ok := unpackedAny.(map[string]interface{})
	if !ok {
		c.logger.Errorf("Unexpected unpack result type")
		return fmt.Errorf("unexpected unpack result type")
	}

	if returnUserID {
		if after, ok := unpacked["afterUserGamedata"].(map[string]interface{}); ok {
			if uidVal, exists := after["userId"]; exists {
				switch uid := uidVal.(type) {
				case int64:
					c.userID = uid
					return nil
				case uint64:
					if uid > math.MaxInt64 {
						return fmt.Errorf("userId too large for int64: %v", uid)
					}
					c.userID = int64(uid)
					return nil
				}
			}
		}
		c.logger.Errorf("Failed to get userId from inherit response")
		return fmt.Errorf("failed to get userId")
	}

	if cred, ok := unpacked["credential"].(string); ok {
		c.credential = cred
		c.logger.Infof("%s Server Sekai Client retrieved user credential.", strings.ToUpper(string(c.server)))
		return nil
	}
	c.logger.Errorf("Failed to get credential from inherit response")
	return fmt.Errorf("failed to get credential")
}

func (c *HarukiSekaiClient) Login(ctx context.Context) error {
	if c.credential == "" {
		return fmt.Errorf("inherit failed")
	}

	c.logger.Infof("%s Server Sekai Client logging in...", strings.ToUpper(string(c.server)))
	body := map[string]any{
		"credential":      c.credential,
		"deviceId":        nil,
		"authTriggerType": "normal",
	}
	packed, err := Pack(body, harukiUtils.SupportedDataUploadServer(c.server))
	if err != nil {
		c.logger.Errorf("Failed to pack login request: %v", err)
		return err
	}

	path := fmt.Sprintf("/user/%s/auth?refreshUpdatedResources=False", strconv.FormatInt(c.userID, 10))
	resp, status, err := c.callAPI(ctx, path, "PUT", packed, nil)
	if err != nil {
		return err
	}
	if status == 403 {
		c.logger.Errorf("Account login failed, status=403")
		return fmt.Errorf("account login failed, status=403")
	}

	unpackedAny, err := Unpack(resp, harukiUtils.SupportedDataUploadServer(c.server))
	if err != nil {
		c.logger.Errorf("Failed to unpack login response: %v", err)
		return err
	}
	unpacked, ok := unpackedAny.(map[string]interface{})
	if !ok {
		c.logger.Errorf("Unexpected unpack result type")
		return fmt.Errorf("unexpected unpack result type")
	}

	if st, ok := unpacked["sessionToken"].(string); ok {
		c.headers["X-Session-Token"] = st
		c.logger.Infof("%s Server Sekai Client logged in.", strings.ToUpper(string(c.server)))
		return nil
	}
	c.logger.Errorf("Login response missing sessionToken")
	return fmt.Errorf("login response missing sessionToken")
}

func (c *HarukiSekaiClient) Init(ctx context.Context) error {
	if err := c.getCookies(ctx, 4); err != nil {
		return err
	}
	if err := c.parseAppVersion(ctx, 4); err != nil {
		return err
	}
	if err := c.InheritAccount(ctx, true); err != nil {
		return err
	}
	time.Sleep(1 * time.Second)
	if err := c.InheritAccount(ctx, false); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	if err := c.Login(ctx); err != nil {
		return err
	}
	return nil
}
