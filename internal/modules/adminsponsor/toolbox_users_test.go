package adminsponsor

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

const toolboxUsersPath = "/api/admin/sponsors/toolbox-users"

func TestAdminToolboxSponsorAccessLifecycle(t *testing.T) {
	e := newAdminTestEnv(t, sponsor.AfdianConfig{})
	ctx := t.Context()
	path := toolboxUsersPath + "/user-1"
	expectStatus(t, e.do(t, http.MethodPut, path, "admin-1", `{"access":"granted","note":"  银行转账已核对  "}`), 200)
	u := e.db.User.GetX(ctx, "user-1")
	if u.AfdianUserID != nil || u.SponsorAccessOverride == nil || *u.SponsorAccessOverride != user.SponsorAccessOverrideGranted || *u.SponsorAccessNote != "银行转账已核对" || *u.SponsorAccessUpdatedBy != "admin-1" || u.SponsorAccessUpdatedAt == nil {
		t.Fatalf("incorrect manual grant: %+v", u)
	}
	if e.db.Sponsor.Query().CountX(ctx) != 0 {
		t.Fatal("manual grant fabricated a public sponsor")
	}
	list := decode[toolboxSponsorList](t, e.do(t, "GET", toolboxUsersPath, "admin-1", ""))
	if list.Total != 1 || len(list.Items) != 1 || !list.Items[0].Eligible || list.Items[0].Category != "former" || list.Items[0].Note != "银行转账已核对" {
		t.Fatalf("manual sponsor absent from list: %+v", list)
	}
	// A real order link remains intact when access is blocked and restored.
	e.db.User.UpdateOneID("user-1").SetAfdianUserID("payer").SetAfdianVerificationMethod(user.AfdianVerificationMethodOrder).ExecX(ctx)
	e.db.Sponsor.Create().SetID("afdian_payer").SetAfdianUserID("payer").SetPlanExpiresAt(time.Now().Add(-24 * time.Hour)).SaveX(ctx)
	for _, tc := range []struct {
		mode     string
		eligible bool
	}{{"blocked", false}, {"automatic", true}} {
		expectStatus(t, e.do(t, "PUT", path, "admin-1", fmt.Sprintf(`{"access":%q,"note":"核对后调整"}`, tc.mode)), 200)
		list = decode[toolboxSponsorList](t, e.do(t, "GET", toolboxUsersPath, "admin-1", ""))
		if len(list.Items) != 1 || list.Items[0].Eligible != tc.eligible || list.Items[0].VerificationMethod != "order" || *list.Items[0].AfdianUserID != "payer" {
			t.Fatalf("%s: %+v", tc.mode, list)
		}
	}
	logs := e.db.SystemLog.Query().Where(systemlog.ActionEQ(toolboxSponsorAction), systemlog.ResultEQ("success")).AllX(ctx)
	if len(logs) != 3 {
		t.Fatalf("expected audit history, got %d entries", len(logs))
	}
	for _, log := range logs {
		if log.TargetType == nil || *log.TargetType != "user" || log.TargetID == nil || *log.TargetID != "user-1" {
			t.Fatalf("wrong audit target: %+v", log)
		}
	}
}

func TestAdminToolboxSponsorsPermissionsSearchAndPagination(t *testing.T) {
	e := newAdminTestEnv(t, sponsor.AfdianConfig{})
	ctx := t.Context()
	for _, id := range []string{"admin-1", "super-1", "user-1"} {
		e.db.User.UpdateOneID(id).SetSponsorAccessOverride(user.SponsorAccessOverrideGranted).ExecX(ctx)
	}
	for _, actor := range []string{"", "user-1"} {
		want := 403
		if actor == "" {
			want = 401
		}
		expectStatus(t, e.do(t, "GET", toolboxUsersPath, actor, ""), want)
		expectStatus(t, e.do(t, "PUT", toolboxUsersPath+"/user-1", actor, `{"access":"granted","note":"x"}`), want)
	}
	list := decode[toolboxSponsorList](t, e.do(t, "GET", toolboxUsersPath, "admin-1", ""))
	if list.Total != 1 || list.Items[0].UserID != "user-1" {
		t.Fatalf("privileged/self user leaked: %+v", list)
	}
	list = decode[toolboxSponsorList](t, e.do(t, "GET", toolboxUsersPath+"?scope=all&q=super", "admin-1", ""))
	if list.Total != 0 {
		t.Fatal("search leaked a super admin")
	}
	expectStatus(t, e.do(t, "PUT", toolboxUsersPath+"/super-1", "admin-1", `{"access":"granted","note":"x"}`), 403)
	expectStatus(t, e.do(t, "PUT", toolboxUsersPath+"/admin-1", "admin-1", `{"access":"granted","note":"x"}`), 400)
	expectStatus(t, e.do(t, "PUT", toolboxUsersPath+"/admin-1", "super-1", `{"access":"granted","note":"x"}`), 200)
	for _, suffix := range []string{"?scope=all", "?scope=unknown", "?page=0", "?page_size=101", "?page=1000001", "?q=" + strings.Repeat("a", 129)} {
		expectStatus(t, e.do(t, "GET", toolboxUsersPath+suffix, "admin-1", ""), 400)
	}
	e.db.User.Create().SetID("other").SetName("Offline Fan").SetEmail("offline@example.test").SaveX(ctx)
	list = decode[toolboxSponsorList](t, e.do(t, "GET", toolboxUsersPath+"?scope=all&q=offline", "admin-1", ""))
	if list.Total != 1 || list.Items[0].UserID != "other" || list.Items[0].Eligible {
		t.Fatalf("candidate search: %+v", list)
	}
	list = decode[toolboxSponsorList](t, e.do(t, "GET", toolboxUsersPath+"?page_size=1&page=2", "super-1", ""))
	if list.Total != 3 || len(list.Items) != 1 || list.Page != 2 {
		t.Fatalf("pagination: %+v", list)
	}
}

