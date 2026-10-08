package sekai

import (
	"context"
	"fmt"
	"maps"
	"strconv"

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
	// The inherit path embeds the inherit ID, the paths after it embed the game
	// user ID the inherit resolved to, and transport errors (*url.Error) repeat
	// the full URL, so everything that can reach a log line or an APIError
	// message is redacted here, where the exact values are known.
	if err != nil {
		err = c.redactError(err)
		c.logger.Errorf("HTTP request failed for %s: %v", c.redactText(url), err)
		return nil, 0, NewAPIError(c.redactText(path), method, 0, "request failed", err)
	}

	if status == statusCodeOK {
		applySessionHeaders(c.headers, respHeaders, c)
		return respBody, status, nil
	}

	c.logger.Errorf("API returned non-200 status for %s: %d", c.redactText(url), status)
	return respBody, status, NewAPIError(c.redactText(path), method, status, "non-200 response", nil)
}

// redactionSecrets returns the credentials this client was built with, so they
// can be scrubbed verbatim even where pattern matching would miss them (an
// inherit ID pasted with a space in it, for example).
func (c *HarukiSekaiClient) redactionSecrets() []string {
	return []string{c.inherit.InheritID, c.inherit.InheritPassword}
}

// maskedIDs returns the identifiers this client masks to a fingerprint rather
// than removing outright: the game user ID, once the inherit has resolved it.
// It was obtained by presenting the transfer credentials, so a log line must
// not tie a transfer attempt to the account; the fingerprint still lets one
// attempt be followed across its lines. The upload audit row records the ID
// itself, for the operators allowed to see it.
func (c *HarukiSekaiClient) maskedIDs() []string {
	if c.userID <= 0 {
		return nil
	}
	return []string{strconv.FormatInt(c.userID, 10)}
}

func (c *HarukiSekaiClient) redactText(s string) string {
	return redact.MaskIDs(redact.Text(s, c.redactionSecrets()...), c.maskedIDs()...)
}

func (c *HarukiSekaiClient) redactError(err error) error {
	return redact.IDsInError(redact.Error(err, c.redactionSecrets()...), c.maskedIDs()...)
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
