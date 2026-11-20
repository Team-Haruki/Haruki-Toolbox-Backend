package usergamebindings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	harukiSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/ent/toolbox/schema"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailnotify"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountdatagrant"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekaiapi"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
	goredis "github.com/redis/go-redis/v9"
)

func TestIsNumericGameUserID(t *testing.T) {
	t.Parallel()

	if !isNumericGameUserID("1241241241") {
		t.Fatalf("numeric game user id should be valid")
	}
	if isNumericGameUserID("") {
		t.Fatalf("empty game user id should be invalid")
	}
	if isNumericGameUserID("abc123") {
		t.Fatalf("non-numeric game user id should be invalid")
	}
	if isNumericGameUserID("123 456") {
		t.Fatalf("spaced game user id should be invalid")
	}
}

func TestBindingOwnerHelpers(t *testing.T) {
	t.Parallel()

	ownerUser := &postgresql.User{ID: "u-1"}
	bindingOwned := &postgresql.GameAccountBinding{}
	bindingOwned.Edges.User = ownerUser

	bindingOrphan := &postgresql.GameAccountBinding{}

	if got := bindingOwnerID(nil); got != "" {
		t.Fatalf("bindingOwnerID(nil) = %q, want empty", got)
	}
	if got := bindingOwnerID(bindingOrphan); got != "" {
		t.Fatalf("bindingOwnerID(orphan) = %q, want empty", got)
	}
	if got := bindingOwnerID(bindingOwned); got != "u-1" {
		t.Fatalf("bindingOwnerID(owned) = %q, want %q", got, "u-1")
	}

	if !bindingOwnerMissing(bindingOrphan) {
		t.Fatalf("bindingOwnerMissing(orphan) should be true")
	}
	if bindingOwnerMissing(bindingOwned) {
		t.Fatalf("bindingOwnerMissing(owned) should be false")
	}
	if bindingOwnerBanned(bindingOwned) {
		t.Fatalf("bindingOwnerBanned should return false for active owner")
	}
	bannedOwnerBinding := &postgresql.GameAccountBinding{}
	bannedOwnerBinding.Edges.User = &postgresql.User{ID: "u-3", Banned: true}
	if !bindingOwnerBanned(bannedOwnerBinding) {
		t.Fatalf("bindingOwnerBanned should return true for banned owner")
	}

	if !isBindingOwnedByUser(bindingOwned, "u-1") {
		t.Fatalf("isBindingOwnedByUser should return true for same user")
	}
	if isBindingOwnedByUser(bindingOwned, "u-2") {
		t.Fatalf("isBindingOwnedByUser should return false for different user")
	}
	if isBindingOwnedByUser(bindingOrphan, "u-1") {
		t.Fatalf("isBindingOwnedByUser should return false for orphan binding")
	}
}

func TestClassifyExistingBinding(t *testing.T) {
	t.Parallel()

	orphan := &postgresql.GameAccountBinding{}
	if got := classifyExistingBinding(orphan, "u-1"); got != existingBindingStateNone {
		t.Fatalf("classify orphan = %v, want %v", got, existingBindingStateNone)
	}

	ownedByOther := &postgresql.GameAccountBinding{}
	ownedByOther.Edges.User = &postgresql.User{ID: "u-2"}
	if got := classifyExistingBinding(ownedByOther, "u-1"); got != existingBindingStateOwnedByOther {
		t.Fatalf("classify owned-by-other = %v, want %v", got, existingBindingStateOwnedByOther)
	}

	verifiedBySelf := &postgresql.GameAccountBinding{Verified: true}
	verifiedBySelf.Edges.User = &postgresql.User{ID: "u-1"}
	if got := classifyExistingBinding(verifiedBySelf, "u-1"); got != existingBindingStateVerifiedBySelf {
		t.Fatalf("classify verified-by-self = %v, want %v", got, existingBindingStateVerifiedBySelf)
	}
}

