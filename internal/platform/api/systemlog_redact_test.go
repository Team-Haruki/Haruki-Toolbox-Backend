package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
)

// System log rows keep the request path; a path that carries a credential
// must be stored redacted, and redaction runs before the 512-byte cut.
func TestWriteSystemLogRedactsCredentialPaths(t *testing.T) {
	t.Parallel()

	db := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	defer func() { _ = db.Close() }()
	helper := &HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db}}

	cases := map[string]string{
		"/api/ios/script/FAKEuploadcode07/upload":         "/api/ios/script/<redacted>/upload",
		"/api/sponsor/afdian/callback/FAKEafdiansecret07": "/api/sponsor/afdian/callback/<redacted>",
		"/inherit/user/FAKEinheritID0007":                 "/inherit/user/<redacted>",
		"/api/user/42/game-account/jp/12345678901234567":  "/api/user/42/game-account/jp/12345678901234567",
		// The code straddles the 512-byte cut; only its redacted form may be
		// cut, or the stored prefix would carry the head of the code.
		"/" + strings.Repeat("a", 495) + "/ios/module/FAKEmodulecode07/x": ("/" + strings.Repeat("a", 495) + "/ios/module/<redacted>/x")[:512],
	}
	for in := range cases {
		path := in
		if err := WriteSystemLog(context.Background(), helper, SystemLogEntry{
			Action:    "test.redact",
			ActorType: SystemLogActorTypeAnonymous,
			Result:    SystemLogResultSuccess,
			Path:      &path,
		}); err != nil {
			t.Fatalf("WriteSystemLog(%q): %v", in, err)
		}
	}

	rows, err := db.SystemLog.Query().All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stored := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.Path == nil {
			t.Fatalf("row %d lost its path", row.ID)
		}
		for _, secret := range []string{"FAKEuploadcode07", "FAKEafdiansecret07", "FAKEinheritID0007", "FAKEmodulecode07"} {
			if strings.Contains(*row.Path, secret) {
				t.Fatalf("stored path leaked %q: %q", secret, *row.Path)
			}
		}
		stored[*row.Path] = true
	}
	for in, want := range cases {
		if !stored[want] {
			t.Fatalf("path %q: want stored %q, have %v", in, want, stored)
		}
	}
}
