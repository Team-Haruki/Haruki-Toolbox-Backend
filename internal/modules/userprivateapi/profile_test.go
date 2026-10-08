package userprivateapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api/data"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiDatabase "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
	goredis "github.com/redis/go-redis/v9"
)

// Synthetic ids only.
const (
	ownedGameUserID      = int64(1001)
	bannedGameUserID     = int64(1002)
	unverifiedGameUserID = int64(1003)
	unboundGameUserID    = int64(1009)
)

var privateTestDBSeq atomic.Int64

type privateTestEnv struct {
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers
	redis  *miniredis.Miniredis
	app    *fiber.App
}

// newPrivateTestEnv builds the private data handler over an in-memory Toolbox
// database and Redis. The game-data store is whatever the caller passes (nil
// in the unit tests), so every 200 below is a cache hit.
func newPrivateTestEnv(t *testing.T, gameData *gamedata.Service) *privateTestEnv {
	t.Helper()
	ctx := context.Background()
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared&_fk=1", strings.ReplaceAll(t.Name(), "/", "_"), privateTestDBSeq.Add(1))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	mustUser := func(id string, banned bool) {
		if _, err := client.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").SetBanned(banned).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mustUser("owner", false)
	mustUser("other", false)
	mustUser("banned", true)
	if _, err := client.SocialPlatformInfo.Create().SetPlatform("qq").SetPlatformUserID("100").SetVerified(true).SetUserSocialPlatformInfo("owner").Save(ctx); err != nil {
		t.Fatal(err)
	}
	mustBinding := func(owner string, gameUserID int64, verified bool) {
		if _, err := client.GameAccountBinding.Create().SetServer("jp").SetGameUserID(strconv.FormatInt(gameUserID, 10)).SetVerified(verified).SetUserID(owner).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mustBinding("owner", ownedGameUserID, true)
	mustBinding("banned", bannedGameUserID, true)
	mustBinding("owner", unverifiedGameUserID, false)
	// The owner authorizes qq/200. Another user's grant to qq/300 must never
	// open the owner's data.
	mustGrant := func(owner, platformUserID string) {
		if _, err := client.AuthorizeSocialPlatformInfo.Create().SetUserID(owner).SetPlatform("qq").SetPlatformUserID(platformUserID).SetPlatformID(1).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mustGrant("owner", "200")
	mustGrant("other", "300")

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	rc := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rc.Close() })

	dbm := &harukiDatabase.HarukiToolboxDBManager{DB: client, Redis: &harukiRedis.HarukiRedisManager{Redis: rc}, GameData: gameData}
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: dbm}
	app := fiber.New()
	app.Get("/api/private/game-data/:server/:data_type/:user_id", handleGetPrivateData(helper))
	return &privateTestEnv{helper: helper, redis: srv, app: app}
}

func (e *privateTestEnv) cacheKey(profile *privateProfile, gameUserID, stamp int64) string {
	return harukiRedis.BuildVersionedGameDataCacheKey(profile.cacheSurface(""), "jp", "suite", gameUserID, "", stamp,
		e.helper.DBManager.GameData.HarvestSchemaFingerprint("jp"))
}

