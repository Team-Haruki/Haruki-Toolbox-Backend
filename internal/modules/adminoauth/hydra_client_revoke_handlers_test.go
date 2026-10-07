package adminoauth

import (
	"bytes"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	_ "github.com/mattn/go-sqlite3"
)

// The Hydra fake answers client=X&all=true (and client alone) with 400, as Hydra
// v25.4.0 does (TestFakeHydraConsentRevokeMatchesHydra), so these tests fail if a
// handler still revokes a whole client in one call.

func TestDisableClientRevokesPerSubject(t *testing.T) {
	hydra := newFakeHydraClients(t, confidentialBotClient)
	helper := newAdminOAuthTestHelper(t)
	seedBotGrantHolders(t, helper, hydra)
	hydra.failRevoke("u-3")
	app := newHydraClientHandlerTestAppAs(helper, hydra.config, "super-admin-1", adminCoreModule.RoleSuperAdmin)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/bot-1/active", `{"active":false}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 although one subject failed, body = %#v", status, envelope)
	}
	if envelope.Message != "oauth client disabled, but some grants could not be revoked" {
		t.Fatalf("message = %q", envelope.Message)
	}
	var resp adminOAuthClientActiveResponse
	decodeUpdatedData(t, envelope, &resp)
	if resp.Active || resp.RevokedSubjects != 4 || !reflect.DeepEqual(resp.FailedSubjects, []string{"u-3"}) || resp.RevocationComplete {
		t.Fatalf("response = %#v, want inactive, 4 revoked, [u-3] failed, incomplete", resp)
	}

	requests := hydra.takeRequests()
	// One subject+client call per subject, both subjects of each holder included.
	if got, want := consentRevocations(t, requests), []string{"kratos-1 bot-1", "kratos-2 bot-1", "u-1 bot-1", "u-2 bot-1", "u-3 bot-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("consent revocations = %v, want %v", got, want)
	}
	// The access-token delete is only a supplement and comes last.
	if last := requests[len(requests)-1]; last.Method != http.MethodDelete || last.Path != "/admin/oauth2/tokens" || last.Query.Get("client_id") != "bot-1" {
		t.Fatalf("last request = %s %s?%s, want the access-token delete", last.Method, last.Path, last.Query.Encode())
	}
	assertConsentClients(t, hydra, map[string][]string{"kratos-1": {}, "u-2": {}, "u-3": {"bot-1"}, "kratos-4": {"other-1"}})
	if active := hydra.client("bot-1")["metadata"].(map[string]any)["haruki"].(map[string]any)["active"]; active != false {
		t.Fatalf("haruki.active = %#v, want false", active)
	}
	assertLatestAuditMetadata(t, helper, adminAuditActionOAuthClientActiveUpdate, map[string]any{
		"active": false, "revokedSubjects": float64(4), "failedSubjects": float64(1), "revocationComplete": false,
	})

	// Enabling revokes nothing.
	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/bot-1/active", `{"active":true}`)
	if status != http.StatusOK || envelope.Message != "oauth client status updated" {
		t.Fatalf("enable status = %d, body = %#v", status, envelope)
	}
	if got := requestLines(hydra.takeRequests()); !reflect.DeepEqual(got, []string{"GET /admin/clients/bot-1", "PATCH /admin/clients/bot-1"}) {
		t.Fatalf("enable requests = %v", got)
	}
	for _, member := range []string{`"revokedSubjects":0`, `"failedSubjects":[]`, `"revocationComplete":true`} {
		if !bytes.Contains(envelope.UpdatedData, []byte(member)) {
			t.Fatalf("enable updatedData %s lacks %s", envelope.UpdatedData, member)
		}
	}
}

