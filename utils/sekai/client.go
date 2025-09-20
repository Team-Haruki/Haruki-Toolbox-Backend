package sekai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Client struct {
	server       harukiUtils.SupportedInheritUploadServer
	api          string
	versionURL   string
	inherit      harukiUtils.InheritInformation
	headers      map[string]string
	proxy        string
	userID       int64
	credential   string
	loginBonus   bool
	isErrorExist bool
	errorMessage string
	jpJWTSecret  string
	enJWTSecret  string

	http   *resty.Client
	logger *harukiLogger.Logger
}

func NewSekaiClient(cfg struct {
	Server      harukiUtils.SupportedInheritUploadServer
	API         string
	VersionURL  string
	Inherit     harukiUtils.InheritInformation
	Headers     map[string]string
	Proxy       string
	JPJWTSecret string
	ENJWTSecret string
}) *Client {
	http := resty.New().
		SetTimeout(60*time.Second).
		SetHeader("Content-Type", "application/octet-stream")

	if cfg.Proxy != "" {
		http.SetProxy(cfg.Proxy)
	}

	return &Client{
		server:      cfg.Server,
		api:         cfg.API,
		versionURL:  cfg.VersionURL,
		inherit:     cfg.Inherit,
		headers:     cfg.Headers,
		proxy:       cfg.Proxy,
		jpJWTSecret: cfg.JPJWTSecret,
		enJWTSecret: cfg.ENJWTSecret,
		http:        http,
		logger:      harukiLogger.NewLogger("SekaiClient", "DEBUG", nil),
	}
}

func (c *Client) getCookies(ctx context.Context, retries int) error {
	if c.server != harukiUtils.SupportedInheritUploadServerJP {
		return nil
	}

	c.logger.Infof("Parsing JP server cookies...")
	url := "https://issue.sekai.colorfulpalette.org/api/signature"
	for i := 0; i < retries; i++ {
		resp, err := c.http.R().
			SetContext(ctx).
			Post(url)
		if err != nil {
			c.logger.Warnf("Resty returned error while parsing cookies: %v, retrying...", err)
			continue
		}
		if resp.StatusCode() == 200 {
			if cookie := resp.Header().Get("Set-Cookie"); cookie != "" {
				c.headers["Cookie"] = cookie
				c.logger.Infof("JP server cookies parsed.")
				return nil
			}
			c.logger.Errorf("Failed to parse JP server cookies, empty Set-Cookie header")
			continue
		}
		c.logger.Errorf("Failed to parse JP server cookies, status=%d", resp.StatusCode())
	}
	c.isErrorExist = true
	c.errorMessage = "failed to parse cookies after retries"
	return fmt.Errorf(c.errorMessage)
}