// seed stores a zstd body under a cache key, through the indexed write the
// private loader uses, and the stamp memo the handler resolves the generation
// from.
func (e *privateTestEnv) seed(t *testing.T, profile *privateProfile, gameUserID, stamp int64, body string) string {
	t.Helper()
	if err := e.redis.Set(harukiRedis.BuildGameDataStampMemoKey("jp", "suite", gameUserID), strconv.FormatInt(stamp, 10)); err != nil {
		t.Fatal(err)
	}
	stored, err := data.CompressGameDataBodyZstd([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	key := e.cacheKey(profile, gameUserID, stamp)
	if err := e.helper.DBManager.Redis.SetGameDataBodyCache(context.Background(), "jp", "suite", gameUserID, key, string(stored), harukiRedis.GameDataCacheTTL); err != nil {
		t.Fatal(err)
	}
	return key
}

func (e *privateTestEnv) get(t *testing.T, path string, header map[string]string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

func privatePath(gameUserID int64, platformUserID, extra string) string {
	p := fmt.Sprintf("/api/private/game-data/jp/suite/%d?platform=qq&platform_user_id=%s", gameUserID, platformUserID)
	if extra != "" {
		p += "&" + extra
	}
	return p
}

var cloudProfile = privateProfiles["cloud"]

const testStamp = int64(1700000000)

func TestResolvePrivateProfile(t *testing.T) {
	cases := []struct {
		name, key string
		dataType  harukiUtils.UploadDataType
		wantErr   string
	}{
		{"", "", harukiUtils.UploadDataTypeSuite, ""},
		{"cloud", "", harukiUtils.UploadDataTypeSuite, ""},
		{"nope", "", harukiUtils.UploadDataTypeSuite, "invalid profile"},
		{"cloud", "", harukiUtils.UploadDataTypeMysekai, "profile is only supported for suite"},
		{"cloud", "userCards", harukiUtils.UploadDataTypeSuite, "profile cannot be combined with key"},
	}
	for _, tc := range cases {
		p, errMsg := resolvePrivateProfile(tc.name, tc.key, tc.dataType)
		if errMsg != tc.wantErr {
			t.Fatalf("%q/%q/%s: err = %q, want %q", tc.name, tc.key, tc.dataType, errMsg, tc.wantErr)
		}
		if tc.wantErr == "" && tc.name != "" && p != cloudProfile {
			t.Fatalf("%q did not resolve to the cloud profile", tc.name)
		}
	}
}

// The profile surface must stay one key segment (no ':'), differ from the full
// and keyed bodies' surfaces, and move when the deny list changes.
func TestPrivateProfileCacheSurface(t *testing.T) {
	var none *privateProfile
	if none.cacheSurface("") != privateCacheSurfaceFull || none.flightSuffix() != "" {
		t.Fatal("no profile must keep the plain private surface")
	}
	if none.cacheSurface("userGamedata") != privateCacheSurfaceKeyed {
		t.Fatal("a keyed request without a profile must keep the keyed surface")
	}
	s := cloudProfile.cacheSurface("")
	if !strings.HasPrefix(s, "private-profile-cloud-") || strings.Contains(s, ":") {
		t.Fatalf("surface %q", s)
	}
	if newPrivateProfile("cloud", "userCostume3dStatuses").cacheSurface("") == s {
		t.Fatal("changing the deny list did not change the cache surface")
	}
	if cloudProfile.flightSuffix() == "" {
		t.Fatal("profile shares the full body's singleflight key")
	}
	want := []string{"userCostume3dStatuses", "userCostume3dShopItems"}
	if strings.Join(cloudProfile.omit, ",") != strings.Join(want, ",") {
		t.Fatalf("cloud deny list = %v, want %v", cloudProfile.omit, want)
	}
}

// Every authorization outcome of the full path is reproduced, unchanged, with
// ?profile=cloud. The profile is resolved before the lookups and changes only
// the cache surface and render, so a request the full path refuses is refused.
func TestPrivateProfileAuthorizationParity(t *testing.T) {
	env := newPrivateTestEnv(t, nil)
	env.seed(t, nil, ownedGameUserID, testStamp, `{"full":true}`)
	env.seed(t, cloudProfile, ownedGameUserID, testStamp, `{"profile":true}`)

	cases := []struct {
		name           string
		gameUserID     int64
		platformUserID string
		wantStatus     int
	}{
		{"owner's own social account", ownedGameUserID, "100", fiber.StatusOK},
		{"account the owner authorized", ownedGameUserID, "200", fiber.StatusOK},
		{"another user's grant", ownedGameUserID, "300", fiber.StatusForbidden},
		{"no grant at all", ownedGameUserID, "400", fiber.StatusForbidden},
		{"unbound game account", unboundGameUserID, "100", fiber.StatusNotFound},
		{"unverified binding", unverifiedGameUserID, "100", fiber.StatusNotFound},
		{"banned owner", bannedGameUserID, "100", fiber.StatusForbidden},
	}
	for _, tc := range cases {
		full, fullBody := env.get(t, privatePath(tc.gameUserID, tc.platformUserID, ""), nil)
		prof, profBody := env.get(t, privatePath(tc.gameUserID, tc.platformUserID, "profile=cloud"), nil)
		if full.StatusCode != tc.wantStatus || prof.StatusCode != tc.wantStatus {
			t.Fatalf("%s: full %d, profile %d, want %d", tc.name, full.StatusCode, prof.StatusCode, tc.wantStatus)
		}
		if tc.wantStatus == fiber.StatusOK {
			if fullBody != `{"full":true}` || profBody != `{"profile":true}` {
				t.Fatalf("%s: full %s, profile %s", tc.name, fullBody, profBody)
			}
		}
	}
	// Missing query parameters are refused the same way too.
	if resp, _ := env.get(t, "/api/private/game-data/jp/suite/1001?profile=cloud", nil); resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("missing platform: status %d", resp.StatusCode)
	}
}

func TestPrivateProfileRejectsInvalidRequests(t *testing.T) {
	env := newPrivateTestEnv(t, nil)
	for _, extra := range []string{"profile=nope", "profile=cloud&key=userCards", "profile=cloud&key=upload_time"} {
		if resp, _ := env.get(t, privatePath(ownedGameUserID, "100", extra), nil); resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", extra, resp.StatusCode)
		}
	}
	resp, _ := env.get(t, "/api/private/game-data/jp/mysekai/1001?platform=qq&platform_user_id=100&profile=cloud", nil)
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("mysekai profile: status %d, want 400", resp.StatusCode)
	}
}