func TestSaveGameAccountBindingTransfersOwnerAndClearsOldGrants(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:game-account-binding-transfer-test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() {
		_ = client.Close()
	})
	ctx := context.Background()

	for _, seed := range []struct {
		id    string
		email string
	}{
		{id: "old-owner", email: "old-owner@example.com"},
		{id: "new-owner", email: "new-owner@example.com"},
		{id: "grantee", email: "grantee@example.com"},
	} {
		if _, err := client.User.Create().
			SetID(seed.id).
			SetName(seed.id).
			SetEmail(seed.email).
			Save(ctx); err != nil {
			t.Fatalf("create user %s returned error: %v", seed.id, err)
		}
	}
	binding, err := client.GameAccountBinding.Create().
		SetServer("jp").
		SetGameUserID("123456").
		SetVerified(true).
		SetSuite(&harukiSchema.SuiteDataPrivacySettings{AllowPublicApi: true}).
		SetMysekai(&harukiSchema.MysekaiDataPrivacySettings{AllowPublicApi: true}).
		SetUserID("old-owner").
		SetSponsorFrameID("previous-frame").
		Save(ctx)
	if err != nil {
		t.Fatalf("create binding returned error: %v", err)
	}
	if _, err := client.GameAccountDataGrant.Create().
		SetOwnerUserID("old-owner").
		SetGranteeUserID("grantee").
		SetServer("jp").
		SetGameUserID("123456").
		SetDataType("suite").
		SetExpiresAt(time.Now().Add(time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("create grant returned error: %v", err)
	}
	existing, err := client.GameAccountBinding.Query().
		Where(gameaccountbinding.IDEQ(binding.ID)).
		WithUser().
		Only(ctx)
	if err != nil {
		t.Fatalf("query binding returned error: %v", err)
	}
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{DB: client},
	}

	saveResult, err := saveGameAccountBinding(ctx, helper, existing, "jp", "123456", "new-owner", harukiAPIHelper.CreateGameAccountBindingPayload{
		Suite:   &harukiSchema.SuiteDataPrivacySettings{AllowPublicApi: false},
		MySekai: &harukiSchema.MysekaiDataPrivacySettings{AllowPublicApi: false},
	})
	if err != nil {
		t.Fatalf("saveGameAccountBinding returned error: %v", err)
	}
	if saveResult == nil || !saveResult.Transferred || saveResult.PreviousOwnerUserID != "old-owner" || saveResult.PreviousOwnerEmail != "old-owner@example.com" {
		t.Fatalf("save result = %+v, want transfer from old owner", saveResult)
	}
	updated, err := client.GameAccountBinding.Query().
		Where(gameaccountbinding.IDEQ(binding.ID)).
		WithUser().
		Only(ctx)
	if err != nil {
		t.Fatalf("query updated binding returned error: %v", err)
	}
	if updated.SponsorFrameID != nil {
		t.Fatal("transferred account retained previous owner frame")
	}
	if updated.Edges.User == nil || updated.Edges.User.ID != "new-owner" {
		t.Fatalf("updated owner = %+v, want new-owner", updated.Edges.User)
	}
	grantCount, err := client.GameAccountDataGrant.Query().
		Where(
			gameaccountdatagrant.OwnerUserIDEQ("old-owner"),
			gameaccountdatagrant.ServerEQ("jp"),
			gameaccountdatagrant.GameUserIDEQ("123456"),
		).
		Count(ctx)
	if err != nil {
		t.Fatalf("count grants returned error: %v", err)
	}
	if grantCount != 0 {
		t.Fatalf("old owner grants = %d, want 0", grantCount)
	}
}

