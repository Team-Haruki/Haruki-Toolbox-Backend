package costume3d

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"

	"github.com/gofiber/fiber/v3"
)

// Public routes sit under /api/misc/, which Oathkeeper already forwards unauthenticated
// for GET (rule haruki-public-misc). The compression middleware skips this prefix: the
// handlers send gzip bodies of their own and stream bundles as they are.
const routePrefix = "/api/misc/costume3d"

const (
	cacheRevalidate = "no-cache"
	cacheLong       = "public, max-age=2592000"
	cacheMaster     = "public, max-age=300"
	cacheDay        = "public, max-age=86400"
)

// RegisterRoutes mounts the public costume3d API; s may be nil (feature off).
const (
	paramRegion     = "region"
	paramFile       = "file"
	notFoundMessage = "not found"
)

func RegisterRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, s *Service) {
	if s == nil {
		return
	}
	s.mount(apiHelper.Router.Group(routePrefix))
}

func (s *Service) mount(g fiber.Router) {
	g.Get("/:region/current.json", s.handleCurrent)
	g.Get("/:region/catalogs/:file", s.handleCatalog)
	g.Get("/:region/bundles/*", s.handleBundle)
	g.Get("/:region/master/characters/:file", s.handleMaster)
	g.Get("/:region/ui/archive-background/:file", s.handleArchiveBackground)
}

func acceptsGzip(c fiber.Ctx) bool {
	return strings.Contains(strings.ToLower(c.Get(fiber.HeaderAcceptEncoding)), "gzip")
}

// sendGzip sends a gzip body as it is, or decoded for a client that does not take gzip.
func sendGzip(c fiber.Ctx, body []byte, contentType string) error {
	c.Set(fiber.HeaderContentType, contentType)
	c.Set(fiber.HeaderVary, fiber.HeaderAcceptEncoding)
	if acceptsGzip(c) {
		c.Set(fiber.HeaderContentEncoding, "gzip")
		return c.Send(body)
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return harukiAPIHelper.ErrorInternal(c, "failed to decode content")
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return harukiAPIHelper.ErrorInternal(c, "failed to decode content")
	}
	return c.Send(raw)
}

func (s *Service) upstreamError(c fiber.Ctx, what string, err error) error {
	if errors.Is(err, errNotFound) {
		return harukiAPIHelper.ErrorNotFound(c, notFoundMessage)
	}
	s.logger.Warnf("costume3d: %s: %v", what, err)
	return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"status": fiber.StatusBadGateway, "message": "upstream unavailable"})
}

func (s *Service) handleCurrent(c fiber.Ctx) error {
	region := c.Params(paramRegion)
	body, _, err := s.current(c.Context(), region)
	if err != nil {
		return s.upstreamError(c, region+" current.json", err)
	}
	c.Set(fiber.HeaderCacheControl, cacheRevalidate)
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Send(body)
}

func (s *Service) handleCatalog(c fiber.Ctx) error {
	region, file := c.Params(paramRegion), c.Params(paramFile)
	body, err := s.catalog(c.Context(), region, file)
	if err != nil {
		return s.upstreamError(c, region+" catalog "+file, err)
	}
	// A catalog file is named by its asset version and never changes.
	c.Set(fiber.HeaderCacheControl, cacheLong)
	return sendGzip(c, body, "application/msgpack")
}

func (s *Service) handleBundle(c fiber.Ctx) error {
	region := c.Params(paramRegion)
	name, ok := strings.CutSuffix(c.Params("*"), ".bundle")
	if !ok {
		return harukiAPIHelper.ErrorNotFound(c, notFoundMessage)
	}
	// The body streams after the handler returns, so the upstream request must not end
	// with the request context; the client timeout bounds it.
	resp, err := s.bundle(context.Background(), region, name)
	if err != nil {
		return s.upstreamError(c, region+" bundle "+name, err)
	}
	// The mirror overwrites a bundle in place: only a URL keyed by its catalog hash
	// (?h=, as the kernel's createMirrorContent asks) may be cached.
	if c.Query("h") != "" {
		c.Set(fiber.HeaderCacheControl, cacheLong)
	} else {
		c.Set(fiber.HeaderCacheControl, cacheRevalidate)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEOctetStream)
	size := -1
	if resp.ContentLength >= 0 {
		size = int(resp.ContentLength)
	}
	return c.SendStream(resp.Body, size)
}

func (s *Service) handleArchiveBackground(c fiber.Ctx) error {
	region, file := c.Params(paramRegion), c.Params(paramFile)
	resp, err := s.archiveBackground(context.Background(), region, file)
	if err != nil {
		return s.upstreamError(c, region+" archive background "+file, err)
	}
	// Unversioned names: cached for a day, the textures change only with a new APK.
	c.Set(fiber.HeaderCacheControl, cacheDay)
	c.Set(fiber.HeaderContentType, "image/png")
	size := -1
	if resp.ContentLength >= 0 {
		size = int(resp.ContentLength)
	}
	return c.SendStream(resp.Body, size)
}

func (s *Service) handleMaster(c fiber.Ctx) error {
	region := c.Params(paramRegion)
	idText, ok := strings.CutSuffix(c.Params(paramFile), ".json")
	characterID, err := strconv.Atoi(idText)
	if !ok || err != nil || characterID <= 0 {
		return harukiAPIHelper.ErrorNotFound(c, notFoundMessage)
	}
	body, hash, err := s.characterMaster(c.Context(), region, characterID)
	if err != nil {
		return s.upstreamError(c, region+" master", err)
	}
	if body == nil {
		return harukiAPIHelper.ErrorNotFound(c, notFoundMessage)
	}
	etag := `"` + hash + "-" + idText + `"`
	c.Set(fiber.HeaderETag, etag)
	c.Set(fiber.HeaderCacheControl, cacheMaster)
	if c.Get(fiber.HeaderIfNoneMatch) == etag {
		return c.SendStatus(fiber.StatusNotModified)
	}
	return sendGzip(c, body, fiber.MIMEApplicationJSON)
}