func TestDisableClientNeverFailsAfterMetadataPatch(t *testing.T) {
	for _, tc := range []struct {
		name            string
		breakHydra      func(*fakeHydraClients)
		wantRevocations []string
		wantAuditFlag   string
	}{
		{
			name:          "grants cannot be listed",
			breakHydra:    func(f *fakeHydraClients) { f.failConsentList = true },
			wantAuditFlag: "queryAuthorizationsFailed",
		},
		{
			name:            "access tokens cannot be deleted",
			breakHydra:      func(f *fakeHydraClients) { f.failTokenDelete = true },
			wantRevocations: []string{"kratos-1 bot-1", "u-1 bot-1"},
			wantAuditFlag:   "revokeTokensFailed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hydra := newFakeHydraClients(t, confidentialBotClient)
			helper := newAdminOAuthTestHelper(t)
			seedAdminOAuthTestUser(t, helper, "u-1", "kratos-1", userSchema.RoleUser)
			hydra.addConsent("kratos-1", "bot-1", "consent-1")
			hydra.mu.Lock()
			tc.breakHydra(hydra)
			hydra.mu.Unlock()
			app := newHydraClientHandlerTestAppAs(helper, hydra.config, "super-admin-1", adminCoreModule.RoleSuperAdmin)

			status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/bot-1/active", `{"active":false}`)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200 once the client is disabled, body = %#v", status, envelope)
			}
			var resp adminOAuthClientActiveResponse
			decodeUpdatedData(t, envelope, &resp)
			if resp.Active || resp.RevocationComplete || resp.FailedSubjects == nil {
				t.Fatalf("response = %#v, want inactive and incomplete with a non-nil failedSubjects", resp)
			}
			requests := hydra.takeRequests()
			if got := consentRevocations(t, requests); !slices.Equal(got, tc.wantRevocations) {
				t.Fatalf("consent revocations = %v, want %v", got, tc.wantRevocations)
			}
			if !slices.ContainsFunc(requests, func(r fakeHydraRequest) bool { return r.Path == "/admin/oauth2/tokens" }) {
				t.Fatalf("requests = %v, want the access-token delete attempted", requestLines(requests))
			}
			assertLatestAuditMetadata(t, helper, adminAuditActionOAuthClientActiveUpdate, map[string]any{"revocationComplete": false, tc.wantAuditFlag: true})
		})
	}
}

// Only super admins reach the disable route (RequireSuperAdmin), so this cannot
// happen in production today. The test mounts the handler without the route guard
// to pin the handler's own filter, which keeps the role-hierarchy rule for admin
// reads (EnsureAdminCanManageTargetUser) should the guard ever be relaxed.
func TestDisableClientHidesSuperAdminSubjectsFromPlainAdmin(t *testing.T) {
	hydra := newFakeHydraClients(t, confidentialBotClient)
	helper := newAdminOAuthTestHelper(t)
	seedAdminOAuthTestUser(t, helper, "root-1", "kratos-root", userSchema.RoleSuperAdmin)
	seedAdminOAuthTestUser(t, helper, "admin-1", "kratos-admin", userSchema.RoleAdmin)
	seedAdminOAuthTestUser(t, helper, "u-1", "kratos-1", userSchema.RoleUser)
	for _, subject := range []string{"kratos-root", "kratos-admin", "kratos-1"} {
		hydra.addConsent(subject, "bot-1", "consent-"+subject)
	}
	hydra.failRevoke("kratos-root", "kratos-admin", "kratos-1")
	app := newHydraClientHandlerTestAppAs(helper, hydra.config, "admin-1", adminCoreModule.RoleAdmin)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/bot-1/active", `{"active":false}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	var resp adminOAuthClientActiveResponse
	decodeUpdatedData(t, envelope, &resp)
	// The actor's own and a plain user's subjects are shown; the super admin's is not,
	// but still makes the revocation incomplete.
	if !reflect.DeepEqual(resp.FailedSubjects, []string{"kratos-1", "kratos-admin"}) || resp.RevokedSubjects != 3 || resp.RevocationComplete {
		t.Fatalf("response = %#v", resp)
	}
	assertLatestAuditMetadata(t, helper, adminAuditActionOAuthClientActiveUpdate, map[string]any{"failedSubjects": float64(3)})
}