// known_upload_time answers 304 for a profile exactly as for the full body.
func TestPrivateProfileNotModified(t *testing.T) {
	env := newPrivateTestEnv(t, nil)
	env.seed(t, cloudProfile, ownedGameUserID, testStamp, `{"profile":true}`)

	resp, body := env.get(t, privatePath(ownedGameUserID, "100", "profile=cloud&known_upload_time="+strconv.FormatInt(testStamp, 10)), nil)
	if resp.StatusCode != fiber.StatusNotModified || body != "" {
		t.Fatalf("status %d body %q, want 304 with no body", resp.StatusCode, body)
	}
	if got := resp.Header.Get(data.HeaderUploadTime); got != strconv.FormatInt(testStamp, 10) {
		t.Fatalf("X-Upload-Time = %q", got)
	}
	if !strings.Contains(resp.Header.Get(fiber.HeaderVary), fiber.HeaderAcceptEncoding) {
		t.Fatal("304 lost Vary: Accept-Encoding")
	}
	resp, body = env.get(t, privatePath(ownedGameUserID, "100", "profile=cloud&known_upload_time="+strconv.FormatInt(testStamp-1, 10)), nil)
	if resp.StatusCode != fiber.StatusOK || body != `{"profile":true}` {
		t.Fatalf("stale known_upload_time: status %d body %s", resp.StatusCode, body)
	}
}

// A stored zstd body passes straight through to a zstd client and is decoded
// for one that does not accept it, exactly like the full body.
func TestPrivateProfileZstdPassthrough(t *testing.T) {
	env := newPrivateTestEnv(t, nil)
	key := env.seed(t, cloudProfile, ownedGameUserID, testStamp, `{"profile":true}`)
	stored, err := env.redis.Get(key)
	if err != nil {
		t.Fatal(err)
	}

	resp, body := env.get(t, privatePath(ownedGameUserID, "100", "profile=cloud"), map[string]string{"Accept-Encoding": "zstd"})
	if resp.StatusCode != fiber.StatusOK || resp.Header.Get(fiber.HeaderContentEncoding) != "zstd" || body != stored {
		t.Fatalf("zstd client: status %d encoding %q", resp.StatusCode, resp.Header.Get(fiber.HeaderContentEncoding))
	}
	resp, body = env.get(t, privatePath(ownedGameUserID, "100", "profile=cloud"), nil)
	if resp.StatusCode != fiber.StatusOK || resp.Header.Get(fiber.HeaderContentEncoding) != "" || body != `{"profile":true}` {
		t.Fatalf("identity client: status %d encoding %q body %s", resp.StatusCode, resp.Header.Get(fiber.HeaderContentEncoding), body)
	}
}

// Profile bodies live under their own key: a full body is never served for a
// profile request, and a profile body never for a full one.
func TestPrivateProfileCacheIsSeparateFromFullBody(t *testing.T) {
	env := newPrivateTestEnv(t, nil)
	env.seed(t, nil, ownedGameUserID, testStamp, `{"full":true}`)
	resp, body := env.get(t, privatePath(ownedGameUserID, "100", "profile=cloud"), nil)
	if strings.Contains(body, "full") {
		t.Fatalf("profile request served the full body (status %d)", resp.StatusCode)
	}

	env = newPrivateTestEnv(t, nil)
	env.seed(t, cloudProfile, ownedGameUserID, testStamp, `{"profile":true}`)
	_, body = env.get(t, privatePath(ownedGameUserID, "100", ""), nil)
	if strings.Contains(body, "profile") {
		t.Fatal("full request served the profile body")
	}
}

// The upload path's per-user clear removes profile bodies together with full
// bodies, and a newer upload_time moves readers to a new key regardless.
func TestPrivateProfileCacheClearedOnUpload(t *testing.T) {
	env := newPrivateTestEnv(t, nil)
	fullKey := env.seed(t, nil, ownedGameUserID, testStamp, `{"full":true}`)
	profileKey := env.seed(t, cloudProfile, ownedGameUserID, testStamp, `{"profile":true}`)
	otherUserKey := env.seed(t, cloudProfile, bannedGameUserID, testStamp, `{"profile":true}`)

	if err := env.helper.DBManager.Redis.ClearUploadedGameDataCaches(context.Background(), "suite", "jp", ownedGameUserID); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{fullKey, profileKey} {
		if env.redis.Exists(k) {
			t.Fatalf("%s survived the upload clear", k)
		}
	}
	if !env.redis.Exists(otherUserKey) {
		t.Fatal("the clear removed another account's profile body")
	}
	if env.cacheKey(cloudProfile, ownedGameUserID, testStamp+1) == profileKey {
		t.Fatal("a new upload_time does not move the profile key")
	}
}
