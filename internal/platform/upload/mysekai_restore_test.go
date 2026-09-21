package upload

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/nuverserestore"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekaiapi"
	"net/http"
	"net/http/httptest"
	"testing"
)

func harvestUpload() map[string]any {
	return map[string]any{"updatedResources": map[string]any{"userMysekaiHarvestMaps": []any{[]any{5, []any{[]any{111, -3, 7, 20, "spawned", nil}}, []any{[]any{"mysekai_item", 24, -3, 7, 10, 13, "before_drop", 2, nil}}}}}}
}
func TestBirthdayUploadRestoresConfiguredRegion(t *testing.T) {
	r, _ := nuverserestore.NewMysekai(map[string]string{"tw": "../../../data/suite_user_cn_6.4.0.avsc"})
	h := &DataHandler{SuiteRestoreService: NewSuiteRestoreService(SuiteRestoreServiceOptions{MysekaiRestorer: r})}
	uid := int64(42)
	got, err := h.PreHandleData(harvestUpload(), &uid, nil, utils.SupportedDataUploadServerTW, utils.UploadDataTypeMysekaiBirthdayParty)
	if err != nil {
		t.Fatal(err)
	}
	maps := got["updatedResources"].(map[string]any)["userMysekaiHarvestMaps"].([]any)
	if maps[0].(map[string]any)["mysekaiSiteId"] != 5 {
		t.Fatalf("not restored: %#v", got)
	}
	if got["upload_time"] == nil {
		t.Fatal("lost upload stamp")
	}
}
func TestNormalUploadRejectsMalformedHarvestBeforeProfileLookup(t *testing.T) {
	h := &DataHandler{SuiteRestoreService: cnHarvestService(t)}
	data := harvestUpload()
	data["updatedResources"].(map[string]any)["userMysekaiHarvestMaps"] = []any{[]any{5}}
	if _, err := h.PreHandleData(data, nil, nil, utils.SupportedDataUploadServerCN, utils.UploadDataTypeMysekai); err == nil {
		t.Fatal("malformed upload accepted")
	}
}
func TestMysekaiJSONSyncUsesConfiguredRegion(t *testing.T) {
	r, _ := nuverserestore.NewMysekai(map[string]string{"tw": "../../../data/suite_user_cn_6.4.0.avsc"})
	service := NewSuiteRestoreService(SuiteRestoreServiceOptions{MysekaiRestorer: r})
	cryptor := syncTestCryptor()
	raw, err := cryptor.Pack(harvestUpload(), utils.SupportedDataUploadServerTW)
	if err != nil {
		t.Fatal(err)
	}
	sent := 0
	runDataSyncerTargets([]syncTarget{{url: "unused", sendJSONZstandard: true}}, 42, utils.SupportedDataUploadServerTW, utils.UploadDataTypeMysekai, raw, cryptor, service, func(_ string, _ int64, _ utils.SupportedDataUploadServer, _ utils.UploadDataType, b []byte, format string, _ map[string]string) {
		sent++
		if format != utils.HarukiDataSyncerDataFormatJsonZstd {
			t.Errorf("unexpected format %s", format)
			return
		}
		got := decodeSyncJSON(t, b).(map[string]any)["updatedResources"].(map[string]any)["userMysekaiHarvestMaps"].([]any)
		if _, ok := got[0].(map[string]any); !ok {
			t.Errorf("not restored: %#v", got)
		}
	})
	if sent != 1 {
		t.Fatalf("sent=%d", sent)
	}
}

func TestNormalMysekaiUploadKeepsOwnershipValidation(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(200) }))
	defer srv.Close()
	h := &DataHandler{SuiteRestoreService: cnHarvestService(t), SekaiAPIClient: sekaiapi.NewHarukiSekaiAPIClient(srv.URL, "test")}
	uid := int64(42)
	makeData := func() map[string]any {
		data := harvestUpload()
		data["updatedResources"].(map[string]any)["userMysekaiPhotos"] = []any{map[string]any{"imagePath": "42_00000000-0000-0000-0000-000000000000"}}
		return data
	}
	got, err := h.PreHandleData(makeData(), &uid, nil, utils.SupportedDataUploadServerCN, utils.UploadDataTypeMysekai)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["updatedResources"].(map[string]any)["userMysekaiHarvestMaps"].([]any)[0].(map[string]any); !ok {
		t.Fatal("not restored")
	}
	if requests != 1 {
		t.Fatal("account verification skipped")
	}
	other := int64(43)
	if _, err := h.PreHandleData(makeData(), &other, nil, utils.SupportedDataUploadServerCN, utils.UploadDataTypeMysekai); err == nil {
		t.Fatal("foreign photo accepted")
	}
	if requests != 1 {
		t.Fatal("foreign photo reached profile API")
	}
}

func cnHarvestService(t *testing.T) *SuiteRestoreService {
	t.Helper()
	r, err := nuverserestore.NewMysekai(map[string]string{"cn": "../../../data/suite_user_cn_6.4.0.avsc"})
	if err != nil {
		t.Fatal(err)
	}
	return NewSuiteRestoreService(SuiteRestoreServiceOptions{MysekaiRestorer: r})
}