func TestAdminRevokeAllPerSubject(t *testing.T) {
	allSubjects := []string{"kratos-1 bot-1", "kratos-2 bot-1", "u-1 bot-1", "u-2 bot-1", "u-3 bot-1"}

	t.Run("every subject revoked", func(t *testing.T) {
		hydra := newFakeHydraClients(t, confidentialBotClient)
		helper := newAdminOAuthTestHelper(t)
		seedBotGrantHolders(t, helper, hydra)
		app := newHydraClientHandlerTestAppAs(helper, hydra.config, "super-admin-1", adminCoreModule.RoleSuperAdmin)

		status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients/bot-1/revoke", "")
		if status != http.StatusOK || envelope.Message != "oauth client authorizations revoked" {
			t.Fatalf("status = %d, body = %#v", status, envelope)
		}
		var resp adminOAuthClientRevokeResponse
		decodeUpdatedData(t, envelope, &resp)
		if resp.RevokedSubjects != 5 || len(resp.FailedSubjects) != 0 || !resp.RevocationComplete || resp.RevokedAuthorizations != 3 || !resp.RevokeTokens {
			t.Fatalf("response = %#v", resp)
		}
		if !bytes.Contains(envelope.UpdatedData, []byte(`"failedSubjects":[]`)) {
			t.Fatalf("updatedData %s must encode failedSubjects as []", envelope.UpdatedData)
		}
		requests := hydra.takeRequests()
		if got := consentRevocations(t, requests); !reflect.DeepEqual(got, allSubjects) {
			t.Fatalf("consent revocations = %v, want %v", got, allSubjects)
		}
		if last := requests[len(requests)-1]; last.Path != "/admin/oauth2/tokens" || last.Query.Get("client_id") != "bot-1" {
			t.Fatalf("last request = %s %s, want the access-token delete", last.Method, last.Path)
		}
		assertConsentClients(t, hydra, map[string][]string{"kratos-1": {}, "u-2": {}, "u-3": {}, "kratos-4": {"other-1"}})
		assertLatestAuditMetadata(t, helper, adminAuditActionOAuthClientRevoke, map[string]any{
			"revokedSubjects": float64(5), "failedSubjects": float64(0), "revocationComplete": true, "revokedAuthorizations": float64(3),
		})
	})

	t.Run("one subject fails", func(t *testing.T) {
		hydra := newFakeHydraClients(t, confidentialBotClient)
		helper := newAdminOAuthTestHelper(t)
		seedBotGrantHolders(t, helper, hydra)
		hydra.failRevoke("u-2")
		app := newHydraClientHandlerTestAppAs(helper, hydra.config, "super-admin-1", adminCoreModule.RoleSuperAdmin)

		status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients/bot-1/revoke", "")
		if status != http.StatusOK || envelope.Message != "oauth client authorizations revoked partially" {
			t.Fatalf("status = %d, body = %#v", status, envelope)
		}
		var resp adminOAuthClientRevokeResponse
		decodeUpdatedData(t, envelope, &resp)
		// u-2's legacy session stays, so only the other two authorizations count.
		if resp.RevokedSubjects != 4 || !reflect.DeepEqual(resp.FailedSubjects, []string{"u-2"}) || resp.RevocationComplete || resp.RevokedAuthorizations != 2 {
			t.Fatalf("response = %#v", resp)
		}
		if got := consentRevocations(t, hydra.takeRequests()); !reflect.DeepEqual(got, allSubjects) {
			t.Fatalf("consent revocations = %v, want every subject tried", got)
		}
		assertConsentClients(t, hydra, map[string][]string{"u-2": {"bot-1"}})
	})

	t.Run("every subject fails", func(t *testing.T) {
		hydra := newFakeHydraClients(t, confidentialBotClient)
		helper := newAdminOAuthTestHelper(t)
		seedBotGrantHolders(t, helper, hydra)
		hydra.failRevoke("kratos-1", "u-1", "kratos-2", "u-2", "u-3")
		app := newHydraClientHandlerTestAppAs(helper, hydra.config, "super-admin-1", adminCoreModule.RoleSuperAdmin)

		status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients/bot-1/revoke", "")
		if status != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 when nothing was revoked, body = %#v", status, envelope)
		}
		requests := hydra.takeRequests()
		if got := consentRevocations(t, requests); !reflect.DeepEqual(got, allSubjects) {
			t.Fatalf("consent revocations = %v", got)
		}
		if slices.ContainsFunc(requests, func(r fakeHydraRequest) bool { return r.Path == "/admin/oauth2/tokens" }) {
			t.Fatalf("requests = %v, want no token delete after a failed revoke", requestLines(requests))
		}
		assertLatestAuditRow(t, helper, adminAuditActionOAuthClientRevoke, systemlog.ResultFailure, map[string]any{
			"reason": adminFailureReasonRevokeAuthorizationsFailed, "revokedSubjects": float64(0), "failedSubjects": float64(5),
		})
	})

	// Hydra answers 204 for the holder's empty fallback subject, so one subject is
	// "revoked" although the only grant is still there. That is still a failure.
	t.Run("only the holder's Kratos subject fails", func(t *testing.T) {
		hydra := newFakeHydraClients(t, confidentialBotClient)
		helper := newAdminOAuthTestHelper(t)
		seedAdminOAuthTestUser(t, helper, "u-1", "kratos-1", userSchema.RoleUser)
		hydra.addConsent("kratos-1", "bot-1", "consent-1")
		hydra.failRevoke("kratos-1")
		app := newHydraClientHandlerTestAppAs(helper, hydra.config, "super-admin-1", adminCoreModule.RoleSuperAdmin)

		status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients/bot-1/revoke", "")
		if status != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 when no grant was revoked, body = %#v", status, envelope)
		}
		requests := hydra.takeRequests()
		if got, want := consentRevocations(t, requests), []string{"kratos-1 bot-1", "u-1 bot-1"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("consent revocations = %v, want %v", got, want)
		}
		if slices.ContainsFunc(requests, func(r fakeHydraRequest) bool { return r.Path == "/admin/oauth2/tokens" }) {
			t.Fatalf("requests = %v, want no token delete after a failed revoke", requestLines(requests))
		}
		assertConsentClients(t, hydra, map[string][]string{"kratos-1": {"bot-1"}})
		assertLatestAuditRow(t, helper, adminAuditActionOAuthClientRevoke, systemlog.ResultFailure, map[string]any{
			"reason": adminFailureReasonRevokeAuthorizationsFailed, "revokedSubjects": float64(1), "failedSubjects": float64(1),
		})
	})

	t.Run("no grant holders", func(t *testing.T) {
		hydra := newFakeHydraClients(t, confidentialBotClient)
		helper := newAdminOAuthTestHelper(t)
		seedAdminOAuthTestUser(t, helper, "u-4", "kratos-4", userSchema.RoleUser)
		hydra.addConsent("kratos-4", "other-1", "consent-4")
		app := newHydraClientHandlerTestAppAs(helper, hydra.config, "super-admin-1", adminCoreModule.RoleSuperAdmin)

		status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients/bot-1/revoke", "")
		if status != http.StatusOK || envelope.Message != "oauth client authorizations revoked" {
			t.Fatalf("status = %d, want 200 when there is nothing to revoke, body = %#v", status, envelope)
		}
		var resp adminOAuthClientRevokeResponse
		decodeUpdatedData(t, envelope, &resp)
		if resp.RevokedSubjects != 0 || resp.RevokedAuthorizations != 0 || len(resp.FailedSubjects) != 0 || !resp.RevocationComplete {
			t.Fatalf("response = %#v", resp)
		}
		requests := hydra.takeRequests()
		if got := consentRevocations(t, requests); len(got) != 0 {
			t.Fatalf("consent revocations = %v, want none", got)
		}
		if last := requests[len(requests)-1]; last.Path != "/admin/oauth2/tokens" || last.Query.Get("client_id") != "bot-1" {
			t.Fatalf("last request = %s %s, want the access-token delete", last.Method, last.Path)
		}
	})

	t.Run("one target user keeps subject+client", func(t *testing.T) {
		hydra := newFakeHydraClients(t, confidentialBotClient)
		helper := newAdminOAuthTestHelper(t)
		seedBotGrantHolders(t, helper, hydra)
		app := newHydraClientHandlerTestAppAs(helper, hydra.config, "super-admin-1", adminCoreModule.RoleSuperAdmin)

		status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients/bot-1/revoke", `{"targetUserId":"u-2","revokeTokens":false}`)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body = %#v", status, envelope)
		}
		var resp adminOAuthClientRevokeResponse
		decodeUpdatedData(t, envelope, &resp)
		if resp.RevokedSubjects != 2 || resp.RevokedAuthorizations != 1 || !resp.RevocationComplete {
			t.Fatalf("response = %#v", resp)
		}
		if got, want := consentRevocations(t, hydra.takeRequests()), []string{"kratos-2 bot-1", "u-2 bot-1"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("consent revocations = %v, want %v", got, want)
		}
		assertConsentClients(t, hydra, map[string][]string{"u-2": {}, "kratos-1": {"bot-1"}})
	})
}

