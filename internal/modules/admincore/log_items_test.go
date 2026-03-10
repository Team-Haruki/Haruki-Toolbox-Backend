package admincore

import (
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/systemlog"
	"testing"
	"time"
)

func TestBuildSystemLogItems(t *testing.T) {
	eventTime := time.Date(2026, 3, 9, 20, 30, 0, 0, time.FixedZone("CST", 8*3600))
	actorUserID := "1001"
	actorRole := "admin"
	targetType := "user"
	targetID := "1002"
	ip := "127.0.0.1"
	userAgent := "ua"
	method := "GET"
	path := "/api/admin/users"
	requestID := "req-1"

	rows := []*postgresql.SystemLog{
		{
			ID:        1,
			EventTime: eventTime,
			ActorType: systemlog.ActorTypeAdmin,
			Action:    "admin.user.list",
			Result:    systemlog.ResultSuccess,
			Metadata:  map[string]any{"k": "v"},
		},
		{
			ID:          2,
			EventTime:   eventTime,
			ActorUserID: &actorUserID,
			ActorRole:   &actorRole,
			ActorType:   systemlog.ActorTypeAdmin,
			Action:      "admin.user.get",
			TargetType:  &targetType,
			TargetID:    &targetID,
			Result:      systemlog.ResultFailure,
			IP:          &ip,
			UserAgent:   &userAgent,
			Method:      &method,
			Path:        &path,
			RequestID:   &requestID,
			Metadata:    map[string]any{"reason": "x"},
		},
	}

	items := BuildSystemLogItems(rows)
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if !items[0].EventTime.Equal(eventTime.UTC()) {
		t.Fatalf("items[0].EventTime = %s, want %s", items[0].EventTime, eventTime.UTC())
	}
	if items[0].ActorUserID != "" {
		t.Fatalf("items[0].ActorUserID = %q, want empty", items[0].ActorUserID)
	}
	if items[1].ActorUserID != actorUserID {
		t.Fatalf("items[1].ActorUserID = %q, want %q", items[1].ActorUserID, actorUserID)
	}
	if items[1].TargetType != targetType || items[1].TargetID != targetID {
		t.Fatalf("items[1] target mismatch: got (%q,%q), want (%q,%q)", items[1].TargetType, items[1].TargetID, targetType, targetID)
	}
	if items[1].IP != ip || items[1].UserAgent != userAgent || items[1].Method != method || items[1].Path != path || items[1].RequestID != requestID {
		t.Fatalf("items[1] request context mismatch")
	}
}

func TestBuildUploadLogItems(t *testing.T) {
	uploadTime := time.Date(2026, 3, 9, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	rows := []*postgresql.UploadLog{
		{
			ID:            10,
			Server:        "jp",
			GameUserID:    "2001",
			ToolboxUserID: "1001",
			DataType:      "suite",
			UploadMethod:  "manual",
			Success:       true,
			UploadTime:    uploadTime,
		},
	}

	items := BuildUploadLogItems(rows)
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	item := items[0]
	if item.ID != 10 || item.Server != "jp" || item.GameUserID != "2001" || item.ToolboxUserID != "1001" || item.DataType != "suite" || item.UploadMethod != "manual" || !item.Success {
		t.Fatalf("item fields mismatch: %+v", item)
	}
	if !item.UploadTime.Equal(uploadTime.UTC()) {
		t.Fatalf("item.UploadTime = %s, want %s", item.UploadTime, uploadTime.UTC())
	}
}

func TestBuildLogItemsEmpty(t *testing.T) {
	if got := BuildSystemLogItems(nil); len(got) != 0 {
		t.Fatalf("len(BuildSystemLogItems(nil)) = %d, want 0", len(got))
	}
	if got := BuildUploadLogItems(nil); len(got) != 0 {
		t.Fatalf("len(BuildUploadLogItems(nil)) = %d, want 0", len(got))
	}
}
