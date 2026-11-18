package usersocial

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	_ "github.com/mattn/go-sqlite3"

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

func TestSocialVerificationIssuanceBindsChallengeAndEnforcesPerUserBudget(t *testing.T) {
	helper := newSocialRateLimitTestHelper(t)
	redis := helper.DBManager.Redis
	app := fiber.New()
	app.Post("/code/:user", func(c fiber.Ctx) error {
		c.Locals("userID", c.Params("user"))
		return c.Next()
	}, handleGenerateVerificationCode(helper))
	request := func(user, platformUser string, want int) harukiAPIHelper.GenerateSocialPlatformCodeResponse {
		t.Helper()
		raw, err := json.Marshal(harukiAPIHelper.GenerateSocialPlatformCodePayload{
			Platform: harukiAPIHelper.SocialPlatformQQ, UserID: platformUser,
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/code/"+user, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("issuance returned %d, want %d", resp.StatusCode, want)
		}
		var result harukiAPIHelper.GenerateSocialPlatformCodeResponse
		if want == http.StatusOK {
			if err := json.UnmarshalRead(resp.Body, &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}

	request("owner", "invalid value", http.StatusBadRequest)
	key := harukiRedis.BuildSocialPlatformVerificationIssueKey("owner")
	if err := redis.Redis.Set(t.Context(), key, 14, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	issued := request("owner", "fixture-target", http.StatusOK)
	if issued.StatusToken == "" || issued.OneTimePassword == "" {
		t.Fatal("verification response is missing required values")
	}
	var binding socialStatusTokenBinding
	if found, err := redis.GetCache(t.Context(), harukiRedis.BuildStatusTokenBindingKey(issued.StatusToken), &binding); err != nil || !found || binding.Platform != "qq" || binding.UserID != "fixture-target" {
		t.Fatalf("status token did not preserve its binding: found=%v err=%v", found, err)
	}
	request("owner", "other-target", http.StatusTooManyRequests)
	request("another-owner", "other-target", http.StatusOK)
	global := harukiRedis.BuildSocialPlatformVerificationIssueKey("")
	if err := redis.Redis.Set(t.Context(), global, 10000, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	request("fresh-owner", "fixture-target", http.StatusTooManyRequests)
	if err := redis.Redis.Set(t.Context(), global, "invalid-counter", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	request("fresh-owner", "fixture-target", http.StatusInternalServerError)
	if err := redis.Redis.Set(t.Context(), harukiRedis.BuildSocialPlatformVerificationIssueKey("broken-owner"), "invalid-counter", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	request("broken-owner", "fixture-target", http.StatusInternalServerError)
}

func TestBotVerificationBindsAndConsumesIssuedChallenge(t *testing.T) {
	helper := newSocialRateLimitTestHelper(t)
	redis := helper.DBManager.Redis
	keys := redis.KeyBuilder()
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	helper.DBManager.DB = db
	db.User.Create().SetID("owner").SetName("Owner").SetEmail("owner@example.test").ExecX(t.Context())
	app := fiber.New()
	app.Post("/verify", handleVerifySocialPlatform(helper, NewBotVerifyConfig(BotVerifyConfigOptions{Token: "fixture-bot-secret"})))
	check := func(account string, want int) {
		t.Helper()
		body := `{"platform":"qq","userId":"` + account + `","oneTimePassword":"fixture-code"}`
		req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer fixture-bot-secret")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("verification returned %d, want %d", resp.StatusCode, want)
		}
	}
	check("invalid account", http.StatusBadRequest)
	key := keys.BuildSocialVerificationChallengeKey("qq", "fixture-account")
	if err := redis.SetCache(t.Context(), key, "unreadable-legacy-state", time.Minute); err != nil {
		t.Fatal(err)
	}
	check("fixture-account", http.StatusInternalServerError)
	challenge, err := encodeSocialVerificationChallenge(keys, "fixture-code", "owner", "fixture-status")
	if err != nil {
		t.Fatal(err)
	}
	if err := redis.SetCache(t.Context(), key, challenge, time.Minute); err != nil {
		t.Fatal(err)
	}
	check("fixture-account", http.StatusOK)
	check("fixture-account", http.StatusBadRequest)
	row := db.SocialPlatformInfo.Query().OnlyX(t.Context())
	if !row.Verified || row.QueryUser().OnlyIDX(t.Context()) != "owner" || row.PlatformUserID != "fixture-account" {
		t.Fatalf("unexpected verified account: %s", row.PlatformUserID)
	}
	var status string
	if found, err := redis.GetCache(t.Context(), harukiRedis.BuildStatusTokenKey("fixture-status"), &status); err != nil || !found || status != "true" {
		t.Fatalf("verification state was not advanced: found=%v err=%v", found, err)
	}
}