func TestDeleteClientDoesNotCallInvalidRevoke(t *testing.T) {
	hydra := newFakeHydraClients(t, confidentialBotClient)
	helper := newAdminOAuthTestHelper(t)
	seedBotGrantHolders(t, helper, hydra)
	app := newHydraClientHandlerTestAppAs(helper, hydra.config, "super-admin-1", adminCoreModule.RoleSuperAdmin)

	// Default options: deleteAuthorizations and deleteTokens are both true.
	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodDelete, "/oauth-clients/bot-1", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	var resp adminOAuthClientDeleteResponse
	decodeUpdatedData(t, envelope, &resp)
	if !resp.DeleteAuthorizations || resp.DeletedAuthorizations != 3 {
		t.Fatalf("response = %#v, want the 3 authorizations counted", resp)
	}
	requests := hydra.takeRequests()
	for _, request := range requests {
		if request.Path == "/admin/oauth2/auth/sessions/consent" && request.Method == http.MethodDelete {
			t.Fatalf("delete sent a consent revocation %v; deleting the client cascades instead", request.Query)
		}
	}
	if last := requests[len(requests)-1]; last.Method != http.MethodDelete || last.Path != "/admin/clients/bot-1" {
		t.Fatalf("requests = %v, want the client deleted last", requestLines(requests))
	}
	hydra.mu.Lock()
	_, exists := hydra.clients["bot-1"]
	hydra.mu.Unlock()
	if exists {
		t.Fatalf("client bot-1 still exists")
	}
	assertConsentClients(t, hydra, map[string][]string{"kratos-1": {}, "u-2": {}, "u-3": {}, "kratos-4": {"other-1"}})
}