func TestSaveGameAccountBindingRejectsTransferFromBannedOwner(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:game-account-binding-banned-owner-transfer-test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() {
		_ = client.Close()
	})
	ctx := context.Background()

	for _, seed := range []struct {
		id     string
		email  string
		banned bool
	}{
		{id: "old-owner", email: "old-owner@example.com", banned: true},
		{id: "new-owner", email: "new-owner@example.com"},
		{id: "grantee", email: "grantee@example.com"},
	} {
		create := client.User.Create().
			SetID(seed.id).
			SetName(seed.id).
			SetEmail(seed.email)
		if seed.banned {
			create.SetBanned(true)
		}
		if _, err := create.Save(ctx); err != nil {
			t.Fatalf("create user %s returned error: %v", seed.id, err)
		}
	}
	binding, err := client.GameAccountBinding.Create().
		SetServer("jp").
		SetGameUserID("123456").
		SetVerified(true).
		SetSuite(&harukiSchema.SuiteDataPrivacySettings{AllowPublicApi: true}).
		SetMysekai(&harukiSchema.MysekaiDataPrivacySettings{AllowPublicApi: true}).
		SetUserID("old-owner").
		Save(ctx)
	if err != nil {
		t.Fatalf("create binding returned error: %v", err)
	}
	if _, err := client.GameAccountDataGrant.Create().
		SetOwnerUserID("old-owner").
		SetGranteeUserID("grantee").
		SetServer("jp").
		SetGameUserID("123456").
		SetDataType("suite").
		SetExpiresAt(time.Now().Add(time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("create grant returned error: %v", err)
	}
	existing, err := client.GameAccountBinding.Query().
		Where(gameaccountbinding.IDEQ(binding.ID)).
		WithUser().
		Only(ctx)
	if err != nil {
		t.Fatalf("query binding returned error: %v", err)
	}
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{DB: client},
	}

	_, err = saveGameAccountBinding(ctx, helper, existing, "jp", "123456", "new-owner", harukiAPIHelper.CreateGameAccountBindingPayload{
		Suite:   &harukiSchema.SuiteDataPrivacySettings{AllowPublicApi: false},
		MySekai: &harukiSchema.MysekaiDataPrivacySettings{AllowPublicApi: false},
	})
	if !errors.Is(err, errGameAccountBindingOwnerBanned) {
		t.Fatalf("saveGameAccountBinding error = %v, want errGameAccountBindingOwnerBanned", err)
	}
	updated, err := client.GameAccountBinding.Query().
		Where(gameaccountbinding.IDEQ(binding.ID)).
		WithUser().
		Only(ctx)
	if err != nil {
		t.Fatalf("query updated binding returned error: %v", err)
	}
	if updated.Edges.User == nil || updated.Edges.User.ID != "old-owner" {
		t.Fatalf("updated owner = %+v, want old-owner", updated.Edges.User)
	}
	grantCount, err := client.GameAccountDataGrant.Query().
		Where(
			gameaccountdatagrant.OwnerUserIDEQ("old-owner"),
			gameaccountdatagrant.ServerEQ("jp"),
			gameaccountdatagrant.GameUserIDEQ("123456"),
		).
		Count(ctx)
	if err != nil {
		t.Fatalf("count grants returned error: %v", err)
	}
	if grantCount != 1 {
		t.Fatalf("old owner grants = %d, want 1", grantCount)
	}
}

func TestNotifyPreviousGameAccountBindingOwnerBestEffort(t *testing.T) {
	var sentTo string
	var sentBody, sentText string
	originalSend := sendGameAccountBindingTransferMail
	sendGameAccountBindingTransferMail = func(_ *harukiAPIHelper.HarukiToolboxRouterHelpers, email string, msg smtp.Message) error {
		sentTo = email
		sentBody = msg.HTML + "\n" + msg.Text
		sentText = msg.Text
		return errors.New("smtp down")
	}
	t.Cleanup(func() {
		sendGameAccountBindingTransferMail = originalSend
	})

	notifyPreviousGameAccountBindingOwner(
		&harukiAPIHelper.HarukiToolboxRouterHelpers{SMTPClient: &smtp.HarukiSMTPClient{}},
		&gameAccountBindingSaveResult{
			Transferred:         true,
			PreviousOwnerUserID: "old-owner",
			PreviousOwnerEmail:  "old-owner@example.com",
		},
		"jp",
		"123456",
		time.Date(2026, time.June, 14, 8, 0, 0, 0, time.UTC),
	)
	if sentTo != "old-owner@example.com" {
		t.Fatalf("sentTo = %q, want old-owner@example.com", sentTo)
	}
	if !strings.Contains(sentBody, "123456") || !strings.Contains(sentBody, "JP") {
		t.Fatalf("sent body missing server/game user id: %s", sentBody)
	}
	if !strings.Contains(sentText, "JP") || !strings.Contains(sentText, "123456") || !strings.Contains(sentText, "2026-06-14 16:00:00（北京时间）") {
		t.Fatalf("plain-text part incomplete: %q", sentText)
	}
	if !strings.Contains(sentBody, "2026-06-14 16:00:00（北京时间）") {
		t.Fatalf("sent body missing the transfer time in CST: %s", sentBody)
	}
	if strings.Contains(sentBody, "new-owner") || strings.Contains(sentBody, "new-owner@example.com") {
		t.Fatalf("sent body should not contain new owner identity: %s", sentBody)
	}
}

