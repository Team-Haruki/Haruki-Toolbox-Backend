// Package masterregistry reads master files from the Haruki master registry's CDN face
// (e.g. https://sekai-api-cdn.haruki.seiunx.com): /v1/master/{region}/current names the
// region's current master set, /v1/master/{region}/files/{name}.json?version={hash} serves
// one file of it.
package masterregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxFileBytes bounds one master file (costume3ds is about 60 MB).
var maxFileBytes int64 = 256 << 20

// Client reads one registry; it is safe for concurrent use.
type Client struct {
	base      string
	userAgent string
	http      *http.Client
}

// New returns a client of the registry at baseURL (with or without a trailing slash).
func New(baseURL, userAgent string, httpClient *http.Client) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), userAgent: userAgent, http: httpClient}
}

func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	// One byte over the bound tells a file that is too large from one that just fits.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxFileBytes {
		return nil, fmt.Errorf("GET %s: more than %d bytes", u, maxFileBytes)
	}
	return body, nil
}

// ContentHash returns the registry's current hash of a region's master set (its data
// version when the registry gives no hash).
func (c *Client) ContentHash(ctx context.Context, region string) (string, error) {
	body, err := c.get(ctx, c.base+"/v1/master/"+url.PathEscape(region)+"/current")
	if err != nil {
		return "", err
	}
	var v struct {
		ContentHash string `json:"contentHash"`
		DataVersion string `json:"dataVersion"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", err
	}
	if v.ContentHash != "" {
		return v.ContentHash, nil
	}
	if v.DataVersion != "" {
		return v.DataVersion, nil
	}
	return "", fmt.Errorf("registry %s current names no version", region)
}

// File returns one master file (e.g. "costume3ds") of the version a ContentHash named.
func (c *Client) File(ctx context.Context, region, name, version string) ([]byte, error) {
	return c.get(ctx, c.base+"/v1/master/"+url.PathEscape(region)+"/files/"+url.PathEscape(name)+".json?version="+url.QueryEscape(version))
}
