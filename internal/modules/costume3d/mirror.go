package costume3d

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// The bundle families the CostumeShop kernel reads: character models (with color
// variations and head accessories), the music items some of them depend on, the live
// shader, and the costume / preview / result / common motions. Nothing else of the mirror
// is served.
var bundlePrefixes = []string{
	"live_pv/model/character/",
	"live_pv/model/characterv2/",
	"live_pv/model/music_item/",
	"character/motion/costume_setting/",
	"model3d/motion/result/",
}
var bundleNames = map[string]bool{"shader/live": true, "model3d/motion/common/default": true}

var (
	bundleNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*(/[A-Za-z0-9_][A-Za-z0-9_.-]*)*$`)
	// "<assetVersion>.msgpack"; a Nuverse region's version is "<app version>-r<asset revision>".
	catalogFilePattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*(-r[0-9]+)?\.msgpack$`)
)

// The textures of the kernel's "archive" background (the character archive costume viewer's; APK player data, not
// CDN bundles), kept beside the bundles under ui/archive-background/.
var archiveBackgroundFiles = map[string]bool{"bg_sekai.png": true, "tex_sekai_v2_02.png": true, "tex_transition_01.png": true}

var errNotFound = errors.New("not found")

// costumeShopBundle reports whether a bundle name is one the CostumeShop kernel reads.
func costumeShopBundle(name string) bool {
	if !bundleNamePattern.MatchString(name) || strings.Contains(name, "..") {
		return false
	}
	if bundleNames[name] {
		return true
	}
	for _, prefix := range bundlePrefixes {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			return true
		}
	}
	return false
}

func (s *Service) mirrorGet(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.ua)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp, nil
	case http.StatusNotFound:
		_ = resp.Body.Close()
		return nil, errNotFound
	default:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
}

func (s *Service) mirrorBytes(ctx context.Context, u string, limit int64) ([]byte, error) {
	resp, err := s.mirrorGet(ctx, u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// current returns the mirror's current.json and the catalog file it names
// ("<assetVersion>.msgpack"), asked again at most once a minute.
func (s *Service) current(ctx context.Context, region string) ([]byte, string, error) {
	m, base, ok := s.regionMirror(region)
	if !ok {
		return nil, "", errNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil || time.Since(m.checkedAt) > versionCheckInterval {
		body, err := s.mirrorBytes(ctx, base+"current.json", 1<<20)
		if err != nil {
			if m.current == nil {
				return nil, "", err
			}
			s.logger.Warnf("costume3d: %s mirror current.json: %v", region, err)
		} else {
			var v struct {
				Catalog string `json:"catalog"`
			}
			if err := json.Unmarshal(body, &v); err != nil {
				return nil, "", fmt.Errorf("current.json: %w", err)
			}
			file := strings.TrimPrefix(v.Catalog, "catalogs/")
			if !catalogFilePattern.MatchString(file) {
				return nil, "", fmt.Errorf("current.json names catalog %q", v.Catalog)
			}
			m.current, m.catalogName = body, file
		}
		m.checkedAt = time.Now()
	}
	return m.current, m.catalogName, nil
}

// catalog returns a catalog file of the mirror, gzip-encoded; the current one stays in
// memory (it is about 38 MB raw).
func (s *Service) catalog(ctx context.Context, region, file string) ([]byte, error) {
	if !catalogFilePattern.MatchString(file) {
		return nil, errNotFound
	}
	_, currentFile, err := s.current(ctx, region)
	if err != nil {
		return nil, err
	}
	m, base, _ := s.regionMirror(region)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.catalogFile == file {
		return m.catalogGzip, nil
	}
	raw, err := s.mirrorBytes(ctx, base+"catalogs/"+file, 256<<20)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if file == currentFile {
		m.catalogFile, m.catalogGzip = file, buf.Bytes()
	}
	return buf.Bytes(), nil
}

// archiveBackground opens one archive background texture of the mirror; the caller closes the body.
func (s *Service) archiveBackground(ctx context.Context, region, file string) (*http.Response, error) {
	if !archiveBackgroundFiles[file] {
		return nil, errNotFound
	}
	return s.mirrorFile(ctx, region, "ui/archive-background/"+file)
}

// mirrorFile opens a file of the region's mirror; the caller closes the body.
func (s *Service) mirrorFile(ctx context.Context, region, path string) (*http.Response, error) {
	_, base, ok := s.regionMirror(region)
	if !ok {
		return nil, errNotFound
	}
	return s.mirrorGet(ctx, base+path)
}

// bundle opens one CostumeShop bundle of the mirror; the caller closes the body.
func (s *Service) bundle(ctx context.Context, region, name string) (*http.Response, error) {
	if !costumeShopBundle(name) {
		return nil, errNotFound
	}
	segments := strings.Split(name, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return s.mirrorFile(ctx, region, "bundles/"+strings.Join(segments, "/")+".bundle")
}