func TestRevokeHydraClientGrantsPerSubjectRefusesEmptyClient(t *testing.T) {
	hydra := newFakeHydraClients(t)
	if _, err := revokeHydraClientGrantsPerSubject(t.Context(), nil, hydra.config, " "); err == nil {
		t.Fatalf("an empty client id must be refused: it would revoke every client of each subject")
	}
	if got := hydra.takeRequests(); len(got) != 0 {
		t.Fatalf("requests = %v, want none", requestLines(got))
	}
}

// newAdminOAuthTestHelper returns a helper backed by an in-memory database, which
// the grant listing and the audit log need.
func newAdminOAuthTestHelper(t *testing.T) *harukiAPIHelper.HarukiToolboxRouterHelpers {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	client := enttest.Open(t, "sqlite3", "file:"+name+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	return &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: client}}
}

func seedAdminOAuthTestUser(t *testing.T, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, id, kratosIdentityID string, role userSchema.Role) {
	t.Helper()
	builder := helper.DBManager.DB.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").SetRole(role)
	if kratosIdentityID != "" {
		builder.SetKratosIdentityID(kratosIdentityID)
	}
	if _, err := builder.Save(t.Context()); err != nil {
		t.Fatalf("seed user %s: %v", id, err)
	}
}

// seedBotGrantHolders seeds three holders of a bot-1 grant and one bystander:
//   - u-1 (kratos-1): session under its Kratos identity
//   - u-2 (kratos-2): legacy session under its local user ID
//   - u-3 (no Kratos identity): session under its local user ID
//   - u-4 (kratos-4): a session for other-1 only
func seedBotGrantHolders(t *testing.T, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydra *fakeHydraClients) {
	t.Helper()
	seedAdminOAuthTestUser(t, helper, "u-1", "kratos-1", userSchema.RoleUser)
	seedAdminOAuthTestUser(t, helper, "u-2", "kratos-2", userSchema.RoleUser)
	seedAdminOAuthTestUser(t, helper, "u-3", "", userSchema.RoleUser)
	seedAdminOAuthTestUser(t, helper, "u-4", "kratos-4", userSchema.RoleUser)
	hydra.addConsent("kratos-1", "bot-1", "consent-1")
	hydra.addConsent("u-2", "bot-1", "consent-2")
	hydra.addConsent("u-3", "bot-1", "consent-3")
	hydra.addConsent("kratos-4", "other-1", "consent-4")
}

