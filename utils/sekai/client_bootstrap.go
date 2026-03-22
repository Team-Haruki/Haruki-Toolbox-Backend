package sekai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/golang-jwt/jwt/v5"
)

type appVersionPayload struct {
	AppVersion   string `json:"appVersion"`
	AppHash      string `json:"appHash"`
	DataVersion  string `json:"dataVersion"`
	AssetVersion string `json:"assetVersion"`
}

func buildCookieHeader(setCookies []string) string {
	pairs := make([]string, 0, len(setCookies))
	for _, raw := range setCookies {
		for _, pair := range extractCookiePairs(raw) {
			if pair == "" {
				continue
			}
			pairs = append(pairs, pair)
		}
	}
	return strings.Join(pairs, "; ")
}

func extractCookiePairs(raw string) []string {
	segments := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ';' || r == ','
	})
	pairs := make([]string, 0, len(segments))
	for _, segment := range segments {
		pair := strings.TrimSpace(segment)
		if pair == "" {
			continue
		}
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, " \t\r\n") || isCookieAttributeName(name) {
			continue
		}
		pairs = append(pairs, name+"="+strings.TrimSpace(value))
	}
	return pairs
}

func isCookieAttributeName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "expires", "max-age", "domain", "path", "samesite", "priority", "version", "comment":
		return true
	default:
		return false
	}
}

func (c *HarukiSekaiClient) getCookies(ctx context.Context, retries int) error {
	if c.server != JP {
		return nil
	}

	c.logger.Infof("Parsing JP server cookies...")
	url := "https://issue.sekai.colorfulpalette.org/api/signature"
	var lastErr error

	for i := range retries {
		status, headers, _, err := c.httpClient.RequestWithHeaders(ctx, httpMethodPost, url, nil, nil)
		if err != nil {
			lastErr = err
			c.logger.Warnf("Cookie request failed (attempt %d/%d): %v", i+1, retries, err)
			continue
		}
		if status == statusCodeOK {
			if cookies, ok := headers["Set-Cookie"]; ok {
				pairs := make([]string, 0, len(cookies))
				for _, rawCookie := range cookies {
					pairs = append(pairs, extractCookiePairs(rawCookie)...)
				}
				if cookieHeader := strings.Join(pairs, "; "); cookieHeader != "" {
					c.headers["Cookie"] = cookieHeader
					c.logger.Infof("JP server cookies parsed.")
					return nil
				}
			}
			lastErr = fmt.Errorf("empty Set-Cookie header")
			c.logger.Errorf("Cookie response missing Set-Cookie header")
			continue
		}
		lastErr = fmt.Errorf("unexpected status code: %d", status)
		c.logger.Errorf("Cookie request failed with status %d", status)
	}

	c.isErrorExist = true
	c.errorMessage = "failed to parse cookies after retries"
	return NewAuthError("getCookies", fmt.Sprintf("failed after %d attempts", retries), lastErr)
}

func (c *HarukiSekaiClient) parseAppVersion(ctx context.Context, retries int) error {
	if c.isErrorExist {
		return NewAuthError("parseAppVersion", "client in error state", nil)
	}

	serverName := strings.ToUpper(string(c.server))
	c.logger.Infof("Parsing %s server app version...", serverName)
	var lastErr error

	for i := range retries {
		status, _, body, err := c.httpClient.Request(ctx, httpMethodGet, c.versionURL, nil, nil)
		if err != nil {
			lastErr = err
			c.logger.Warnf("Version request failed (attempt %d/%d): %v", i+1, retries, err)
			continue
		}
		if status == statusCodeOK {
			versionData, err := parseVersionPayload(body)
			if err != nil {
				lastErr = err
				c.logger.Errorf("Failed to parse version response: %v", err)
				continue
			}
			applyVersionHeaders(c.headers, versionData)
			c.logger.Infof("Parsed %s server app version: %s", serverName, versionData.AppVersion)
			return nil
		}
		lastErr = fmt.Errorf("unexpected status code: %d", status)
		c.logger.Errorf("Version request failed with status %d", status)
	}

	c.isErrorExist = true
	c.errorMessage = "failed to parse game version after retries"
	return NewAuthError("parseAppVersion", fmt.Sprintf("%s server: failed after %d attempts", serverName, retries), lastErr)
}

func parseVersionPayload(body []byte) (appVersionPayload, error) {
	var data appVersionPayload
	if err := sonic.Unmarshal(body, &data); err != nil {
		return appVersionPayload{}, err
	}
	return data, nil
}

func applyVersionHeaders(headers map[string]string, payload appVersionPayload) {
	headers["X-App-Version"] = payload.AppVersion
	headers["X-App-Hash"] = payload.AppHash
	headers["X-Data-Version"] = payload.DataVersion
	headers["X-Asset-Version"] = payload.AssetVersion
}

func (c *HarukiSekaiClient) generateInheritToken() (string, error) {
	inheritPayload := jwt.MapClaims{
		"inheritId": c.inherit.InheritID,
		"password":  c.inherit.InheritPassword,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, inheritPayload)
	return token.SignedString([]byte(c.inheritJWTToken))
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
