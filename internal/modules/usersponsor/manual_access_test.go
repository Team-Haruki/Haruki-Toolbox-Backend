package usersponsor

import (
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

func TestManualSponsorAccessControlsOwnerAndBotWithoutAfdian(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	frame := frameCatalog()[0]
	e.db.User.UpdateOneID("alice").SetSponsorAccessOverride(user.SponsorAccessOverrideGranted).SetSponsorAccessNote("private-proof").SetSponsorAccessUpdatedBy("private-admin").ExecX(ctx)
	b := e.db.GameAccountBinding.Create().SetServer("cn").SetGameUserID("12345").SetUserID("alice").SetVerified(true).SaveX(ctx)
	status, body := e.request(t, "PUT", "/api/user/alice/sponsor-frames/cn/12345", "alice", "session", map[string]string{"frameId": frame.ID})
	if status != 200 || !strings.Contains(string(body), `"eligible":true`) || strings.Contains(string(body), "private-") {
		t.Fatalf("manual owner status: %d %s", status, body)
	}
	// Unlinking never clears a separate administrative grant.
	e.request(t, "DELETE", "/api/user/alice/afdian", "alice", "session", nil)
	_, body = e.request(t, "GET", "/game-frame/cn/12345", "", "", nil)
	if !strings.Contains(string(body), frame.Horizontal.Base) {
		t.Fatalf("manual bot frame missing: %s", body)
	}
	e.db.User.UpdateOneID("alice").SetSponsorAccessOverride(user.SponsorAccessOverrideBlocked).ExecX(ctx)
	// Even a new successful verification must not undo an administrative block.
	if err := e.h.bindIdentity(ctx, "alice", "afdian-alice", "order"); err != nil {
		t.Fatal(err)
	}
	e.db.Sponsor.Create().SetID("afdian_alice").SetAfdianUserID("afdian-alice").SaveX(ctx)
	status, body = e.request(t, "PUT", "/api/user/alice/sponsor-frames/cn/12345", "alice", "session", map[string]string{"frameId": frame.ID})
	if status != 403 {
		t.Fatalf("blocked owner saved a frame: %d %s", status, body)
	}
	_, body = e.request(t, "GET", "/game-frame/cn/12345", "", "", nil)
	if string(body) != `{"frame":null}` || e.db.GameAccountBinding.GetX(ctx, b.ID).SponsorFrameID == nil {
		t.Fatalf("block did not preserve the inactive selection: %s", body)
	}
	e.db.User.UpdateOneID("alice").ClearSponsorAccessOverride().ExecX(ctx)
	_, body = e.request(t, "GET", "/game-frame/cn/12345", "", "", nil)
	if !strings.Contains(string(body), frame.Horizontal.Base) {
		t.Fatal("automatic access did not resume")
	}
	e.db.User.UpdateOneID("alice").SetSponsorAccessOverride(user.SponsorAccessOverrideGranted).SetBanned(true).ExecX(ctx)
	_, body = e.request(t, "GET", "/game-frame/cn/12345", "", "", nil)
	if string(body) != `{"frame":null}` {
		t.Fatal("manual grant bypassed a ban")
	}
}
