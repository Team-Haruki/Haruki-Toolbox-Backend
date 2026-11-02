package sekai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	json "encoding/json/v2"

	"github.com/golang-jwt/jwt/v5"
)

// requestDataGeneral is the body the client posts to the inherit and MySekai
// endpoints.
const requestDataGeneral = "/6O9YhTzP+c8ty/uImK+2w=="

var errPreviewMissingUserID = errors.New("inherit response has no userId")

// Bootstrap fetches the signed cookies (JP) and the current app version, the
// headers every later call needs.
func (s *InheritSession) Bootstrap(ctx context.Context) error {
	if s.region.cookieURL != "" {
		if err := s.track(SubStepCookies, func() error { return s.fetchCookies(ctx) }); err != nil {
			return err
		}
	}
	return s.track(SubStepVersion, func() error { return s.fetchVersion(ctx, false) })
}

func (s *InheritSession) fetchCookies(ctx context.Context) error {
	var lastErr error
	for i := range bootstrapAttempts {
		if i > 0 {
			if err := pause(ctx, retryBackoff(i)); err != nil {
				return NewAuthError("getCookies", "interrupted before retrying", err)
			}
		}
		status, headers, _, err := s.http.RequestWithHeaders(ctx, "POST", s.region.cookieURL, nil, nil)
		if err != nil {
			// APIError{0} keeps a hard-down signature host visible to the
			// circuit breaker's degradation classifier.
			lastErr = NewAPIError(s.region.cookieURL, "POST", 0, "request failed", err)
			continue
		}
		if status != 200 {
			lastErr = NewAPIError(s.region.cookieURL, "POST", status, "unexpected status code", nil)
			continue
		}
		if cookie := buildCookieHeader(headers["Set-Cookie"]); cookie != "" {
			s.headers["Cookie"] = cookie
			return nil
		}
		lastErr = errors.New("empty Set-Cookie header")
	}
	return NewAuthError("getCookies", fmt.Sprintf("failed after %d attempts", bootstrapAttempts), lastErr)
}

type appVersionPayload struct {
	AppVersion   string `json:"appVersion"`
	AppHash      string `json:"appHash"`
	DataVersion  string `json:"dataVersion"`
	AssetVersion string `json:"assetVersion"`
}

// noCacheHeaders ask every cache between us and the version file for a fresh
// copy; a refresh after a 426 must not be answered by a cached stale file.
var noCacheHeaders = map[string]string{"Cache-Control": "no-cache", "Pragma": "no-cache"}

// fetchVersion reads the version file and sets the version headers. The app
// version and hash always come from it. Its data and asset versions are set
// on the first read, and afterwards only when the file moved, so a refresh
// with a stale file keeps versions a login reported (adoptDataVersions).
func (s *InheritSession) fetchVersion(ctx context.Context, fresh bool) error {
	var requestHeaders map[string]string
	if fresh {
		requestHeaders = noCacheHeaders
	}
	var lastErr error
	for i := range bootstrapAttempts {
		if i > 0 {
			if err := pause(ctx, retryBackoff(i)); err != nil {
				return NewAuthError("parseAppVersion", "interrupted before retrying", err)
			}
		}
		status, _, body, err := s.http.Request(ctx, "GET", s.version, requestHeaders, nil)
		if err != nil {
			lastErr = NewAPIError(s.version, "GET", 0, "request failed", err)
			continue
		}
		if status != 200 {
			lastErr = NewAPIError(s.version, "GET", status, "unexpected status code", nil)
			continue
		}
		var v appVersionPayload
		if err := json.Unmarshal(body, &v); err != nil {
			lastErr = err
			continue
		}
		s.headers[headerAppVersion] = v.AppVersion
		s.headers[headerAppHash] = v.AppHash
		if !s.versionRead || v.DataVersion != s.fileData {
			s.headers[headerDataVersion] = v.DataVersion
		}
		if !s.versionRead || v.AssetVersion != s.fileAsset {
			s.headers[headerAssetVersion] = v.AssetVersion
		}
		s.fileData, s.fileAsset, s.versionRead = v.DataVersion, v.AssetVersion, true
		return nil
	}
	return NewAuthError("parseAppVersion", fmt.Sprintf("%s server: failed after %d attempts", strings.ToUpper(string(s.region.server)), bootstrapAttempts), lastErr)
}

