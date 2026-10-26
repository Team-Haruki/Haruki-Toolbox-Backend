package usersponsor

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/gofiber/fiber/v3"
)

const previewRoute = "/api/misc/sponsor-frames/"
const maxCatalogBytes = 1 << 20
const maxPreviewBytes = 8 << 20

var frameIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,95}$`)
var previewNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,159}\.(png|jpg|jpeg|webp)$`)

// Catalog reads catalog.json and previews/ from an operator-owned directory.
// Mount the directory (not the JSON file) read-only so atomic rename updates
// remain visible. Each request reloads the manifest; invalid updates retain the
// last valid snapshot. An invalid initial manifest fails application startup.
// Preview bytes are held in the same snapshot as their content-addressed URLs.
type Catalog struct {
	mu        sync.Mutex
	directory string
	publicURL string
	frames    []Frame
	previews  map[string]previewImage
	lastError string
	logger    *logger.Logger
}
type previewImage struct {
	data                []byte
	contentType, digest string
	info                os.FileInfo
}

func NewCatalog(directory, publicURL string) (*Catalog, error) {
	if directory == "" && publicURL == "" {
		return nil, nil
	}
	u, err := url.Parse(publicURL)
	if directory == "" || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("sponsor frame catalog requires a directory and an HTTPS public API origin")
	}
	c := &Catalog{directory: directory, publicURL: strings.TrimRight(publicURL, "/"), logger: logger.NewLoggerFromGlobal("SponsorFrames")}
	if err := c.reload(); err != nil {
		return nil, fmt.Errorf("load sponsor frame catalog: %w", err)
	}
	return c, nil
}

func readCatalogFile(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid catalog asset size or type")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("catalog asset exceeds size limit")
	}
	return data, nil
}

func validParts(parts FrameParts) bool {
	for _, p := range []string{parts.Base, parts.CenterTop, parts.LeftTop, parts.RightTop, parts.LeftBottom, parts.RightBottom} {
		if !strings.HasPrefix(p, "static_images/custom_frames/") || path.Clean(p) != p || strings.ContainsAny(p, "\\\x00?#:") || path.Ext(p) != ".png" {
			return false
		}
	}
	return true
}

func (c *Catalog) loadPreview(root *os.Root, source string, images map[string]previewImage) (string, error) {
	name := strings.TrimPrefix(source, "/sponsor-frames/")
	if !previewNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid preview filename")
	}
	if _, ok := images[name]; !ok {
		info, err := root.Stat("previews/" + name)
		if err != nil {
			return "", err
		}
		if previous, ok := c.previews[name]; ok && os.SameFile(previous.info, info) && previous.info.Size() == info.Size() && previous.info.ModTime() == info.ModTime() {
			images[name] = previous
		} else {
			data, err := readCatalogFile(root, "previews/"+name, maxPreviewBytes)
			if err != nil {
				return "", err
			}
			ct := http.DetectContentType(data)
			if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" {
				return "", fmt.Errorf("invalid preview image type")
			}
			images[name] = previewImage{data: data, contentType: ct, digest: fmt.Sprintf("%x", sha256.Sum256(data)), info: info}
		}
	}
	return c.publicURL + previewRoute + name + "?v=" + images[name].digest, nil
}

func (c *Catalog) reload() error {
	root, err := os.OpenRoot(c.directory)
	if err != nil {
		return err
	}
	defer root.Close()
	data, err := readCatalogFile(root, "catalog.json", maxCatalogBytes)
	if err != nil {
		return err
	}
	var frames []Frame
	if err := jsoncodec.Unmarshal(data, &frames); err != nil {
		return fmt.Errorf("invalid catalog JSON")
	}
	if frames == nil || len(frames) > 256 {
		return fmt.Errorf("catalog must be an array of at most 256 frames")
	}
	seen := make(map[string]bool)
	images := make(map[string]previewImage)
	for i := range frames {
		f := &frames[i]
		if !frameIDPattern.MatchString(f.ID) || strings.HasPrefix(f.ID, "icon_user_avatar_frame_") || seen[f.ID] {
			return fmt.Errorf("invalid, reserved or duplicate frame ID")
		}
		seen[f.ID] = true
		if strings.TrimSpace(f.Name["zh-CN"]) == "" || !validParts(f.Horizontal) || !validParts(f.Vertical) {
			return fmt.Errorf("invalid frame name or parts")
		}
		for _, p := range []*string{&f.Thumbnail, &f.PanelPreview, &f.ProfilePreview} {
			*p, err = c.loadPreview(root, *p, images)
			if err != nil {
				return fmt.Errorf("invalid frame preview: %w", err)
			}
		}
		total := 0
		for _, image := range images {
			total += len(image.data)
		}
		if total > 64<<20 {
			return fmt.Errorf("catalog preview total exceeds 64 MiB")
		}
	}
	c.frames, c.previews = frames, images
	return nil
}

func (c *Catalog) refreshLocked() {
	if err := c.reload(); err != nil {
		if err.Error() != c.lastError {
			c.logger.Warnf("Retaining previous sponsor frame catalog: %v", err)
		}
		c.lastError = err.Error()
	} else {
		c.lastError = ""
	}
}

func (c *Catalog) snapshot() []Frame {
	if c == nil {
		return frameCatalog()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshLocked()
	return c.frames
}

func lookupFrame(frames []Frame, id string) *Frame {
	for _, f := range frames {
		if f.ID == id {
			return &f
		}
	}
	return nil
}

// Only validated catalog previews can be served; no request path is opened.
func (c *Catalog) preview(ctx fiber.Ctx) error {
	c.mu.Lock()
	c.refreshLocked()
	img, ok := c.previews[ctx.Params("filename")]
	c.mu.Unlock()
	if !ok {
		return ctx.SendStatus(fiber.StatusNotFound)
	}
	ctx.Set("Cache-Control", "no-cache")
	ctx.Set("X-Content-Type-Options", "nosniff")
	ctx.Set("Content-Type", img.contentType)
	etag := `"` + img.digest + `"`
	ctx.Set("ETag", etag)
	if ctx.Get("If-None-Match") == etag {
		return ctx.SendStatus(fiber.StatusNotModified)
	}
	return ctx.Send(img.data)
}
