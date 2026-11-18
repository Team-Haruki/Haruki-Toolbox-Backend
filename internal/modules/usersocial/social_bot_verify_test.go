package usersocial

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"

	"github.com/gofiber/fiber/v3"
)

func TestVerifySocialPlatformUsesRouteScopedBotVerifyConfig(t *testing.T) {
	t.Parallel()

	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
	app := fiber.New()
	app.Post("/first", handleVerifySocialPlatform(apiHelper, NewBotVerifyConfig(BotVerifyConfigOptions{Token: "first-secret"})))
	app.Post("/second", handleVerifySocialPlatform(apiHelper, NewBotVerifyConfig(BotVerifyConfigOptions{Token: "second-secret"})))

	tests := []struct {
		name       string
		path       string
		token      string
		wantStatus int
	}{
		{name: "first route accepts first secret", path: "/first", token: "first-secret", wantStatus: fiber.StatusBadRequest},
		{name: "second route rejects first secret", path: "/second", token: "first-secret", wantStatus: fiber.StatusUnauthorized},
		{name: "second route accepts second secret", path: "/second", token: "second-secret", wantStatus: fiber.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader("{"))
			req.Header.Set("Authorization", "Bearer "+test.token)
			req.Header.Set("Content-Type", "application/json")
			response, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() error: %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
			}
		})
	}
}

func TestVerifySocialPlatformAtomicallyLimitsConcurrentGuesses(t *testing.T) {
	const requestCount = 32

	apiHelper := newSocialRateLimitTestHelper(t)
	redis := apiHelper.DBManager.Redis
	keys := redis.KeyBuilder()
	storageKey := keys.BuildSocialVerificationChallengeKey(string(harukiAPIHelper.SocialPlatformQQ), "target-user")
	challenge, err := encodeSocialVerificationChallenge(keys, "correct-code", "owner", "status-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := redis.SetCache(t.Context(), storageKey, challenge, time.Minute); err != nil {
		t.Fatalf("SetCache() error: %v", err)
	}

	app := fiber.New()
	app.Post("/verify", handleVerifySocialPlatform(apiHelper, NewBotVerifyConfig(BotVerifyConfigOptions{Token: "bot-secret"})))

	statuses := make(chan int, requestCount)
	errs := make(chan error, requestCount)
	var wg sync.WaitGroup
	for range requestCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(`{"platform":"qq","userId":"target-user","oneTimePassword":"wrong-code"}`))
			req.Header.Set("Authorization", "Bearer bot-secret")
			req.Header.Set("Content-Type", "application/json")
			response, err := app.Test(req)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = response.Body.Close() }()
			statuses <- response.StatusCode
		}()
	}
	wg.Wait()
	close(errs)
	close(statuses)

	for err := range errs {
		t.Fatalf("app.Test() error: %v", err)
	}
	unauthorized := 0
	tooManyAttempts := 0
	for status := range statuses {
		switch status {
		case fiber.StatusUnauthorized:
			unauthorized++
		case fiber.StatusBadRequest:
			tooManyAttempts++
		default:
			t.Fatalf("unexpected status: %d", status)
		}
	}
	if unauthorized != socialPlatformVerifyMaxAttempts {
		t.Fatalf("unauthorized guesses = %d, want %d", unauthorized, socialPlatformVerifyMaxAttempts)
	}
	if tooManyAttempts != requestCount-socialPlatformVerifyMaxAttempts {
		t.Fatalf("limited guesses = %d, want %d", tooManyAttempts, requestCount-socialPlatformVerifyMaxAttempts)
	}
}

func TestSocialChallengeConsumesOnlyTheObservedGeneration(t *testing.T) {
	redis := newSocialRateLimitTestHelper(t).DBManager.Redis
	keys := redis.KeyBuilder()
	storageKey := keys.BuildSocialVerificationChallengeKey("qq", "target-user")
	first, err := encodeSocialVerificationChallenge(keys, "code-a", "owner-a", "status-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := encodeSocialVerificationChallenge(keys, "code-b", "owner-b", "status-b")
	if err != nil {
		t.Fatal(err)
	}
	if err := redis.SetCache(t.Context(), storageKey, second, time.Minute); err != nil {
		t.Fatal(err)
	}
	if consumed, err := redis.DeleteCacheIfValueMatches(t.Context(), storageKey, first); err != nil || consumed {
		t.Fatalf("old generation consumed newer record: %t, %v", consumed, err)
	}
	if consumed, err := redis.DeleteCacheIfValueMatches(t.Context(), storageKey, second); err != nil || !consumed {
		t.Fatalf("current challenge was not consumed: %t, %v", consumed, err)
	}
	if consumed, err := redis.DeleteCacheIfValueMatches(t.Context(), storageKey, second); err != nil || consumed {
		t.Fatalf("consumed challenge was reused: %t, %v", consumed, err)
	}
}