func newGameBindingRedisHelper(t *testing.T) (*harukiAPIHelper.HarukiToolboxRouterHelpers, *harukiRedis.HarukiRedisManager, context.Context) {
	t.Helper()

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
	})

	client := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
	})

	redisManager := &harukiRedis.HarukiRedisManager{Redis: client}
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{Redis: redisManager},
	}
	return helper, redisManager, context.Background()
}

func TestGameVerificationTargetBudgetUsesCanonicalAccount(t *testing.T) {
	helper, redis, ctx := newGameBindingRedisHelper(t)
	key := harukiRedis.BuildGameAccountVerificationTargetIssueKey("jp", "12345")
	for range gameAccountVerificationTargetLimit {
		if _, err := redis.IncrementWithTTL(ctx, key, gameAccountVerificationTTL); err != nil {
			t.Fatal(err)
		}
	}
	app := fiber.New()
	app.Post("/:server/:game_user_id", func(c fiber.Ctx) error { c.Locals("userID", "fixture-owner"); return c.Next() }, handleGenerateGameAccountVerificationCode(helper))
	for path, want := range map[string]int{"/jp/00012345": 429, "/en/12345": 200, "/jp/12346": 200} {
		resp, err := app.Test(httptest.NewRequest(http.MethodPost, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s returned %d, want %d", path, resp.StatusCode, want)
		}
	}
	code := newGameAccountVerificationCode()
	if len(code) != 20 || !profileContainsVerificationCode("verify "+code, code) || profileContainsVerificationCode(code, newGameAccountVerificationCode()) {
		t.Fatal("issued code did not match the profile challenge format")
	}
}

func TestGetVerificationCodeTooManyAttempts(t *testing.T) {
	t.Parallel()

	helper, redisManager, ctx := newGameBindingRedisHelper(t)
	userID, server, gameUserID := "u1", "jp", "12345"
	attemptKey := redisManager.KeyBuilder().BuildGameAccountVerifyAttemptKey(userID, server, gameUserID, "1/2/3/4/5/6")
	codeKey := harukiRedis.BuildGameAccountVerifyKey(userID, server, gameUserID)

	if err := redisManager.SetCache(ctx, attemptKey, gameAccountVerificationMaxAttempts, time.Minute); err != nil {
		t.Fatalf("seed attempt key error: %v", err)
	}
	if err := redisManager.SetCache(ctx, codeKey, "1/2/3/4/5/6", time.Minute); err != nil {
		t.Fatalf("seed code key error: %v", err)
	}

	_, err := getVerificationCode(ctx, helper, userID, server, gameUserID)
	if !errors.Is(err, errGameAccountVerificationTooManyAttempts) {
		t.Fatalf("error = %v, want %v", err, errGameAccountVerificationTooManyAttempts)
	}
	var attemptCount int
	if found, getErr := redisManager.GetCache(ctx, attemptKey, &attemptCount); getErr != nil || !found {
		t.Fatalf("read attempt count: found=%v error=%v", found, getErr)
	}
	if attemptCount != gameAccountVerificationMaxAttempts+1 {
		t.Fatalf("attempt count = %d, want %d", attemptCount, gameAccountVerificationMaxAttempts+1)
	}
}

func TestProfileCheckBudgetSurvivesChallengeRotation(t *testing.T) {
	helper, redis, ctx := newGameBindingRedisHelper(t)
	const actor, server, target = "actor", "jp", "123"
	codeKey := harukiRedis.BuildGameAccountVerifyKey(actor, server, target)
	for i := range gameAccountProfileChecksPerUser + 1 {
		code := strconv.Itoa(i + 1)
		if err := redis.SetCache(ctx, codeKey, code, gameAccountVerificationTTL); err != nil {
			t.Fatal(err)
		}
		got, err := getVerificationCode(ctx, helper, actor, server, target)
		if i == gameAccountProfileChecksPerUser {
			if !errors.Is(err, errGameAccountProfileChecksLimited) || mapGameAccountVerificationCodeLookupError(err).Code != http.StatusTooManyRequests {
				t.Fatalf("replacement challenge bypassed the profile budget: %v", err)
			}
		} else if err != nil || got != code {
			t.Fatalf("profile check %d unexpectedly failed: %v", i, err)
		}
	}
	key := harukiRedis.BuildGameAccountProfileCheckTargetKey(server, target)
	if err := redis.SetCache(ctx, key, gameAccountProfileChecksPerTarget, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, region := range []string{server, "en"} {
		if err := redis.SetCache(ctx, harukiRedis.BuildGameAccountVerifyKey("another", region, target), "fixture-challenge", time.Minute); err != nil {
			t.Fatal(err)
		}
		_, err := getVerificationCode(ctx, helper, "another", region, target)
		if (region == server && !errors.Is(err, errGameAccountProfileChecksLimited)) || (region != server && err != nil) {
			t.Fatalf("unexpected target budget for region %s: %v", region, err)
		}
	}
}

func TestReissuedGameVerificationCodeHasItsOwnAttemptBudget(t *testing.T) {
	helper, redis, ctx := newGameBindingRedisHelper(t)
	const userID, server, gameUserID = "u1", "jp", "12345"
	const original = "fixture-original"
	codeKey := harukiRedis.BuildGameAccountVerifyKey(userID, server, gameUserID)
	attemptKey := redis.KeyBuilder().BuildGameAccountVerifyAttemptKey(userID, server, gameUserID, original)
	if err := redis.SetCache(ctx, codeKey, original, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := redis.SetCache(ctx, attemptKey, gameAccountVerificationMaxAttempts, time.Minute); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Post("/:server/:game_user_id", func(c fiber.Ctx) error {
		c.Locals("userID", userID)
		return c.Next()
	}, handleGenerateGameAccountVerificationCode(helper))
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/jp/00012345", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reissuance status = %d", resp.StatusCode)
	}
	code, err := getVerificationCode(ctx, helper, userID, server, gameUserID)
	if err != nil || code == "" || code == original {
		t.Fatalf("replacement challenge was not usable: %v", err)
	}
	if err := consumeGameAccountVerificationCode(ctx, helper, userID, server, gameUserID, original); !errors.Is(err, errGameAccountVerificationCodeExpired) {
		t.Fatalf("superseded challenge could consume the replacement: %v", err)
	}
}

func TestPaddedGameAccountIDReusesCanonicalProfile(t *testing.T) {
	env := newProfileTestEnv(t, sekaiapi.Options{})
	for _, id := range []string{"000123", "123"} {
		status, _, _ := env.get(t, "owner", id)
		if status != fiber.StatusOK {
			t.Fatalf("profile for %s returned %d", id, status)
		}
	}
	if got := env.upstream.hits.Load(); got != 1 {
		t.Fatalf("profile cache missed for an equivalent ID: %d requests", got)
	}
}

func TestBindingTransferReservesNotificationBeforeConsumingCode(t *testing.T) {
	env := newProfileTestEnv(t, sekaiapi.Options{})
	env.helper.SMTPClient = &smtp.HarukiSMTPClient{}
	env.app.Post("/:server/:game_user_id", handleCreateGameAccountBinding(env.helper))
	redis := env.helper.DBManager.Redis
	const code = "hello"
	codeKey := harukiRedis.BuildGameAccountVerifyKey("grantee", "tw", "123")
	if err := redis.SetCache(t.Context(), codeKey, code, time.Minute); err != nil {
		t.Fatal(err)
	}
	var reservations []*mailnotify.Reservation
	for reservation := mailnotify.Reserve(); reservation != nil; reservation = mailnotify.Reserve() {
		reservations = append(reservations, reservation)
		defer reservation.Cancel()
	}
	tryTransfer := func(want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/tw/123", strings.NewReader(`{}`))
		req.Header.Set("X-Test-User", "grantee")
		req.Header.Set("Content-Type", "application/json")
		resp, err := env.app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("transfer status = %d, want %d", resp.StatusCode, want)
		}
	}
	tryTransfer(http.StatusServiceUnavailable)
	var retained string
	if found, err := redis.GetCache(t.Context(), codeKey, &retained); err != nil || !found || retained != code {
		t.Fatalf("unadmitted transfer consumed its challenge: found=%v err=%v", found, err)
	}
	binding, err := queryExistingBinding(t.Context(), env.helper, "tw", "123")
	if err != nil || bindingOwnerID(binding) != "owner" {
		t.Fatalf("unadmitted transfer changed the binding: %v", err)
	}
	for _, reservation := range reservations {
		reservation.Cancel()
	}
	mail := make(chan string, 1)
	originalSend := sendGameAccountBindingTransferMail
	sendGameAccountBindingTransferMail = func(_ *harukiAPIHelper.HarukiToolboxRouterHelpers, email string, _ smtp.Message) error {
		mail <- email
		return nil
	}
	t.Cleanup(func() { sendGameAccountBindingTransferMail = originalSend })
	tryTransfer(http.StatusOK)
	select {
	case recipient := <-mail:
		if recipient != "owner@example.com" {
			t.Fatalf("transfer mail recipient = %q", recipient)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("transferred binding did not queue mail")
	}
}

func TestGrantRouteUsesCanonicalGameAccountID(t *testing.T) {
	app := fiber.New()
	app.Put("/:server/:game_user_id/:data_type/:grantee_user_id", func(c fiber.Ctx) error {
		_, id, _, _, parseErr := parseGrantRouteParams(c)
		if parseErr != nil || id != "123" {
			t.Errorf("parsed grant account = %q, %v", id, parseErr)
		}
		return c.SendStatus(http.StatusNoContent)
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodPut, "/jp/000123/profile/test-user", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected route status: %d", resp.StatusCode)
	}
}

func TestGetVerificationCodeMissingDoesNotReserveAttempt(t *testing.T) {
	t.Parallel()

	helper, redisManager, ctx := newGameBindingRedisHelper(t)
	userID, server, gameUserID := "u1", "jp", "12345"
	attemptKey := redisManager.KeyBuilder().BuildGameAccountVerifyAttemptKey(userID, server, gameUserID, "1/2/3/4/5/6")

	_, err := getVerificationCode(ctx, helper, userID, server, gameUserID)
	if !errors.Is(err, errGameAccountVerificationCodeExpired) {
		t.Fatalf("error = %v, want %v", err, errGameAccountVerificationCodeExpired)
	}
	if exists, existsErr := redisManager.Redis.Exists(ctx, attemptKey).Result(); existsErr != nil {
		t.Fatalf("attempt key exists check error: %v", existsErr)
	} else if exists != 0 {
		t.Fatalf("missing verification code reserved an attempt, exists=%d", exists)
	}
}

func TestGetVerificationCodeAtomicallyReservesConcurrentAttempts(t *testing.T) {
	t.Parallel()

	helper, redisManager, ctx := newGameBindingRedisHelper(t)
	userID, server, gameUserID := "u1", "jp", "12345"
	codeKey := harukiRedis.BuildGameAccountVerifyKey(userID, server, gameUserID)
	attemptKey := redisManager.KeyBuilder().BuildGameAccountVerifyAttemptKey(userID, server, gameUserID, "1/2/3/4/5/6")
	if err := redisManager.SetCache(ctx, codeKey, "1/2/3/4/5/6", time.Minute); err != nil {
		t.Fatalf("seed code key error: %v", err)
	}

	const callers = 32
	start := make(chan struct{})
	results := make(chan error, callers)
	var callersDone sync.WaitGroup
	callersDone.Add(callers)
	for range callers {
		go func() {
			defer callersDone.Done()
			<-start
			_, err := getVerificationCode(ctx, helper, userID, server, gameUserID)
			results <- err
		}()
	}
	close(start)
	callersDone.Wait()
	close(results)

	accepted := 0
	rejected := 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, errGameAccountVerificationTooManyAttempts):
			rejected++
		default:
			t.Fatalf("unexpected reservation error: %v", err)
		}
	}
	if accepted != gameAccountVerificationMaxAttempts {
		t.Fatalf("accepted attempts = %d, want %d", accepted, gameAccountVerificationMaxAttempts)
	}
	if rejected != callers-gameAccountVerificationMaxAttempts {
		t.Fatalf("rejected attempts = %d, want %d", rejected, callers-gameAccountVerificationMaxAttempts)
	}

	var attemptCount int
	if found, err := redisManager.GetCache(ctx, attemptKey, &attemptCount); err != nil || !found {
		t.Fatalf("read final attempt count: found=%v error=%v", found, err)
	}
	if attemptCount != callers {
		t.Fatalf("final attempt count = %d, want %d", attemptCount, callers)
	}
}

