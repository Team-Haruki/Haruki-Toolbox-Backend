package sekaiapi

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/circuitbreaker"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/go-resty/resty/v2"
)

// sekaiAPIRequestTimeout is the transport ceiling for every call. Callers bound
// their own calls more tightly with a context deadline (see Options).
const sekaiAPIRequestTimeout = 15 * time.Second

const (
	// DefaultProfileViewTimeout bounds a profile view. Healthy views are fast (7-day
	// p99 under 0.8 s), and during an outage they fail with 502 either way, so a
	// shorter bound only makes the failure arrive sooner.
	DefaultProfileViewTimeout = 5 * time.Second
	// minTWProfileViewTimeout keeps TW above its healthy tail: about 0.7% of its
	// successful views take more than 4 s.
	minTWProfileViewTimeout = 5 * time.Second
	// DefaultVerifyTimeout bounds the binding verification lookup. It runs rarely
	// and a false timeout blocks the binding, so it gets more room than a view.
	DefaultVerifyTimeout = 10 * time.Second
	// DefaultProfileCacheTTL is how long a successful profile view is reused.
	DefaultProfileCacheTTL = 60 * time.Second
)

// The breaker trips per server after breakerFailureThreshold degraded calls
// (transport errors, timeouts, 5xx) within breakerWindow, fails fast for
// breakerCooldown, then lets one probe through.
const (
	breakerFailureThreshold = 5
	breakerWindow           = 60 * time.Second
	breakerCooldown         = 30 * time.Second
	breakerRetryAfterFloor  = 1 * time.Second
)

// ErrCircuitOpen matches (errors.Is) the error returned while a server's breaker
// is open.
var ErrCircuitOpen = errors.New("sekai api circuit breaker is open")

// CircuitOpenError is returned, with a result whose ServerAvailable is false,
// when the breaker for the server is open and no request was sent.
type CircuitOpenError struct {
	Server     string
	RetryAfter time.Duration
}

func (e *CircuitOpenError) Error() string {
	return fmt.Sprintf("%v for server %s, retry after %s", ErrCircuitOpen, e.Server, e.RetryAfter)
}

func (e *CircuitOpenError) Is(target error) bool { return target == ErrCircuitOpen }

// Options tunes the caller-side deadlines and the profile view cache. Zero values
// select the defaults.
type Options struct {
	// ProfileViewTimeout bounds a profile view for servers without an entry in
	// ProfileViewTimeoutByServer.
	ProfileViewTimeout time.Duration
	// ProfileViewTimeoutByServer overrides ProfileViewTimeout per server ("jp",
	// "tw", ...). TW is never bounded below 5 s.
	ProfileViewTimeoutByServer map[string]time.Duration
	// VerifyTimeout bounds the binding verification lookup.
	VerifyTimeout time.Duration
	// ProfileCacheTTL is how long a successful profile view is cached; negative
	// disables the cache.
	ProfileCacheTTL time.Duration
}

type HarukiSekaiAPIClient struct {
	httpClient             *resty.Client
	harukiSekaiAPIEndpoint string
	harukiSekaiAPIToken    string
	options                Options
	breaker                *circuitbreaker.Breaker[string]
}

type HarukiSekaiAPIResult struct {
	ServerAvailable bool
	AccountExists   bool
	Body            bool
}

func NewHarukiSekaiAPIClient(apiEndpoint, apiToken string) *HarukiSekaiAPIClient {
	return NewHarukiSekaiAPIClientWithOptions(apiEndpoint, apiToken, Options{})
}

func NewHarukiSekaiAPIClientWithOptions(apiEndpoint, apiToken string, options Options) *HarukiSekaiAPIClient {
	return &HarukiSekaiAPIClient{
		httpClient:             jsoncodec.ConfigureResty(resty.New()).SetTimeout(sekaiAPIRequestTimeout),
		harukiSekaiAPIEndpoint: apiEndpoint,
		harukiSekaiAPIToken:    apiToken,
		options:                normalizeOptions(options),
		breaker:                circuitbreaker.New[string](breakerFailureThreshold, breakerWindow, breakerCooldown, breakerRetryAfterFloor),
	}
}

func normalizeOptions(options Options) Options {
	if options.ProfileViewTimeout <= 0 {
		options.ProfileViewTimeout = DefaultProfileViewTimeout
	}
	byServer := make(map[string]time.Duration, len(options.ProfileViewTimeoutByServer))
	for server, timeout := range options.ProfileViewTimeoutByServer {
		if timeout > 0 {
			byServer[strings.ToLower(strings.TrimSpace(server))] = timeout
		}
	}
	options.ProfileViewTimeoutByServer = byServer
	if options.VerifyTimeout <= 0 {
		options.VerifyTimeout = DefaultVerifyTimeout
	}
	if options.ProfileCacheTTL == 0 {
		options.ProfileCacheTTL = DefaultProfileCacheTTL
	}
	return options
}

// ProfileViewTimeout returns the deadline for a profile view on server.
func (c *HarukiSekaiAPIClient) ProfileViewTimeout(server string) time.Duration {
	timeout, ok := c.options.ProfileViewTimeoutByServer[server]
	if !ok {
		timeout = c.options.ProfileViewTimeout
	}
	if server == string(utils.SupportedDataUploadServerTW) && timeout < minTWProfileViewTimeout {
		timeout = minTWProfileViewTimeout
	}
	return timeout
}

