package data

import (
	"encoding/json"
	"testing"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiDatabase "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
)

// The PostgreSQL read path hands back an ALREADY RENDERED body as []byte.
// Marshalling that again does not re-emit it: encoding/json and sonic both
// encode a []byte as a base64 STRING. The response would be valid JSON
// containing garbage, with no error raised anywhere, so nothing but a client
// would ever notice.
func TestEncodeGameDataBodyPassesRenderedBytesThrough(t *testing.T) {
	body := []byte(`{"userCards":[{"cardId":1}]}`)
	got, err := EncodeGameDataBody(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("rendered body was re-encoded:\n got %s\nwant %s", got, body)
	}
}

// Guard the hazard directly, so the reason this helper exists stays visible.
func TestMarshallingARenderedBodyWouldBase64It(t *testing.T) {
	body := []byte(`{"a":1}`)
	naive, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(naive) == string(body) {
		t.Fatal("json.Marshal round-tripped a []byte; the hazard this guards is gone")
	}
	if naive[0] != '"' {
		t.Fatalf("expected a JSON string, got %s", naive)
	}
}

// The MongoDB path still returns Go values and must still be marshalled.
func TestEncodeGameDataBodyMarshalsGoValues(t *testing.T) {
	got, err := EncodeGameDataBody(map[string]any{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":1}` {
		t.Fatalf("got %s", got)
	}
}

// The generation stamp must resolve from whichever datastore is authoritative.
//
// Both callers used to reach straight into DBManager.Mongo and return a bare
// false when it was nil. The moment MongoDB is removed that would make the cache
// write fence refuse every write and leave the response cache permanently
// empty — with nothing reporting it, because an empty cache looks exactly like a
// cold one. This asserts the nil-Mongo case now produces an ERROR (which the
// caller turns into the unconfirmed fallback) rather than a silent false.
func TestUploadTimeWithNoStoreConfiguredIsAnErrorNotASilentFalse(t *testing.T) {
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &harukiDatabase.HarukiToolboxDBManager{},
	}
	_, found, err := readUploadTime(t.Context(), helper, "jp", harukiUtils.UploadDataTypeSuite, 1)
	if err == nil {
		t.Fatal("a missing store reported success; the cache fence would fail closed silently")
	}
	if found {
		t.Fatal("found=true with no store")
	}
}