func TestConsumeGameAccountVerificationCode(t *testing.T) {
	t.Parallel()

	helper, redisManager, ctx := newGameBindingRedisHelper(t)
	userID, server, gameUserID := "u1", "jp", "12345"
	code := "1/2/3/4/5/6"
	attemptKey := redisManager.KeyBuilder().BuildGameAccountVerifyAttemptKey(userID, server, gameUserID, "1/2/3/4/5/6")
	codeKey := harukiRedis.BuildGameAccountVerifyKey(userID, server, gameUserID)

	if err := redisManager.SetCache(ctx, attemptKey, 2, time.Minute); err != nil {
		t.Fatalf("seed attempt key error: %v", err)
	}
	if err := redisManager.SetCache(ctx, codeKey, code, time.Minute); err != nil {
		t.Fatalf("seed code key error: %v", err)
	}

	if err := consumeGameAccountVerificationCode(ctx, helper, userID, server, gameUserID, code); err != nil {
		t.Fatalf("consumeGameAccountVerificationCode error: %v", err)
	}

	exists, err := redisManager.Redis.Exists(ctx, codeKey, attemptKey).Result()
	if err != nil {
		t.Fatalf("exists check error: %v", err)
	}
	if exists != 0 {
		t.Fatalf("expected verification and attempt keys to be removed, exists=%d", exists)
	}
}

