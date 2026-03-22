package sekai

import (
	"context"
	"fmt"
	"maps"

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
	if err != nil {
		c.logger.Errorf("HTTP request failed for %s: %v", url, err)
		return nil, 0, err
	}

	if status == statusCodeOK {
		applySessionHeaders(c.headers, respHeaders, c)
		return respBody, status, nil
	}

	c.logger.Errorf("API returned non-200 status for %s: %d", url, status)
	return respBody, status, fmt.Errorf("API error: %d", status)
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