// retryBackoff is the wait before retry attempt i (1-based) of the cookie and
// version requests.
func retryBackoff(attempt int) time.Duration {
	return time.Duration(attempt) * 500 * time.Millisecond
}

// buildCookieHeader joins the name=value pairs of Set-Cookie headers, which
// may each carry several cookies separated by commas (CloudFront does).
func buildCookieHeader(setCookies []string) string {
	var pairs []string
	for _, raw := range setCookies {
		pairs = append(pairs, extractCookiePairs(raw)...)
	}
	return strings.Join(pairs, "; ")
}

func extractCookiePairs(raw string) []string {
	segments := strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ',' })
	pairs := make([]string, 0, len(segments))
	for _, segment := range segments {
		name, value, ok := strings.Cut(strings.TrimSpace(segment), "=")
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
	switch strings.ToLower(name) {
	case "expires", "max-age", "domain", "path", "samesite", "priority", "version", "comment":
		return true
	}
	return false
}

// Preview makes the non-executing inherit call (isExecuteInherit=False). It
// does not consume the code; it resolves the game user ID and returns the
// account summary the game shows before a transfer.
func (s *InheritSession) Preview(ctx context.Context, code InheritCode) (*InheritPreview, error) {
	s.AddSecrets(code.InheritID, code.Password)
	var preview *InheritPreview
	err := s.track(SubStepInheritCheck, func() error {
		// The non-executing call uses nothing up, so a 426 may be retried
		// once with refreshed versions.
		return s.retryOnUpgrade(ctx, false, func() ([]byte, error) {
			resp, raw, err := s.inherit(ctx, code, false)
			if err != nil {
				return raw, err
			}
			preview, err = parseInheritPreview(resp)
			return raw, err
		})
	})
	if err != nil {
		return nil, err
	}
	s.userID = preview.UserID
	s.previewedAt = time.Now()
	return preview, nil
}

// Execute makes the executing inherit call (isExecuteInherit=True) and
// returns the account credential. From here on the code is used up and the
// device that generated it can no longer log in. Execute first waits out
// whatever is left of the pause that separates it from Preview.
func (s *InheritSession) Execute(ctx context.Context, code InheritCode) (string, error) {
	s.AddSecrets(code.InheritID, code.Password)
	if err := pause(ctx, remaining(s.previewedAt, s.pacing.AfterInheritCheck)); err != nil {
		return "", NewAuthError("inherit", "interrupted before the executing inherit call", err)
	}
	var credential string
	// ⚠️ Never retried, not even on a 426: this call uses up the code.
	err := s.track(SubStepInheritExecute, func() error {
		resp, _, err := s.inherit(ctx, code, true)
		if err != nil {
			return err
		}
		credential, err = stringField(resp, "credential")
		return err
	})
	if err != nil {
		return "", err
	}
	s.AddSecrets(credential)
	s.executedAt = time.Now()
	return credential, nil
}

// inherit makes the inherit call and returns the decoded answer and the raw
// body (which a non-200 answer also returns).
func (s *InheritSession) inherit(ctx context.Context, code InheritCode, execute bool) (map[string]any, []byte, error) {
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"inheritId": code.InheritID,
		"password":  code.Password,
	}).SignedString([]byte(s.jwtSecret))
	if err != nil {
		return nil, nil, err
	}
	flag := "False"
	if execute {
		flag = "True"
	}
	path := "/inherit/user/" + code.InheritID + "?isExecuteInherit=" + flag + s.region.inheritQuery
	body, _ := base64.StdEncoding.DecodeString(requestDataGeneral)
	resp, _, err := s.call(ctx, "POST", path, body, map[string]string{headerInheritIDToken: token})
	if err != nil {
		return nil, resp, err
	}
	m, err := s.unpack(resp)
	return m, resp, err
}

// Login authenticates with credential as userID and keeps the session token.
// After Execute it first waits out the rest of the pre-login pause.
func (s *InheritSession) Login(ctx context.Context, userID int64, credential string) error {
	if userID <= 0 || credential == "" {
		return NewAuthError("login", "missing user ID or credential", nil)
	}
	s.userID = userID
	s.AddSecrets(credential)
	if !s.executedAt.IsZero() {
		if err := pause(ctx, remaining(s.executedAt, s.pacing.BeforeLogin)); err != nil {
			return NewAuthError("login", "interrupted before login", err)
		}
	}
	s.credential = credential
	return s.track(SubStepAuth, func() error {
		// Login only reads the app version from the version file, so on a 426
		// the refresh re-reads that file (a nested login could not help).
		return s.retryOnUpgrade(ctx, false, func() ([]byte, error) { return s.authenticate(ctx) })
	})
}