// VerifyTimeout returns the deadline for the binding verification lookup.
func (c *HarukiSekaiAPIClient) VerifyTimeout() time.Duration {
	return c.options.VerifyTimeout
}

// ProfileCacheTTL returns how long a successful profile view may be reused, or 0
// when the cache is disabled.
func (c *HarukiSekaiAPIClient) ProfileCacheTTL() time.Duration {
	if c.options.ProfileCacheTTL < 0 {
		return 0
	}
	return c.options.ProfileCacheTTL
}

// GetUserProfile fetches a game account profile through the per-server circuit
// breaker. While the breaker is open it returns a ServerAvailable=false result
// and a *CircuitOpenError without calling the API.
func (c *HarukiSekaiAPIClient) GetUserProfile(ctx context.Context, userID string, serverStr string) (*HarukiSekaiAPIResult, []byte, error) {
	server, err := utils.ParseSupportedDataUploadServer(serverStr)
	if err != nil {
		return nil, nil, err
	}
	key := string(server)
	allowed, retryAfter, token := c.breaker.Allow(key)
	if !allowed {
		return &HarukiSekaiAPIResult{}, nil, &CircuitOpenError{Server: key, RetryAfter: retryAfter}
	}
	result, body, degraded, err := c.requestUserProfile(ctx, userID, key)
	if errors.Is(err, context.Canceled) {
		// The caller gave up; that says nothing about the upstream.
		c.breaker.ReleaseProbe(key, token)
	} else {
		c.breaker.RecordResult(key, token, degraded)
	}
	return result, body, err
}

// requestUserProfile performs one call. degraded reports a transport error, a
// timeout or a 5xx (including 503 maintenance): the outcomes that count toward
// the breaker. A 404, a missing account and other 4xx answers do not.
func (c *HarukiSekaiAPIClient) requestUserProfile(ctx context.Context, userID string, serverStr string) (*HarukiSekaiAPIResult, []byte, bool, error) {
	url := fmt.Sprintf("%s/api/%s/%s/profile", c.harukiSekaiAPIEndpoint, serverStr, userID)
	resp, err := c.httpClient.R().
		SetContext(ctx).
		SetHeader("X-Haruki-Sekai-Token", c.harukiSekaiAPIToken).
		SetHeader("Accept", "application/json").
		Get(url)
	if err != nil {
		harukiLogger.Errorf("Sekai API request failed for %s: %v", url, err)
		return nil, nil, true, fmt.Errorf("请求失败: %w", err)
	}

	statusCode := resp.StatusCode()
	body := resp.Body()

	if len(body) > 0 {
		var bodyData map[string]any
		if jsonErr := json.Unmarshal(body, &bodyData); jsonErr == nil {
			if _, hasError := bodyData["errorCode"]; hasError {

				errMsg, _ := bodyData["errorMessage"].(string)
				httpStatus := 0
				if hs, ok := bodyData["httpStatus"].(float64); ok {
					httpStatus = int(hs)
				}
				switch {
				case httpStatus == 404 || statusCode == 404:
					return &HarukiSekaiAPIResult{
						ServerAvailable: true,
						AccountExists:   false,
						Body:            false,
					}, nil, false, fmt.Errorf("this user does not exist: %s", errMsg)
				case httpStatus == 503 || statusCode == 503:
					return &HarukiSekaiAPIResult{
						ServerAvailable: false,
						AccountExists:   false,
						Body:            false,
					}, nil, true, fmt.Errorf("game server under maintenance: %s", errMsg)
				case httpStatus >= 500 || statusCode >= 500:
					harukiLogger.Errorf("Sekai API returned server error for %s: %s", url, errMsg)
					return &HarukiSekaiAPIResult{
						ServerAvailable: false,
						AccountExists:   false,
						Body:            false,
					}, nil, true, fmt.Errorf("api server error: %s", errMsg)
				default:
					return &HarukiSekaiAPIResult{
						ServerAvailable: true,
						AccountExists:   false,
						Body:            false,
					}, nil, false, fmt.Errorf("api error (status %d): %s", httpStatus, errMsg)
				}
			}
		}
	}

	switch statusCode {
	case 200:
		return &HarukiSekaiAPIResult{
			ServerAvailable: true,
			AccountExists:   true,
			Body:            true,
		}, body, false, nil
	case 404:
		return &HarukiSekaiAPIResult{
			ServerAvailable: true,
			AccountExists:   false,
			Body:            false,
		}, nil, false, fmt.Errorf("this user does not exist, please check your userID if is corrent")
	case 500:
		harukiLogger.Errorf("Sekai API returned 500 for %s", url)
		return &HarukiSekaiAPIResult{
			ServerAvailable: false,
			AccountExists:   false,
			Body:            false,
		}, nil, true, fmt.Errorf("api is busy, please try again later, if this problem still consist, please contact Haruki Dev Team")
	case 503:
		harukiLogger.Warnf("Sekai API returned 503 (maintenance) for %s", url)
		return &HarukiSekaiAPIResult{
			ServerAvailable: false,
			AccountExists:   false,
			Body:            false,
		}, nil, true, fmt.Errorf("the game server you query is under maintenance")
	default:
		harukiLogger.Errorf("Sekai API returned unexpected status %d for %s", statusCode, url)
		return &HarukiSekaiAPIResult{
			ServerAvailable: false,
			AccountExists:   false,
			Body:            false,
		}, nil, statusCode >= 500, fmt.Errorf("api request failed, status code: %d", statusCode)
	}
}