func TestSuperAdminCanSearchAndManageOwnSponsorship(t *testing.T) {
	e := newAdminTestEnv(t, sponsor.AfdianConfig{})
	ctx := t.Context()
	path := toolboxUsersPath + "/super-1"
	search := toolboxUsersPath + "?scope=all&q=super-1"
	list := decode[toolboxSponsorList](t, e.do(t, "GET", search, "super-1", ""))
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].UserID != "super-1" || list.Items[0].Eligible {
		t.Fatalf("super admin cannot find their own unverified sponsorship: %+v", list)
	}
	expectStatus(t, e.do(t, "PUT", path, "super-1", `{"access":"granted","note":""}`), 400)
	expectStatus(t, e.do(t, "PUT", path, "super-1", `{"access":"granted","note":"Self sponsorship confirmed"}`), 200)
	u := e.db.User.GetX(ctx, "super-1")
	if u.Role != user.RoleSuperAdmin || u.AfdianUserID != nil || u.SponsorAccessOverride == nil || *u.SponsorAccessOverride != user.SponsorAccessOverrideGranted || u.SponsorAccessUpdatedBy == nil || *u.SponsorAccessUpdatedBy != "super-1" || u.SponsorAccessUpdatedAt == nil {
		t.Fatal("self grant changed identity/role or omitted its audit attribution")
	}
	list = decode[toolboxSponsorList](t, e.do(t, "GET", toolboxUsersPath, "super-1", ""))
	if list.Total != 1 || !list.Items[0].Eligible || list.Items[0].UserID != "super-1" {
		t.Fatalf("certified self is absent: %+v", list)
	}
	for _, access := range []string{"blocked", "automatic"} {
		expectStatus(t, e.do(t, "PUT", path, "super-1", fmt.Sprintf(`{"access":%q,"note":"Self sponsorship adjustment"}`, access)), 200)
		list = decode[toolboxSponsorList](t, e.do(t, "GET", search, "super-1", ""))
		if list.Total != 1 || list.Items[0].Eligible {
			t.Fatalf("self %s did not remove manual access: %+v", access, list)
		}
	}
	logs := e.db.SystemLog.Query().Where(systemlog.ActionEQ(toolboxSponsorAction), systemlog.ResultEQ("success")).AllX(ctx)
	if len(logs) != 3 {
		t.Fatalf("self audit count = %d, want 3", len(logs))
	}
	for _, log := range logs {
		if log.ActorUserID == nil || *log.ActorUserID != "super-1" || log.TargetID == nil || *log.TargetID != "super-1" {
			t.Fatal("self audit has the wrong actor or target")
		}
	}
	// A role change between actor and target resolution must not allow self access.
	if err := ensureCanManageToolboxSponsor("same", "super_admin", "same", "admin"); err == nil {
		t.Fatal("self exception survived target role change")
	}
	list = decode[toolboxSponsorList](t, e.do(t, "GET", toolboxUsersPath+"?scope=all&q=admin-1", "admin-1", ""))
	if list.Total != 0 {
		t.Fatal("ordinary admin self-search restriction changed")
	}
}

func TestAdminToolboxSponsorRejectsInvalidMutations(t *testing.T) {
	e := newAdminTestEnv(t, sponsor.AfdianConfig{})
	path := toolboxUsersPath + "/user-1"
	for _, body := range []string{`null`, `not json`, `{}`, `{"access":"granted"}`, `{"access":"granted","note":" "}`, `{"access":"invalid","note":"x"}`, `{"access":"granted","note":"` + strings.Repeat("界", 334) + `"}`} {
		expectStatus(t, e.do(t, "PUT", path, "admin-1", body), 400)
	}
	if e.db.User.GetX(t.Context(), "user-1").SponsorAccessOverride != nil {
		t.Fatal("invalid request changed access")
	}
	e.db.User.UpdateOneID("user-1").SetBanned(true).ExecX(t.Context())
	expectStatus(t, e.do(t, "PUT", path, "admin-1", `{"access":"granted","note":"x"}`), 409)
	expectStatus(t, e.do(t, "PUT", toolboxUsersPath+"/missing", "admin-1", `{"access":"granted","note":"x"}`), 404)
}
