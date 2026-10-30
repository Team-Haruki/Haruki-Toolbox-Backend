package sekai

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/redact"

	"github.com/google/uuid"
)

// InheritSession is one account-transfer conversation with a JP or EN game
// server. It owns the HTTP client, the request headers (cookies, app version,
// session token) and the game user ID once known, and exposes the transfer as
// ordered steps the caller drives one at a time:
//
//	Bootstrap -> Preview -> Execute -> Login -> IssueInheritCode
//	          -> FetchSuite -> RefreshHome -> FetchMysekai
//
// A re-issue from a stored credential skips Preview and Execute:
//
//	Bootstrap -> Login -> IssueInheritCode
//
// The session never logs. Each network call is reported to the Progress sink
// (a sub-step name plus running/done/failed), and every error it returns has
// the inherit ID, the passwords it was given and the game user ID scrubbed.
type InheritSession struct {
	region    inheritRegion
	cryptor   ServerCryptor
	api       string
	version   string
	jwtSecret string
	headers   map[string]string
	http      *harukiHttp.Client
	pacing    InheritPacing
	progress  Progress

	userID      int64
	loginBonus  bool
	previewedAt time.Time
	executedAt  time.Time
	// secrets are scrubbed verbatim from every error; maskedIDs are replaced
	// with a per-process fingerprint.
	secrets []string
}

// InheritSessionConfig builds an InheritSession. NewInheritSessionConfig fills
// it from the loaded configuration; tests point API, VersionURL and CookieURL
// at a local server.
type InheritSessionConfig struct {
	Server     harukiUtils.SupportedInheritUploadServer
	Cryptor    ServerCryptor
	API        string
	VersionURL string
	// CookieURL overrides the region's signature endpoint (JP only).
	CookieURL        string
	Headers          map[string]string
	Proxy            string
	InheritJWTSecret string
	// Pacing sets the pauses; unset (zero) fields keep the defaults.
	Pacing   InheritPacing
	Progress Progress
}

// InheritCode is a transfer code: the ID the game shows and the password the
// user chose for it.
type InheritCode struct {
	InheritID string
	Password  string
}

// InheritPreview is what the non-executing inherit call reveals about the
// account before anything is transferred.
type InheritPreview struct {
	UserID      int64
	Name        string
	Rank        int64
	Restriction *TransferRestriction
}

// TransferRestriction mirrors the game's UserRestrictInfo
// (userEventDeviceTransferRestrict): during some events a transfer is limited.
type TransferRestriction struct {
	IsRestrictDeviceTransfer bool
	RestrictEndAt            int64
	RestTransferCount        int64
	IsWorldBloomChapter      bool
	RestrictRank             int64
	GameCharacterID          int64
}

// inheritRegion holds the few things that differ between JP and EN.
type inheritRegion struct {
	server harukiUtils.SupportedInheritUploadServer
	// cookieURL is the signed-cookie endpoint fetched before anything else;
	// empty means the region needs no cookies.
	cookieURL string
	// inheritQuery is appended to the inherit path after isExecuteInherit.
	inheritQuery string
	// suiteQuery is appended to the first suite request.
	suiteQuery string
}

var inheritRegions = map[harukiUtils.SupportedInheritUploadServer]inheritRegion{
	ServerJP: {server: ServerJP, cookieURL: "https://issue.sekai.colorfulpalette.org/api/signature", suiteQuery: "?isLogin=true"},
	ServerEN: {server: ServerEN, inheritQuery: "&isAdult=True&tAge=16"},
}

const (
	ServerJP harukiUtils.SupportedInheritUploadServer = "jp"
	ServerEN harukiUtils.SupportedInheritUploadServer = "en"

	sessionHTTPTimeout = 15 * time.Second
	bootstrapAttempts  = 2

	headerSessionToken   = "X-Session-Token"
	headerLoginBonus     = "X-Login-Bonus-Status"
	headerRequestID      = "X-Request-Id"
	headerInheritIDToken = "x-inherit-id-verify-token"
)

var newRequestID = uuid.NewString