func TestMapGameAccountVerificationCodeLookupError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		inputErr   error
		wantCode   int
		wantDetail string
	}{
		{
			name:       "too many attempts",
			inputErr:   errGameAccountVerificationTooManyAttempts,
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "too many verification attempts, please generate a new code",
		},
		{
			name:       "code expired",
			inputErr:   errGameAccountVerificationCodeExpired,
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "verification code expired or not found",
		},
		{
			name:       "storage unavailable",
			inputErr:   errGameAccountVerificationServiceUnstable,
			wantCode:   fiber.StatusInternalServerError,
			wantDetail: "verification service unavailable",
		},
		{
			name:       "unknown lookup error",
			inputErr:   errors.New("boom"),
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "verification code not found",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mapGameAccountVerificationCodeLookupError(tc.inputErr)
			if got == nil {
				t.Fatalf("mapGameAccountVerificationCodeLookupError(%v) returned nil", tc.inputErr)
			}
			if got.Code != tc.wantCode {
				t.Fatalf("status code = %d, want %d", got.Code, tc.wantCode)
			}
			if got.Message != tc.wantDetail {
				t.Fatalf("message = %q, want %q", got.Message, tc.wantDetail)
			}
		})
	}
}

func TestMapGameAccountOwnershipVerificationError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		inputErr   error
		wantCode   int
		wantDetail string
	}{
		{
			name:       "missing code in profile",
			inputErr:   errGameAccountVerificationCodeMissing,
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "verification code missing in game profile",
		},
		{
			name:       "code mismatch",
			inputErr:   errGameAccountVerificationCodeMismatch,
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "verification code does not match game profile",
		},
		{
			name:       "account not found",
			inputErr:   errGameAccountNotFound,
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "game account not found",
		},
		{
			name:       "server unavailable",
			inputErr:   errGameAccountServerUnavailable,
			wantCode:   fiber.StatusBadGateway,
			wantDetail: "game server unavailable",
		},
		{
			name:       "upstream request failed",
			inputErr:   errors.Join(errGameAccountProfileRequestFailed, errors.New("timeout")),
			wantCode:   fiber.StatusBadGateway,
			wantDetail: "failed to query game account profile",
		},
		{
			name:       "empty profile",
			inputErr:   errGameAccountProfileEmpty,
			wantCode:   fiber.StatusBadGateway,
			wantDetail: "empty game account profile response",
		},
		{
			name:       "invalid profile",
			inputErr:   errors.Join(errGameAccountProfileInvalid, errors.New("bad json")),
			wantCode:   fiber.StatusBadGateway,
			wantDetail: "invalid game account profile response",
		},
		{
			name:       "unexpected error",
			inputErr:   errors.New("panic"),
			wantCode:   fiber.StatusInternalServerError,
			wantDetail: "failed to verify game account ownership",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mapGameAccountOwnershipVerificationError(tc.inputErr)
			if got == nil {
				t.Fatalf("mapGameAccountOwnershipVerificationError(%v) returned nil", tc.inputErr)
			}
			if got.Code != tc.wantCode {
				t.Fatalf("status code = %d, want %d", got.Code, tc.wantCode)
			}
			if got.Message != tc.wantDetail {
				t.Fatalf("message = %q, want %q", got.Message, tc.wantDetail)
			}
		})
	}
}