func (c *Client) parseAppVersion(ctx context.Context, retries int) error {
	if c.isErrorExist {
		return fmt.Errorf("client error while parsing cookies")
	}

	c.logger.Infof("Parsing %s server app version...", strings.ToUpper(string(c.server)))
	for i := 0; i < retries; i++ {
		resp, err := c.http.SetTimeout(5).R().
			SetContext(ctx).
			Get(c.versionURL)
		if err != nil {
			c.logger.Warnf("Resty returned error while parsing %s server version: %v, retrying...", strings.ToUpper(string(c.server)), err)
			continue
		}
		if resp.StatusCode() == 200 {
			var data struct {
				AppVersion   string `json:"appVersion"`
				AppHash      string `json:"appHash"`
				DataVersion  string `json:"dataVersion"`
				AssetVersion string `json:"assetVersion"`
			}
			if err := json.Unmarshal(resp.Body(), &data); err != nil {
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
		c.logger.Errorf("Game version API returned error, status=%d", resp.StatusCode())
	}
	c.isErrorExist = true
	c.errorMessage = "failed to parse game version after retries"
	return fmt.Errorf(c.errorMessage)
}

func (c *Client) generateInheritToken() (string, error) {
	inheritPayload := jwt.MapClaims{
		"inheritId": c.inherit.InheritID,
		"password":  c.inherit.InheritPassword,
	}

	secret := ""
	if c.server == harukiUtils.SupportedInheritUploadServerJP {
		secret = c.jpJWTSecret
	} else {
		secret = c.enJWTSecret
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, inheritPayload)
	return token.SignedString([]byte(secret))
}

func (c *Client) callAPI(ctx context.Context, path, method string, body []byte, customHeaders map[string]string) ([]byte, int, error) {
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

	req := c.http.R().
		SetContext(ctx).
		SetHeaders(headers).
		SetBody(body)

	url := c.api + path
	resp, err := req.Execute(method, url)
	if err != nil {
		return nil, 0, err
	}

	if resp.StatusCode() == 200 {
		if st := resp.Header().Get("X-Session-Token"); st != "" {
			c.headers["X-Session-Token"] = st
		}
		if resp.Header().Get("X-Login-Bonus-Status") == "true" {
			c.loginBonus = true
		}
		return resp.Body(), resp.StatusCode(), nil
	}

	return resp.Body(), resp.StatusCode(), fmt.Errorf("API error: %d", resp.StatusCode())
}

func (c *Client) InheritAccount(ctx context.Context, returnUserID bool) error {
	c.logger.Infof(" %s Server Sekai Client generating inherit token...", strings.ToUpper(string(c.server)))
	token, err := c.generateInheritToken()
	if err != nil {
		return err
	}
	headers := map[string]string{"x-inherit-id-verify-token": token}
	c.logger.Infof(" %s Server Sekai Client generated inherit token.", strings.ToUpper(string(c.server)))

	c.logger.Infof(" %s Server Sekai Client inheriting account...", strings.ToUpper(string(c.server)))
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
		return err
	}
	unpacked, ok := unpackedAny.(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected unpack result type")
	}

	if returnUserID {
		if after, ok := unpacked["afterUserGamedata"].(map[string]interface{}); ok {
			if uid, ok := after["userId"].(int64); ok {
				c.userID = uid
				c.logger.Infof(" %s Server Sekai Client retrieved user ID.", strings.ToUpper(string(c.server)))
				return nil
			}
		}
		return fmt.Errorf("failed to get userId")
	} else {
		if cred, ok := unpacked["credential"].(string); ok {
			c.credential = cred
			c.logger.Infof(" %s Server Sekai Client retrieved user credential.", strings.ToUpper(string(c.server)))
			return nil
		}
		return fmt.Errorf("failed to get credential")
	}
}

func (c *Client) Login(ctx context.Context) error {
	if c.credential == "" {
		return fmt.Errorf("inherit failed")
	}

	c.logger.Infof(" %s Server Sekai Client logging in...", strings.ToUpper(string(c.server)))
	body := map[string]any{
		"credential": c.credential,
		"deviceId":   nil,
	}
	packed, err := Pack(body, harukiUtils.SupportedDataUploadServer(c.server))
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/user/%s/auth?refreshUpdatedResources=False", strconv.FormatInt(c.userID, 10))
	resp, status, err := c.callAPI(ctx, path, "PUT", packed, nil)
	if err != nil {
		return err
	}
	if status == 403 {
		return fmt.Errorf("account login failed, status=403")
	}

	unpackedAny, err := Unpack(resp, harukiUtils.SupportedDataUploadServer(c.server))
	if err != nil {
		return err
	}
	unpacked, ok := unpackedAny.(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected unpack result type")
	}

	if st, ok := unpacked["sessionToken"].(string); ok {
		c.headers["X-Session-Token"] = st
		c.logger.Infof(" %s Server Sekai Client logged in.", strings.ToUpper(string(c.server)))
		return nil
	}
	return fmt.Errorf("login response missing sessionToken")
}

func (c *Client) Init(ctx context.Context) error {
	if err := c.getCookies(ctx, 4); err != nil {
		return err
	}
	if err := c.parseAppVersion(ctx, 4); err != nil {
		return err
	}
	if err := c.InheritAccount(ctx, true); err != nil {
		return err
	}
	if err := c.InheritAccount(ctx, false); err != nil {
		return err
	}
	if err := c.Login(ctx); err != nil {
		return err
	}
	return nil
}

func (c *Client) Close() error {
	return nil
}
