package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/gofiber/fiber/v3"
)

type profileQueryCounts struct {
	sync.Mutex
	selects, updates int
}

func (q *profileQueryCounts) log(args ...any) {
	text := fmt.Sprint(args...)
	if !strings.Contains(text, "`users`") {
		return
	}
	q.Lock()
	defer q.Unlock()
	if strings.Contains(text, "query=SELECT ") {
		q.selects++
	}
	if strings.Contains(text, "query=UPDATE ") {
		q.updates++
	}
}
func (q *profileQueryCounts) reset() { q.Lock(); defer q.Unlock(); q.selects = 0; q.updates = 0 }
func (q *profileQueryCounts) assert(t *testing.T, selects, updates int) {
	t.Helper()
	q.Lock()
	defer q.Unlock()
	if q.selects != selects || q.updates != updates {
		t.Fatalf("user queries: SELECT=%d UPDATE=%d; want %d/%d", q.selects, q.updates, selects, updates)
	}
}
func profileCountingDB(t *testing.T) (*postgresql.Client, *profileQueryCounts) {
	t.Helper()
	q := &profileQueryCounts{}
	db := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()), enttest.WithOptions(postgresql.Debug(), postgresql.Log(q.log)))
	t.Cleanup(func() { _ = db.Close() })
	return db, q
}

func TestAuthProxyReusesProfileWithinEachRequest(t *testing.T) {
	db, q := profileCountingDB(t)
	ctx := context.Background()
	db.User.Create().SetID("profile-user").SetName("initial").SetEmail("profile@example.test").SetKratosIdentityID("profile-identity").SaveX(ctx)
	handler := NewSessionHandler(nil, "")
	handler.DBClient = db
	handler.ConfigureAuthProxy(true, "X-Auth-Proxy-Secret", "test-only-proxy-shared-secret", "X-Kratos-Identity-Id", "X-User-Name", "X-User-Email", "X-User-Email-Verified", "X-User-Id")
	handler.ConfigureAuthProxySessionHeader("X-Auth-Proxy-Session-Id")
	app := fiber.New()
	app.Get("/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		if c.Locals("userID") != "profile-user" || c.Locals("identityID") != "profile-identity" || c.Locals("authProxySessionID") != "request-session" {
			return c.SendStatus(500)
		}
		return c.SendStatus(204)
	})
	request := func(secret, claimed, name string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/profile", nil)
		req.Header.Set("X-Auth-Proxy-Secret", secret)
		req.Header.Set("X-Kratos-Identity-Id", "profile-identity")
		req.Header.Set("X-User-Id", claimed)
		req.Header.Set("X-User-Name", name)
		req.Header.Set("X-User-Email", "profile@example.test")
		req.Header.Set("X-User-Email-Verified", "true")
		req.Header.Set("X-Auth-Proxy-Session-Id", "request-session")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	q.reset()
	if status := request("test-only-proxy-shared-secret", "profile-user", "initial"); status != 204 {
		t.Fatal(status)
	}
	q.assert(t, 1, 0)
	q.reset()
	if status := request("test-only-proxy-shared-secret", "profile-user", "updated"); status != 204 {
		t.Fatal(status)
	}
	q.assert(t, 1, 1)
	if db.User.GetX(ctx, "profile-user").Name != "updated" {
		t.Fatal("profile was not updated")
	}
	// A following request must see an intervening database change, not a cache.
	db.User.UpdateOneID("profile-user").SetName("external-change").SaveX(ctx)
	q.reset()
	if status := request("test-only-proxy-shared-secret", "profile-user", "updated"); status != 204 {
		t.Fatal(status)
	}
	q.assert(t, 1, 1)
	q.reset()
	if status := request("test-only-proxy-shared-secret", "different-user", "forged"); status != 401 {
		t.Fatal(status)
	}
	q.assert(t, 1, 0)
	q.reset()
	if status := request("wrong-secret", "profile-user", "forged"); status != 401 {
		t.Fatal(status)
	}
	q.assert(t, 0, 0)
	if db.User.GetX(ctx, "profile-user").Name != "updated" {
		t.Fatal("rejected request changed profile")
	}
}

