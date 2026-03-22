package upload

import (
	"context"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	openUploadRateLimitPerMinute = 180
	openUploadRateLimitWindow    = time.Minute
	openUploadRateLimitKeyTTL    = 2 * time.Minute
)

var fallbackOpenUploadRateLimiter = newOpenUploadRateLimiter(openUploadRateLimitPerMinute, openUploadRateLimitWindow)

type openUploadRateLimiter struct {
	mu          sync.Mutex
	window      time.Duration
	maxRequests int
	buckets     map[string]openUploadRateBucket
	hits        uint64
}

type openUploadRateBucket struct {
	windowStart time.Time
	count       int
}

func newOpenUploadRateLimiter(maxRequests int, window time.Duration) *openUploadRateLimiter {
	return &openUploadRateLimiter{
		window:      window,
		maxRequests: maxRequests,
		buckets:     make(map[string]openUploadRateBucket),
	}
}

func (l *openUploadRateLimiter) allow(now time.Time, key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[key]
	if !ok || now.Sub(bucket.windowStart) >= l.window {
		l.buckets[key] = openUploadRateBucket{windowStart: now, count: 1}
		l.hits++
		if l.hits%256 == 0 {
			l.cleanupLocked(now)
		}
		return true
	}
	if bucket.count >= l.maxRequests {
		return false
	}

	bucket.count++
	l.buckets[key] = bucket
	return true
}

func (l *openUploadRateLimiter) cleanupLocked(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.windowStart) >= l.window {
			delete(l.buckets, key)
		}
	}
}

func openUploadEntryGuard(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		now := time.Now().UTC()
		bucketKey := buildOpenUploadRateLimitKey(c)

		allowed, retryAfter, err := consumeOpenUploadRateLimit(c.Context(), apiHelper, bucketKey, now)
		if err != nil {
			// Fallback to in-process limiter when Redis is unavailable.
			harukiLogger.Warnf("Open upload redis rate limiter fallback: %v", err)
			if fallbackOpenUploadRateLimiter.allow(now, bucketKey) {
				return c.Next()
			}
			retryAfter = int(openUploadRateLimitWindow / time.Second)
		}

		if allowed {
			return c.Next()
		}

		if retryAfter <= 0 {
			retryAfter = int(openUploadRateLimitWindow / time.Second)
			if retryAfter <= 0 {
				retryAfter = 60
			}
		}
		c.Set("Retry-After", strconv.Itoa(retryAfter))
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusTooManyRequests, "too many requests", nil)
	}
}

func consumeOpenUploadRateLimit(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, bucketKey string, now time.Time) (bool, int, error) {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil {
		return false, 0, fiber.NewError(fiber.StatusInternalServerError, "redis rate limiter not initialized")
	}

	windowSlot := now.Unix() / int64(openUploadRateLimitWindow/time.Second)
	key := harukiRedis.BuildUploadIngressRateLimitKey(windowSlot, bucketKey)
	count, err := apiHelper.DBManager.Redis.IncrementWithTTL(ctx, key, openUploadRateLimitKeyTTL)
	if err != nil {
		return false, 0, err
	}
	if count <= int64(openUploadRateLimitPerMinute) {
		return true, 0, nil
	}
	return false, secondsUntilWindowReset(now, openUploadRateLimitWindow), nil
}

func secondsUntilWindowReset(now time.Time, window time.Duration) int {
	if window <= 0 {
		return 0
	}
	elapsedInWindow := now.UnixNano() % int64(window)
	remaining := int64(window) - elapsedInWindow
	if remaining <= 0 {
		return 0
	}
	remainingDuration := time.Duration(remaining)
	remainingSeconds := int((remainingDuration + time.Second - 1) / time.Second)
	if remainingSeconds <= 0 {
		return 1
	}
	return remainingSeconds
}

func buildOpenUploadRateLimitKey(c fiber.Ctx) string {
	routePath := c.Path()
	if route := c.Route(); route != nil {
		routePattern := strings.TrimSpace(route.Path)
		if routePattern != "" {
			routePath = routePattern
		}
	}
	return c.IP() + "|" + c.Method() + "|" + routePath
}
