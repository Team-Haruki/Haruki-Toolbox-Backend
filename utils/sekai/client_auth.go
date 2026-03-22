package sekai

import (
	"context"
	"encoding/base64"
	"fmt"
	harukiUtils "haruki-suite/utils"
	"math"
	"strconv"
	"strings"
)

func (c *HarukiSekaiClient) InheritAccount(ctx context.Context, returnUserID bool) error {
	c.logger.Infof("%s Server Sekai Client generating inherit token...", strings.ToUpper(string(c.server)))
	token, err := c.generateInheritToken()
	if err != nil {
		c.logger.Errorf("Failed to generate inherit token: %v", err)
		return err
	}

	headers := map[string]string{headerInheritIDToken: token}
	c.logger.Infof("%s Server Sekai Client generated inherit token.", strings.ToUpper(string(c.server)))
	c.logger.Infof("%s Server Sekai Client inheriting account...", strings.ToUpper(string(c.server)))

	path := buildInheritPath(c.server, c.inherit.InheritID, returnUserID)
	data, _ := base64.StdEncoding.DecodeString(RequestDataGeneral)
	resp, status, err := c.callAPI(ctx, path, httpMethodPost, data, headers)
	if err != nil {
		return err
	}
	if status != statusCodeOK {
		return fmt.Errorf("inherit account failed, status=%d", status)
	}

	unpackedAny, err := Unpack(resp, harukiUtils.SupportedDataUploadServer(c.server))
	if err != nil {
		c.logger.Errorf("Failed to unpack inherit response: %v", err)
		return err
	}
	unpacked, ok := unpackedAny.(map[string]any)
	if !ok {
		c.logger.Errorf("Unexpected unpack result type")
		return fmt.Errorf("unexpected unpack result type")
	}

	if returnUserID {
		uid, err := extractInheritUserID(unpacked)
		if err != nil {
			c.logger.Errorf("Failed to get userId from inherit response")
			return err
		}
		c.userID = uid
		return nil
	}

	credential, err := extractInheritCredential(unpacked)
	if err != nil {
		c.logger.Errorf("Failed to get credential from inherit response")
		return err
	}
	c.credential = credential
	c.logger.Infof("%s Server Sekai Client retrieved user credential.", strings.ToUpper(string(c.server)))
	return nil
}

func buildInheritPath(server harukiUtils.SupportedInheritUploadServer, inheritID string, returnUserID bool) string {
	path := fmt.Sprintf(
		"/inherit/user/%s?isExecuteInherit=%s",
		inheritID,
		inheritExecuteFlag(returnUserID),
	)
	if server == EN {
		path += "&isAdult=True&tAge=16"
	}
	return path
}

func inheritExecuteFlag(returnUserID bool) string {
	if returnUserID {
		return "False"
	}
	return "True"
}

func extractInheritUserID(unpacked map[string]any) (int64, error) {
	after, ok := unpacked["afterUserGamedata"].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("failed to get userId")
	}
	uidVal, exists := after["userId"]
	if !exists {
		return 0, fmt.Errorf("failed to get userId")
	}

	switch uid := uidVal.(type) {
	case int64:
		return uid, nil
	case uint64:
		if uid > math.MaxInt64 {
			return 0, fmt.Errorf("userId too large for int64: %v", uid)
		}
		return int64(uid), nil
	default:
		return 0, fmt.Errorf("failed to get userId")
	}
}

func extractInheritCredential(unpacked map[string]any) (string, error) {
	cred, ok := unpacked["credential"].(string)
	if !ok || cred == "" {
		return "", fmt.Errorf("failed to get credential")
	}
	return cred, nil
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
	resp, status, err := c.callAPI(ctx, path, httpMethodPut, packed, nil)
	if err != nil {
		return err
	}
	if status == statusCodeForbidden {
		c.logger.Errorf("Account login failed, status=403")
		return fmt.Errorf("account login failed, status=403")
	}

	unpackedAny, err := Unpack(resp, harukiUtils.SupportedDataUploadServer(c.server))
	if err != nil {
		c.logger.Errorf("Failed to unpack login response: %v", err)
		return err
	}
	unpacked, ok := unpackedAny.(map[string]any)
	if !ok {
		c.logger.Errorf("Unexpected unpack result type")
		return fmt.Errorf("unexpected unpack result type")
	}

	sessionToken, err := extractSessionToken(unpacked)
	if err != nil {
		c.logger.Errorf("Login response missing sessionToken")
		return err
	}
	c.headers[headerSessionToken] = sessionToken
	c.logger.Infof("%s Server Sekai Client logged in.", strings.ToUpper(string(c.server)))
	return nil
}

func extractSessionToken(unpacked map[string]any) (string, error) {
	st, ok := unpacked["sessionToken"].(string)
	if !ok || st == "" {
		return "", fmt.Errorf("login response missing sessionToken")
	}
	return st, nil
}