func TestResolvedProfileFallbackBranches(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		seed, verified, link, provision bool
		bound                           string
		success                         bool
	}{
		{"unverified-existing", true, false, true, true, "", false},
		{"unverified-new", false, false, true, true, "", false},
		{"link-disabled", true, true, false, true, "", false},
		{"provision-disabled", false, true, true, false, "", false},
		{"different-identity", true, true, true, true, "other-identity", false},
		{"link", true, true, true, false, "", true},
		{"provision", false, true, true, true, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newSessionHandlerTestDB(t)
			ctx := context.Background()
			if tc.seed {
				builder := db.User.Create().SetID("fallback-user").SetName("old").SetEmail("fallback@example.test")
				if tc.bound != "" {
					builder.SetKratosIdentityID(tc.bound)
				}
				builder.SaveX(ctx)
			}
			handler := NewSessionHandler(nil, "")
			handler.DBClient = db
			handler.KratosAutoLinkByEmail = tc.link
			handler.KratosAutoProvisionUser = tc.provision
			id, profile, err := handler.resolveKratosIdentityWithProfile(ctx, "fallback-identity", "fallback@example.test", tc.verified)
			if !tc.success {
				if !errors.Is(err, errKratosIdentityUnmapped) || id != "" || profile != nil {
					t.Fatalf("rejected branch returned identity: %v", err)
				}
				if tc.seed {
					user := db.User.GetX(ctx, "fallback-user")
					bound := ""
					if user.KratosIdentityID != nil {
						bound = *user.KratosIdentityID
					}
					if bound != tc.bound {
						t.Fatal("rejected branch linked user")
					}
				} else if db.User.Query().CountX(ctx) != 0 {
					t.Fatal("unverified/disabled provisioning created user")
				}
				return
			}
			if err != nil || id == "" || profile != nil {
				t.Fatalf("fallback must retain fresh profile read: id=%q profile=%v err=%v", id, profile != nil, err)
			}
			name := "display-name"
			handler.syncResolvedUserProfile(ctx, id, "fallback-identity", "fallback@example.test", &name, profile)
			user := db.User.GetX(ctx, id)
			if user.Name != name || user.KratosIdentityID == nil || *user.KratosIdentityID != "fallback-identity" {
				t.Fatal("fallback profile mismatch")
			}
			// Once linked, even an unverified email does not unlink an established identity.
			nextID, snapshot, err := handler.resolveKratosIdentityWithProfile(ctx, "fallback-identity", "", false)
			if err != nil || nextID != id || snapshot == nil || snapshot.Name != name {
				t.Fatal("linked identity did not return profile")
			}
		})
	}
}

func TestProfileSnapshotCannotCrossUsers(t *testing.T) {
	db, q := profileCountingDB(t)
	ctx := context.Background()
	db.User.Create().SetID("owner").SetName("before").SetEmail("owner@example.test").SaveX(ctx)
	other := db.User.Create().SetID("other").SetName("desired").SetEmail("other@example.test").SaveX(ctx)
	h := NewSessionHandler(nil, "")
	h.DBClient = db
	name := "desired"
	q.reset()
	h.syncResolvedUserProfile(ctx, "owner", "", "owner@example.test", &name, other)
	q.assert(t, 1, 1)
	if db.User.GetX(ctx, "owner").Name != name || db.User.GetX(ctx, "other").Email != "other@example.test" {
		t.Fatal("profile snapshot crossed user scope")
	}
}

func TestKratosWhoamiReusesProfileWithoutCachingSession(t *testing.T) {
	db, q := profileCountingDB(t)
	ctx := context.Background()
	db.User.Create().SetID("kratos-user").SetName("display").SetEmail("kratos@example.test").SetKratosIdentityID("kratos-identity").SaveX(ctx)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/sessions/whoami" || r.Header.Get("X-Session-Token") != "test-session-token" {
			http.Error(w, "invalid", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active":true,"identity":{"id":"kratos-identity","traits":{"name":"display","email":"kratos@example.test"}}}`))
	}))
	defer server.Close()
	h := NewSessionHandler(nil, "")
	h.ConfigureIdentityProvider("kratos", server.URL, "", "X-Session-Token", "ory_kratos_session", true, true, time.Second, db)
	for range 2 {
		q.reset()
		resolved, err := h.resolveKratosSession(ctx, "test-session-token", "")
		if err != nil || resolved.UserID != "kratos-user" || resolved.IdentityID != "kratos-identity" {
			t.Fatalf("session resolution failed: %v", err)
		}
		q.assert(t, 1, 0)
	}
	if calls.Load() != 2 {
		t.Fatal("whoami was cached across requests")
	}
}
