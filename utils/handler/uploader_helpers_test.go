package handler

import (
	harukiConfig "haruki-suite/config"
	"haruki-suite/ent/toolbox/schema"
	"haruki-suite/utils"
	apiHelper "haruki-suite/utils/api"
	"testing"
)

func TestBuildSyncTargetsSuite(t *testing.T) {
	t.Parallel()

	cfg := harukiConfig.ThirdPartyDataProviderConfig{
		Endpoint8823:            "https://8823/upload",
		Secret8823:              "sec8823",
		SendJSONZstandard8823:   true,
		CheckEnabled8823:        true,
		CheckURL8823:            "https://8823/check/{user_id}/{server}",
		RestoreSuite8823:        true,
		EndpointSakura:          "https://sakura/upload",
		SecretSakura:            "secsakura",
		SendJSONZstandardSakura: true,
		CheckEnabledSakura:      true,
		CheckURLSakura:          "https://sakura/check",
		RestoreSuiteSakura:      true,
		EndpointResona:          "https://resona/upload",
		SecretResona:            "secresona",
		SendJSONZstandardResona: false,
		CheckEnabledResona:      false,
		CheckURLResona:          "",
		RestoreSuiteResona:      false,
	}
	settings := apiHelper.HarukiToolboxGameAccountPrivacySettings{
		Suite: &schema.SuiteDataPrivacySettings{
			Allow8823:   true,
			AllowSakura: true,
			AllowResona: true,
		},
	}

	targets := buildSyncTargets(cfg, utils.UploadDataTypeSuite, settings)
	if len(targets) != 3 {
		t.Fatalf("buildSyncTargets suite len = %d, want 3", len(targets))
	}
	if !targets[0].is8823 || !targets[0].restoreSuite {
		t.Fatalf("first target should be 8823 with restoreSuite enabled")
	}
	if targets[1].url != "https://sakura/upload" || !targets[1].restoreSuite {
		t.Fatalf("second target mismatch: %+v", targets[1])
	}
	if targets[2].url != "https://resona/upload" || targets[2].restoreSuite {
		t.Fatalf("third target mismatch: %+v", targets[2])
	}
}

func TestBuildSyncTargetsMysekai(t *testing.T) {
	t.Parallel()

	cfg := harukiConfig.ThirdPartyDataProviderConfig{
		Endpoint8823:            "https://8823/upload",
		Secret8823:              "sec8823",
		SendJSONZstandard8823:   true,
		CheckEnabled8823:        true,
		CheckURL8823:            "https://8823/check",
		RestoreSuite8823:        true,
		EndpointSakura:          "https://sakura/upload",
		SecretSakura:            "secsakura",
		SendJSONZstandardSakura: true,
		CheckEnabledSakura:      true,
		CheckURLSakura:          "https://sakura/check",
		RestoreSuiteSakura:      true,
		EndpointLuna:            "https://luna/upload",
		SecretLuna:              "secluna",
	}
	settings := apiHelper.HarukiToolboxGameAccountPrivacySettings{
		Mysekai: &schema.MysekaiDataPrivacySettings{
			Allow8823: true,
			AllowLuna: true,
		},
	}

	targets := buildSyncTargets(cfg, utils.UploadDataTypeMysekai, settings)
	if len(targets) != 2 {
		t.Fatalf("buildSyncTargets mysekai len = %d, want 2", len(targets))
	}
	for _, target := range targets {
		if target.restoreSuite {
			t.Fatalf("mysekai targets should not enable restoreSuite: %+v", target)
		}
		if target.url == "https://sakura/upload" {
			t.Fatalf("mysekai should not include sakura target")
		}
	}
}

func TestComputeProcessingNeeds(t *testing.T) {
	t.Parallel()

	targets := []syncTarget{
		{sendJSONZstandard: true, restoreSuite: true},
		{sendJSONZstandard: true, restoreSuite: false},
		{sendJSONZstandard: false},
	}

	needsProcessed, needsRestored := computeProcessingNeeds(targets, utils.UploadDataTypeSuite)
	if !needsProcessed || !needsRestored {
		t.Fatalf("suite processing needs mismatch: processed=%v restored=%v", needsProcessed, needsRestored)
	}

	needsProcessed, needsRestored = computeProcessingNeeds(targets, utils.UploadDataTypeMysekai)
	if !needsProcessed || needsRestored {
		t.Fatalf("mysekai processing needs mismatch: processed=%v restored=%v", needsProcessed, needsRestored)
	}
}

func TestChooseSyncPayload(t *testing.T) {
	t.Parallel()

	raw := []byte("raw")
	processed := []byte("processed")
	restored := []byte("restored")

	target := syncTarget{sendJSONZstandard: true, restoreSuite: true}
	gotData, gotFormat := chooseSyncPayload(
		target,
		utils.UploadDataTypeSuite,
		raw,
		processed,
		restored,
		true,
		true,
	)
	if string(gotData) != "restored" || gotFormat != utils.HarukiDataSyncerDataFormatJsonZstd {
		t.Fatalf("suite restored payload mismatch: data=%q format=%q", string(gotData), gotFormat)
	}

	target = syncTarget{sendJSONZstandard: true}
	gotData, gotFormat = chooseSyncPayload(
		target,
		utils.UploadDataTypeMysekai,
		raw,
		processed,
		restored,
		true,
		false,
	)
	if string(gotData) != "processed" || gotFormat != utils.HarukiDataSyncerDataFormatJsonZstd {
		t.Fatalf("processed payload mismatch: data=%q format=%q", string(gotData), gotFormat)
	}

	target = syncTarget{}
	gotData, gotFormat = chooseSyncPayload(
		target,
		utils.UploadDataTypeMysekai,
		raw,
		processed,
		restored,
		false,
		false,
	)
	if string(gotData) != "raw" || gotFormat != utils.HarukiDataSyncerDataFormatRaw {
		t.Fatalf("raw payload mismatch: data=%q format=%q", string(gotData), gotFormat)
	}
}

func TestBuildSyncHeaders(t *testing.T) {
	t.Parallel()

	target := syncTarget{is8823: true, secret: "sec"}
	headers := buildSyncHeaders(target, 123, utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite)
	if headers[headerXCredentials] != "sec" {
		t.Fatalf("x-credentials mismatch")
	}
	if headers[headerXUserID] != "123" {
		t.Fatalf("x-user-id mismatch")
	}
	if headers[headerXServerRegion] != "jp" {
		t.Fatalf("x-server-region mismatch")
	}
	if headers[headerXUploadType] != "suite" {
		t.Fatalf("x-upload-type mismatch")
	}

	target = syncTarget{secret: "token"}
	headers = buildSyncHeaders(target, 1, utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite)
	if headers[headerAuthorization] != "Bearer token" {
		t.Fatalf("authorization mismatch: %q", headers[headerAuthorization])
	}
}

func TestReplaceSyncURLPlaceholders(t *testing.T) {
	t.Parallel()

	got := replaceSyncURLPlaceholders(
		"https://example.com/u/{user_id}/s/{server}/t/{data_type}",
		999,
		utils.SupportedDataUploadServerEN,
		utils.UploadDataTypeMysekai,
	)
	want := "https://example.com/u/999/s/en/t/mysekai"
	if got != want {
		t.Fatalf("replaceSyncURLPlaceholders = %q, want %q", got, want)
	}
}
