package sekai

import (
	"context"
	"fmt"
	"maps"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/redact"
	"github.com/google/uuid"
)

var newRequestID = uuid.NewString

func (c *HarukiSekaiClient) callAPI(
	ctx context.Context,
	path, method string,
	body []byte,
	customHeaders map[string]string,
) ([]byte, int, error) {
	if c.isErrorExist {
		return nil, 0, fmt.Errorf("client in error state: %s", c.errorMessage)
	}

	headers := mergedHeaders(c.headers, customHeaders)
	headers[headerRequestID] = newRequestID()

	url := c.api + path
	status, respHeaders, respBody, err := c.httpClient.Request(ctx, method, url, headers, body)
	// The inherit path embeds the inherit ID, and transport errors (*url.Error)
	// repeat the full URL, so everything that can reach a log line or an
	// APIError message is redacted here, where the exact values are known.
	secrets := c.redactionSecrets()
	if err != nil {
		err = redact.Error(err, secrets...)
		c.logger.Errorf("HTTP request failed for %s: %v", redact.Text(url, secrets...), err)
		return nil, 0, NewAPIError(redact.Text(path, secrets...), method, 0, "request failed", err)
	}

	if status == statusCodeOK {
		applySessionHeaders(c.headers, respHeaders, c)
		return respBody, status, nil
	}

	c.logger.Errorf("API returned non-200 status for %s: %d", redact.Text(url, secrets...), status)
	return respBody, status, NewAPIError(redact.Text(path, secrets...), method, status, "non-200 response", nil)
}

// redactionSecrets returns the credentials this client was built with, so they
// can be scrubbed verbatim even where pattern matching would miss them (an
// inherit ID pasted with a space in it, for example).
func (c *HarukiSekaiClient) redactionSecrets() []string {
	return []string{c.inherit.InheritID, c.inherit.InheritPassword}
}

func mergedHeaders(base, custom map[string]string) map[string]string {
	headers := make(map[string]string)
	maps.Copy(headers, base)
	maps.Copy(headers, custom)
	return headers
}

func applySessionHeaders(dst, respHeaders map[string]string, c *HarukiSekaiClient) {
	if st, ok := respHeaders[headerSessionToken]; ok && st != "" {
		dst[headerSessionToken] = st
	}
	if v, ok := respHeaders[headerLoginBonus]; ok && v == "true" {
		c.loginBonus = true
	}
}