func TestGameVerificationRejectsInvalidIDsAndFailsClosedOnQuotaErrors(t *testing.T) {
	helper, redis, _ := newGameBindingRedisHelper(t)
	app := fiber.New()
	app.Post("/:actor/:server/:game_user_id", func(c fiber.Ctx) error {
		c.Locals("userID", c.Params("actor"))
		return c.Next()
	}, handleGenerateGameAccountVerificationCode(helper))
	check := func(path string, want int) {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(http.MethodPost, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("issuance for %s returned %d, want %d", path, resp.StatusCode, want)
		}
	}
	ctx := t.Context()
	check("/owner/jp/not-a-number", http.StatusBadRequest)
	user := harukiRedis.BuildGameAccountVerificationIssueKey("owner")
	if err := redis.Redis.Set(ctx, user, 10, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	check("/owner/jp/43", http.StatusTooManyRequests)
	for _, key := range []string{harukiRedis.BuildGameAccountVerificationIssueKey("faulty"), harukiRedis.BuildGameAccountVerificationTargetIssueKey("jp", "44")} {
		if err := redis.Redis.Set(ctx, key, "fixture-invalid-counter", time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	check("/faulty/jp/45", http.StatusInternalServerError)
	check("/other/jp/44", http.StatusInternalServerError)
}

func TestBindingCreationValidatesInputAndReusesCanonicalVerifiedAccount(t *testing.T) {
	helper, db, ctx := newDefaultBindingTestHelper(t, t.Name())
	createDefaultBindingTestUser(t, db, ctx, "owner")
	db.GameAccountBinding.Create().SetUserID("owner").SetServer("jp").SetGameUserID("42").SetVerified(true).ExecX(ctx)
	app := fiber.New()
	app.Post("/:server/:game_user_id", func(c fiber.Ctx) error {
		c.Locals("userID", "owner")
		return c.Next()
	}, handleCreateGameAccountBinding(helper))
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/jp/invalid", "{}", http.StatusBadRequest},
		{"/unknown/42", "{}", http.StatusBadRequest},
		{"/jp/42", "{", http.StatusBadRequest},
		{"/jp/00042", "{}", http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("binding at %s returned %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
	}
	if count := db.GameAccountBinding.Query().CountX(ctx); count != 1 {
		t.Fatalf("canonical lookup produced %d bindings", count)
	}
}