// NewInheritSessionConfig builds the session configuration for server from
// the sekai_client configuration and the outbound proxy.
func NewInheritSessionConfig(cfg harukiConfig.SekaiClientConfig, proxy string, server harukiUtils.SupportedInheritUploadServer, cryptor ServerCryptor, pacing InheritPacing, progress Progress) (InheritSessionConfig, error) {
	out := InheritSessionConfig{Server: server, Cryptor: cryptor, Proxy: proxy, Pacing: pacing, Progress: progress}
	switch server {
	case ServerJP:
		out.API = fmt.Sprintf("https://%s/api", cfg.JPServerAPIHost)
		out.VersionURL = cfg.JPServerAppVersionUrl
		out.Headers = cfg.JPServerInheritClientHeaders
		out.InheritJWTSecret = cfg.JPServerInheritToken
	case ServerEN:
		out.API = fmt.Sprintf("https://%s/api", cfg.ENServerAPIHost)
		out.VersionURL = cfg.ENServerAppVersionUrl
		out.Headers = cfg.ENServerInheritClientHeaders
		out.InheritJWTSecret = cfg.ENServerInheritToken
	default:
		return InheritSessionConfig{}, fmt.Errorf("%w: %s", ErrInvalidServer, server)
	}
	return out, nil
}

// NewInheritSession returns a session for cfg.Server.
func NewInheritSession(cfg InheritSessionConfig) (*InheritSession, error) {
	region, ok := inheritRegions[cfg.Server]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrInvalidServer, cfg.Server)
	}
	if cfg.CookieURL != "" && region.cookieURL != "" {
		region.cookieURL = cfg.CookieURL
	}
	headers := make(map[string]string, len(cfg.Headers)+6)
	maps.Copy(headers, cfg.Headers)
	return &InheritSession{
		region:    region,
		cryptor:   cfg.Cryptor,
		api:       cfg.API,
		version:   cfg.VersionURL,
		jwtSecret: cfg.InheritJWTSecret,
		headers:   headers,
		http:      harukiHttp.NewClient(cfg.Proxy, sessionHTTPTimeout),
		pacing:    cfg.Pacing.withDefaults(),
		progress:  cfg.Progress,
	}, nil
}

// UserID is the game user ID, known after Preview or given to Login.
func (s *InheritSession) UserID() int64 { return s.userID }

// AddSecrets registers values that must never appear in an error the session
// returns (a password it does not otherwise see, for example).
func (s *InheritSession) AddSecrets(values ...string) {
	for _, v := range values {
		if v != "" {
			s.secrets = append(s.secrets, v)
		}
	}
}

// call sends one game API request. A non-200 answer returns its body and
// status together with an *APIError; a transport failure is an *APIError with
// StatusCode 0. The path and any transport error are redacted here, where the
// exact secret values are known.
func (s *InheritSession) call(ctx context.Context, method, path string, body []byte, extra map[string]string) ([]byte, int, error) {
	headers := make(map[string]string, len(s.headers)+len(extra)+1)
	maps.Copy(headers, s.headers)
	maps.Copy(headers, extra)
	headers[headerRequestID] = newRequestID()

	status, respHeaders, respBody, err := s.http.Request(ctx, method, s.api+path, headers, body)
	if err != nil {
		return nil, 0, NewAPIError(s.redactText(path), method, 0, "request failed", s.redactError(err))
	}
	if status != 200 {
		return respBody, status, NewAPIError(s.redactText(path), method, status, "non-200 response", nil)
	}
	if st := respHeaders[headerSessionToken]; st != "" {
		s.headers[headerSessionToken] = st
	}
	if respHeaders[headerLoginBonus] == "true" {
		s.loginBonus = true
	}
	return respBody, status, nil
}

// unpack decrypts a game response that must be a map.
func (s *InheritSession) unpack(body []byte) (map[string]any, error) {
	v, err := s.cryptor.Unpack(body, harukiUtils.SupportedDataUploadServer(s.region.server))
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected response type %T", v)
	}
	return m, nil
}

func (s *InheritSession) pack(v any) ([]byte, error) {
	return s.cryptor.Pack(v, harukiUtils.SupportedDataUploadServer(s.region.server))
}

func (s *InheritSession) maskedIDs() []string {
	if s.userID <= 0 {
		return nil
	}
	return []string{strconv.FormatInt(s.userID, 10)}
}

func (s *InheritSession) redactText(text string) string {
	return redact.MaskIDs(redact.Text(text, s.secrets...), s.maskedIDs()...)
}

func (s *InheritSession) redactError(err error) error {
	return redact.IDsInError(redact.Error(err, s.secrets...), s.maskedIDs()...)
}

// RedactError scrubs err with everything this session knows to be secret, for
// callers that build their own error messages from session results.
func (s *InheritSession) RedactError(err error) error {
	if err == nil {
		return nil
	}
	return s.redactError(err)
}

func userPath(format string, userID int64) string {
	return fmt.Sprintf(format, strconv.FormatInt(userID, 10))
}