// consentRevocations returns "subject client" for every consent revocation, sorted,
// and fails the test on any revocation that is not subject+client.
func consentRevocations(t *testing.T, requests []fakeHydraRequest) []string {
	t.Helper()
	var revocations []string
	for _, request := range requests {
		if request.Method != http.MethodDelete || request.Path != "/admin/oauth2/auth/sessions/consent" {
			continue
		}
		query := request.Query
		if query.Get("subject") == "" || query.Get("client") == "" || query.Has("all") || query.Has("consent_request_id") {
			t.Fatalf("consent revocation %q is not subject+client", query.Encode())
		}
		revocations = append(revocations, query.Get("subject")+" "+query.Get("client"))
	}
	slices.Sort(revocations)
	return revocations
}

func assertConsentClients(t *testing.T, hydra *fakeHydraClients, want map[string][]string) {
	t.Helper()
	for subject, clients := range want {
		if got := hydra.consentClients(subject); !slices.Equal(got, clients) {
			t.Fatalf("consent sessions of %s = %v, want %v", subject, got, clients)
		}
	}
}

func assertLatestAuditMetadata(t *testing.T, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string, want map[string]any) {
	t.Helper()
	assertLatestAuditRow(t, helper, action, systemlog.ResultSuccess, want)
}

func assertLatestAuditRow(t *testing.T, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string, result systemlog.Result, want map[string]any) {
	t.Helper()
	row, err := helper.DBManager.DB.SystemLog.Query().
		Where(systemlog.ActionEQ(action)).
		Order(postgresql.Desc(systemlog.FieldID)).
		First(t.Context())
	if err != nil {
		t.Fatalf("query audit log %s: %v", action, err)
	}
	if row.Result != result {
		t.Fatalf("audit result = %s, want %s (metadata %#v)", row.Result, result, row.Metadata)
	}
	for key, value := range want {
		if !reflect.DeepEqual(row.Metadata[key], value) {
			t.Fatalf("audit metadata %s = %#v, want %#v (metadata %#v)", key, row.Metadata[key], value, row.Metadata)
		}
	}
}
