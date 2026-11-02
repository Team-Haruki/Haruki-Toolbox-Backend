package sekai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ErrGameVersionOutdated marks a game answer of 426 Upgrade Required that a
// version refresh did not cure: the data or asset version the session sends
// is behind the game's, and no trusted source had a newer one yet.
var ErrGameVersionOutdated = errors.New("game version outdated")

// SubStepVersionRefresh reports a refresh of the version headers after a 426.
// Its note says where the new versions came from ("file", "login" or
// "response") or why none were found ("unchanged", "error"); it never carries
// a secret.
const SubStepVersionRefresh = "version_refresh"

// Version refresh sources, reported as the note of SubStepVersionRefresh.
const (
	VersionSourceFile     = "file"
	VersionSourceLogin    = "login"
	VersionSourceResponse = "response"
	versionRefreshNone    = "unchanged"
	versionRefreshError   = "error"
)

const (
	headerAppVersion   = "X-App-Version"
	headerAppHash      = "X-App-Hash"
	headerDataVersion  = "X-Data-Version"
	headerAssetVersion = "X-Asset-Version"
)

// IsUpgradeRequired reports whether err carries a game answer of 426.
func IsUpgradeRequired(err error) bool {
	return APIStatus(err) == http.StatusUpgradeRequired
}

// IsGameVersionOutdated reports whether err is a 426 the session could not
// recover from (or any 426, for a step that is never retried).
func IsGameVersionOutdated(err error) bool {
	return errors.Is(err, ErrGameVersionOutdated) || IsUpgradeRequired(err)
}

// versionHeaders is the data and asset version the session currently sends.
func (s *InheritSession) versionHeaders() (string, string) {
	return s.headers[headerDataVersion], s.headers[headerAssetVersion]
}

// adoptDataVersions sets the data and asset version headers from a game
// answer (the login response, or the body of a 426). Empty values are
// ignored. The app version and hash are never taken from the game: only the
// version file sets them.
func (s *InheritSession) adoptDataVersions(m map[string]any) bool {
	data, _ := m["dataVersion"].(string)
	asset, _ := m["assetVersion"].(string)
	changed := false
	if data != "" && data != s.headers[headerDataVersion] {
		s.headers[headerDataVersion] = data
		changed = true
	}
	if asset != "" && asset != s.headers[headerAssetVersion] {
		s.headers[headerAssetVersion] = asset
		changed = true
	}
	return changed
}

// retryOnUpgrade runs one game call that is safe to repeat. When the game
// answers 426, it refreshes the version headers and, if they changed, runs
// the call once more. A 426 that survives is wrapped in
// ErrGameVersionOutdated. run returns the raw response body, which on a 426
// may name the versions the game expects.
//
// ⚠️ Never wrap the executing inherit call: it uses up the transfer code, so
// it must reach the game at most once.
func (s *InheritSession) retryOnUpgrade(ctx context.Context, allowRelogin bool, run func() ([]byte, error)) error {
	body, err := run()
	if !IsUpgradeRequired(err) {
		return err
	}
	if !s.refreshVersions(ctx, body, allowRelogin) {
		return fmt.Errorf("%w: %w", ErrGameVersionOutdated, err)
	}
	_, err = run()
	if IsUpgradeRequired(err) {
		return fmt.Errorf("%w: %w", ErrGameVersionOutdated, err)
	}
	return err
}

// refreshVersions looks for newer data and asset versions after a 426, in
// order of trust, and reports whether the headers changed:
//
//  1. the version file, fetched again past any cache. Its app version and
//     hash always apply; its data and asset versions apply only when the
//     file itself moved since it was last read, so a stale file cannot undo
//     versions a login already reported;
//  2. a fresh login with the session's credential (only once logged in, and
//     not from inside Login), whose answer carries the current data and
//     asset versions, the way the game client and Sekai-API learn them;
//  3. the 426 answer itself, when it names dataVersion / assetVersion.
func (s *InheritSession) refreshVersions(ctx context.Context, upgradeBody []byte, allowRelogin bool) bool {
	s.report(SubStepVersionRefresh, ProgressRunning, "")
	beforeData, beforeAsset := s.versionHeaders()
	beforeApp, beforeHash := s.headers[headerAppVersion], s.headers[headerAppHash]
	dataChanged := func() bool {
		data, asset := s.versionHeaders()
		return data != beforeData || asset != beforeAsset
	}
	done := func(source string) bool {
		data, asset := s.versionHeaders()
		s.report(SubStepVersionRefresh, ProgressDone, fmt.Sprintf("%s data=%s asset=%s", source, data, asset))
		return true
	}

	fileErr := s.fetchVersion(ctx, true)
	if fileErr == nil && dataChanged() {
		return done(VersionSourceFile)
	}
	if allowRelogin && s.credential != "" {
		if _, err := s.authenticate(ctx); err == nil && dataChanged() {
			return done(VersionSourceLogin)
		}
	}
	if len(upgradeBody) > 0 {
		if m, err := s.unpack(upgradeBody); err == nil && s.adoptDataVersions(m) {
			return done(VersionSourceResponse)
		}
	}
	// A new app version (or hash) from the file is a refresh too: it is what a
	// 426 on login usually means. It is checked last so it never skips the
	// login or answer steps that could also bring newer data versions.
	if fileErr == nil && (s.headers[headerAppVersion] != beforeApp || s.headers[headerAppHash] != beforeHash) {
		return done(VersionSourceFile)
	}
	note := versionRefreshNone
	if fileErr != nil {
		note = versionRefreshError
	}
	s.report(SubStepVersionRefresh, ProgressFailed, note)
	return false
}

// compareVersions compares dotted numeric versions ("7.1.0.45"). ok is false
// when either is not of that form.
func compareVersions(a, b string) (cmp int, ok bool) {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var na, nb int
		var err error
		if i < len(pa) {
			if na, err = strconv.Atoi(pa[i]); err != nil || na < 0 {
				return 0, false
			}
		}
		if i < len(pb) {
			if nb, err = strconv.Atoi(pb[i]); err != nil || nb < 0 {
				return 0, false
			}
		}
		if na != nb {
			if na < nb {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// fileVersionApplies reports whether a data or asset version read from the
// version file replaces the current header: always on the first read; later
// only when the file moved, and never to a version older than the current one
// (which a login or a 426 answer may have set). Versions that do not compare
// numerically are taken when the file moved.
func (s *InheritSession) fileVersionApplies(fileValue, lastFileValue, current string) bool {
	if !s.versionRead {
		return true
	}
	if fileValue == lastFileValue {
		return false
	}
	if cmp, ok := compareVersions(fileValue, current); ok && cmp < 0 {
		return false
	}
	return true
}