// authenticate logs in with the session's credential, keeps the session
// token and adopts the data and asset versions the game reports, as the game
// client does. It never takes the app version from the answer.
func (s *InheritSession) authenticate(ctx context.Context) ([]byte, error) {
	packed, err := s.pack(map[string]any{
		"credential":      s.credential,
		"deviceId":        nil,
		"authTriggerType": "normal",
	})
	if err != nil {
		return nil, err
	}
	resp, _, err := s.call(ctx, "PUT", userPath("/user/%s/auth?refreshUpdatedResources=False", s.userID), packed, nil)
	if err != nil {
		return resp, err
	}
	unpacked, err := s.unpack(resp)
	if err != nil {
		return resp, err
	}
	token, err := stringField(unpacked, "sessionToken")
	if err != nil {
		return resp, err
	}
	s.headers[headerSessionToken] = token
	s.adoptDataVersions(unpacked)
	return resp, nil
}

// IssueInheritCode generates a new transfer code protected by password. The
// game invalidates the previous code for the account.
func (s *InheritSession) IssueInheritCode(ctx context.Context, password string) (string, error) {
	s.AddSecrets(password)
	var inheritID string
	err := s.track(SubStepIssueCode, func() error {
		packed, err := s.pack(map[string]any{"password": password})
		if err != nil {
			return err
		}
		return s.retryOnUpgrade(ctx, true, func() ([]byte, error) {
			resp, _, err := s.call(ctx, "PUT", userPath("/user/%s/inherit", s.userID), packed, nil)
			if err != nil {
				return resp, err
			}
			unpacked, err := s.unpack(resp)
			if err != nil {
				return resp, err
			}
			userInherit, _ := unpacked["userInherit"].(map[string]any)
			inheritID, err = stringField(userInherit, "inheritId")
			return resp, err
		})
	})
	if err != nil {
		return "", err
	}
	s.AddSecrets(inheritID)
	return inheritID, nil
}

// remaining is how much of wait is left since start; the full wait when start
// is unset.
func remaining(start time.Time, wait time.Duration) time.Duration {
	if start.IsZero() {
		return wait
	}
	return wait - since(start)
}

// since is time.Since; tests freeze it.
var since = time.Since

func parseInheritPreview(resp map[string]any) (*InheritPreview, error) {
	after, _ := resp["afterUserGamedata"].(map[string]any)
	if after == nil {
		return nil, errPreviewMissingUserID
	}
	uid, ok := int64Field(after, "userId")
	if !ok || uid <= 0 {
		return nil, errPreviewMissingUserID
	}
	preview := &InheritPreview{UserID: uid}
	preview.Name, _ = after["name"].(string)
	preview.Rank, _ = int64Field(after, "rank")
	restrict, _ := resp["userEventDeviceTransferRestrict"].(map[string]any)
	if restrict == nil {
		restrict, _ = after["userEventDeviceTransferRestrict"].(map[string]any)
	}
	if restrict != nil {
		r := &TransferRestriction{}
		r.IsRestrictDeviceTransfer, _ = restrict["isRestrictDeviceTransfer"].(bool)
		r.IsWorldBloomChapter, _ = restrict["isWorldBloomChapter"].(bool)
		r.RestrictEndAt, _ = int64Field(restrict, "restrictEndAt")
		r.RestTransferCount, _ = int64Field(restrict, "restTransferCount")
		r.RestrictRank, _ = int64Field(restrict, "restrictRank")
		r.GameCharacterID, _ = int64Field(restrict, "gameCharacterId")
		preview.Restriction = r
	}
	return preview, nil
}

func stringField(m map[string]any, key string) (string, error) {
	v, _ := m[key].(string)
	if v == "" {
		return "", fmt.Errorf("response has no %s", key)
	}
	return v, nil
}

// int64Field reads an integer of any msgpack width.
func int64Field(m map[string]any, key string) (int64, bool) {
	switch v := m[key].(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case int32:
		return int64(v), true
	case int16:
		return int64(v), true
	case int8:
		return int64(v), true
	case uint64:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint8:
		return int64(v), true
	case float64:
		return int64(v), true
	}
	return 0, false
}
