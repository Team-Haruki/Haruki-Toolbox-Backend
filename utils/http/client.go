package http

import (
	"bytes"
	"context"
	"fmt"
	harukiLogger "haruki-suite/utils/logger"
	"io"
	stdhttp "net/http"
	"time"

	"github.com/go-resty/resty/v2"
)

const defaultRequestTimeout = 15 * time.Second

type Client struct {
	Proxy   string
	Timeout time.Duration
	client  *resty.Client
}

func NewClient(proxy string, timeout time.Duration) *Client {
	client := &Client{Proxy: proxy, Timeout: timeout}
	if err := client.init(); err != nil {
		harukiLogger.Errorf("Failed to initialize HTTP client: %v", err)
	}
	return client
}

func (c *Client) init() error {
	if c.client != nil {
		return nil
	}
	c.client = resty.New()
	timeout := c.Timeout
	if timeout == 0 {
		timeout = defaultRequestTimeout
	}
	c.client.SetTimeout(timeout)
	if c.Proxy != "" {
		c.client.SetProxy(c.Proxy)
	}
	return nil
}

func (c *Client) Request(ctx context.Context, method, uri string, headers map[string]string, body []byte) (int, map[string]string, []byte, error) {
	statusCode, respHeaders, respBody, err := c.RequestWithHeaders(ctx, method, uri, headers, body)
	if err != nil {
		return 0, nil, nil, err
	}
	return statusCode, flattenHeaders(respHeaders), respBody, nil
}

func (c *Client) RequestNoRedirect(ctx context.Context, method, uri string, headers map[string]string, body []byte) (int, map[string]string, []byte, error) {
	statusCode, respHeaders, respBody, err := c.requestRaw(ctx, method, uri, headers, body, true)
	if err != nil {
		return 0, nil, nil, err
	}
	return statusCode, flattenHeaders(respHeaders), respBody, nil
}

func (c *Client) RequestWithHeaders(ctx context.Context, method, uri string, headers map[string]string, body []byte) (int, map[string][]string, []byte, error) {
	if c.client == nil {
		if err := c.init(); err != nil {
			return 0, nil, nil, fmt.Errorf("failed to initialize client: %w", err)
		}
	}
	req := c.client.R().
		SetContext(ctx).
		SetHeaders(headers)
	if len(body) > 0 {
		req.SetBody(body)
	}
	resp, err := req.Execute(method, uri)
	if err != nil {
		return 0, nil, nil, err
	}
	respHeaders := cloneHeaders(resp.Header())
	return resp.StatusCode(), respHeaders, resp.Body(), nil
}

func (c *Client) requestRaw(ctx context.Context, method, uri string, headers map[string]string, body []byte, disableRedirects bool) (int, map[string][]string, []byte, error) {
	if c.client == nil {
		if err := c.init(); err != nil {
			return 0, nil, nil, fmt.Errorf("failed to initialize client: %w", err)
		}
	}

	req, err := stdhttp.NewRequestWithContext(ctx, method, uri, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	baseClient := c.client.GetClient()
	rawClient := &stdhttp.Client{
		Transport: baseClient.Transport,
		Timeout:   baseClient.Timeout,
	}
	if disableRedirects {
		rawClient.CheckRedirect = func(req *stdhttp.Request, via []*stdhttp.Request) error {
			return stdhttp.ErrUseLastResponse
		}
	}

	resp, err := rawClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, cloneHeaders(resp.Header), respBody, nil
}

func flattenHeaders(headers map[string][]string) map[string]string {
	respHeaders := make(map[string]string, len(headers))
	for k, values := range headers {
		if len(values) == 0 {
			continue
		}
		respHeaders[k] = values[0]
	}
	return respHeaders
}

func cloneHeaders(headers map[string][]string) map[string][]string {
	respHeaders := make(map[string][]string, len(headers))
	for k, values := range headers {
		if len(values) == 0 {
			continue
		}
		respHeaders[k] = append([]string(nil), values...)
	}
	return respHeaders
}
