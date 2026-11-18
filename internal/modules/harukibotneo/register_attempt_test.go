package harukibotneo

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestRecordInvalidVerificationAttemptUsesAtomicLimit(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	t.Cleanup(server.Close)

	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	manager := &harukiRedis.HarukiRedisManager{Redis: client}
	ctx := context.Background()
	const attemptKey = "bot:verify:attempt:test"
	const codeKey = "bot:verify:code:test"
	if err := manager.SetCache(ctx, codeKey, "123456", verifyCodeTTL); err != nil {
		t.Fatalf("seed verification code: %v", err)
	}

	for attempt := 1; attempt <= maxVerifyAttempts; attempt++ {
		limited, err := recordInvalidVerificationAttempt(ctx, manager, attemptKey, codeKey, "123456")
		if err != nil {
			t.Fatalf("attempt %d returned error: %v", attempt, err)
		}
		if limited {
			t.Fatalf("attempt %d was limited before maxVerifyAttempts", attempt)
		}
	}

	limited, err := recordInvalidVerificationAttempt(ctx, manager, attemptKey, codeKey, "123456")
	if err != nil {
		t.Fatalf("over-limit attempt returned error: %v", err)
	}
	if !limited {
		t.Fatal("over-limit attempt was not rejected")
	}
	if exists := server.Exists(codeKey); exists {
		t.Fatal("verification code remained after too many attempts")
	}
	if got, err := server.Get(attemptKey); err != nil || got != "6" {
		t.Fatalf("attempt counter after invalidation = %q, %v; want 6 retained until TTL", got, err)
	}
}

func TestRecordInvalidVerificationAttemptCannotResetUnderConcurrency(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	t.Cleanup(server.Close)

	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	manager := &harukiRedis.HarukiRedisManager{Redis: client}
	ctx := context.Background()
	const attemptKey = "bot:verify:attempt:concurrent"
	const codeKey = "bot:verify:code:concurrent"
	if err := manager.SetCache(ctx, codeKey, "123456", verifyCodeTTL); err != nil {
		t.Fatalf("seed verification code: %v", err)
	}

	const requests = 32
	start := make(chan struct{})
	var ready sync.WaitGroup
	var finished sync.WaitGroup
	var allowed atomic.Int64
	var limited atomic.Int64
	var failed atomic.Int64
	ready.Add(requests)
	finished.Add(requests)
	for range requests {
		go func() {
			defer finished.Done()
			ready.Done()
			<-start
			wasLimited, err := recordInvalidVerificationAttempt(ctx, manager, attemptKey, codeKey, "123456")
			switch {
			case err != nil:
				failed.Add(1)
			case wasLimited:
				limited.Add(1)
			default:
				allowed.Add(1)
			}
		}()
	}
	ready.Wait()
	close(start)
	finished.Wait()

	if failed.Load() != 0 {
		t.Fatalf("concurrent attempt errors = %d, want 0", failed.Load())
	}
	if got := allowed.Load(); got != int64(maxVerifyAttempts) {
		t.Fatalf("concurrent attempts below limit = %d, want exactly %d", got, maxVerifyAttempts)
	}
	if got := limited.Load(); got != requests-int64(maxVerifyAttempts) {
		t.Fatalf("concurrent limited attempts = %d, want %d", got, requests-int64(maxVerifyAttempts))
	}
	if got, err := server.Get(attemptKey); err != nil || got != "32" {
		t.Fatalf("final atomic counter = %q, %v; want 32", got, err)
	}
	if server.Exists(codeKey) {
		t.Fatal("verification code remained after concurrent limit was exceeded")
	}
}

func TestBotVerificationUsesIndependentActorAndRecipientBudgets(t *testing.T) {
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	keys := harukiRedis.NewKeyBuilder("fixture")
	if keys.BuildBotVerifyCodeKey("first", "123") == keys.BuildBotVerifyCodeKey("other", "123") {
		t.Fatal("verification codes share state across actors")
	}
	if keys.BuildBotRegisterRateLimitUserKey("first") == keys.BuildBotRegisterRateLimitUserKey("other") {
		t.Fatal("pre-proof register limits share state across actors")
	}
	redisKeys := []string{"ip", "recipient"}
	if err := client.Set(t.Context(), redisKeys[0], sendMailIPLimit, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	limits, err := client.Eval(t.Context(), sendMailRateLimitScript, redisKeys, sendMailIPLimit, sendMailTargetLimit, 60000).Int64Slice()
	if err != nil || limits[0] != rateLimitLimitedByIP || server.Exists(redisKeys[1]) {
		t.Fatalf("rejected sender spent recipient budget: %v, %v", limits, err)
	}
}
