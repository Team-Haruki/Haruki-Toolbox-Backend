package usersponsor

import (
	"slices"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

func TestEndfieldFramesAreReservedForCloudConfiguration(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.db.User.UpdateOneID("alice").SetSponsorAccessOverride(user.SponsorAccessOverrideGranted).ExecX(ctx)
	// A preference saved before the catalog restriction must no longer override
	// Cloud configuration, nor leave an unselectable frame in the settings UI.
	reserved := "icon_user_avatar_frame_endfield_1"
	b := e.db.GameAccountBinding.Create().SetServer("cn").SetGameUserID("12345").SetUserID("alice").SetVerified(true).SetSponsorFrameID(reserved).SaveX(ctx)
	status, body := e.request(t, "GET", "/api/user/alice/sponsor-frames", "alice", "session", nil)
	var response struct {
		Data struct {
			Frames   []Frame        `json:"frames"`
			Accounts []frameAccount `json:"accounts"`
		} `json:"updatedData"`
	}
	if status != 200 || jsoncodec.Unmarshal(body, &response) != nil {
		t.Fatalf("catalog response: %d %s", status, body)
	}
	var ids []string
	for _, frame := range response.Data.Frames {
		ids = append(ids, frame.ID)
	}
	if !slices.Equal(ids, []string{"rgb_coiled_dragon", "rgb_thunder", "gif_laughing_yellow_20261001"}) {
		t.Fatalf("unexpected user-selectable catalog: %v", ids)
	}
	if len(response.Data.Accounts) != 1 || response.Data.Accounts[0].FrameID != nil {
		t.Fatalf("reserved preference exposed: %s", body)
	}
	status, body = e.request(t, "GET", "/game-frame/cn/12345", "", "", nil)
	if status != 200 || string(body) != `{"frame":null}` {
		t.Fatalf("reserved preference still overrides Cloud: %d %s", status, body)
	}
	for _, id := range []string{reserved, "icon_user_avatar_frame_activity_1", "icon_user_avatar_frame_bp_1", "icon_user_avatar_frame_channel_v1d5_3"} {
		status, body = e.request(t, "PUT", "/api/user/alice/sponsor-frames/cn/12345", "alice", "session", map[string]string{"frameId": id})
		if status != 400 {
			t.Fatalf("reserved frame %s accepted: %d %s", id, status, body)
		}
	}
	if frame := e.db.GameAccountBinding.GetX(ctx, b.ID).SponsorFrameID; frame == nil || *frame != reserved {
		t.Fatal("rejected selections changed the stored preference")
	}
	status, body = e.request(t, "PUT", "/api/user/alice/sponsor-frames/cn/12345", "alice", "session", map[string]string{"frameId": ""})
	if status != 200 || e.db.GameAccountBinding.GetX(ctx, b.ID).SponsorFrameID != nil {
		t.Fatalf("could not clear a legacy reserved preference: %d %s", status, body)
	}
}
