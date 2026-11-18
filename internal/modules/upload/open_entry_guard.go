package upload

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

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

// openUploadEntryGuard rate-limits the open upload entry points. onLimited,
// if given, runs on a request it refuses, before the 429 is written.
func openUploadEntryGuard(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, onLimited ...func(fiber.Ctx)) fiber.Handler {
	return func(c fiber.Ctx) error {
		retryAfter := openUploadRetryAfter(c.Context(), apiHelper, buildOpenUploadRateLimitKey(c), time.Now().UTC())
		if retryAfter == 0 {
			return c.Next()
		}
		c.Set("Retry-After", strconv.Itoa(retryAfter))
		for _, hook := range onLimited {
			hook(c)
		}
		return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusTooManyRequests, "too many requests", nil)
	}
}

// A verified iOS script uses its own namespace and quota, independently of
// the apparent peer address and the anonymous proxy budget.
func iosScriptUploadRateBucket(userID string) string {
	return "ios-script|" + userID
}

func openUploadRetryAfter(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, bucket string, now time.Time) int {
	allowed, retryAfter, err := consumeOpenUploadRateLimit(ctx, apiHelper, bucket, now)
	if err != nil {
		harukiLogger.Warnf("Open upload redis rate limiter fallback: %v", err)
		if fallbackOpenUploadRateLimiter.allow(now, bucket) {
			return 0
		}
	}
	if allowed {
		return 0
	}
	if retryAfter <= 0 {
		return int(openUploadRateLimitWindow / time.Second)
	}
	return retryAfter
}

func consumeOpenUploadRateLimit(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, bucketKey string, now time.Time) (bool, int, error) {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil || apiHelper.DBManager.Redis.Redis == nil {
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
