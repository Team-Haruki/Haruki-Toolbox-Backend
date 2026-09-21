package upload

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/perfstats"
	"github.com/klauspost/compress/zstd"
)

func syncTestCryptor() sekai.ServerCryptor {
	return sekai.NewServerCryptor(sekai.ServerCryptorConfig{Regions: map[string]utils.CryptoMaterial{"jp": {Key: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", IV: "0102030405060708090a0b0c0d0e0f10"}, "tw": {Key: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", IV: "0102030405060708090a0b0c0d0e0f10"}, "kr": {Key: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", IV: "0102030405060708090a0b0c0d0e0f10"}, "cn": {Key: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", IV: "0102030405060708090a0b0c0d0e0f10"}}})
}
func syncFixture(rows int) map[string]any {
	records := make([]any, rows)
	for i := range records {
		records[i] = map[string]any{"musicId": i, "userId": int64(7486493092749597481), "score": i * 123, "clear": true}
	}
	return map[string]any{"userMusicResults": records, "userGamedata": map[string]any{"userId": int64(7486493092749597481)}, "empty": []any{}, "nil": nil}
}
func decodeSyncJSON(t *testing.T, body []byte) any {
	t.Helper()
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	plain, err := decoder.DecodeAll(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(plain, &v, jsonvalue.Numbers); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestSyncEncodingMatchesBatch3AndOwnsResults(t *testing.T) {
	cryptor := syncTestCryptor()
	server := utils.SupportedDataUploadServerJP
	service := NewSuiteRestoreService(SuiteRestoreServiceOptions{StructuresFile: map[string]string{"jp": writeTestSuiteSchema(t, t.TempDir())}})
	raw, err := cryptor.Pack(syncFixture(100), server)
	if err != nil {
		t.Fatal(err)
	}
	mp, err := cryptor.DecryptToMsgpack(raw, server)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(mp)
	for _, restored := range []bool{false, true} {
		var old, new []byte
		if restored {
			old, err = batch3ProcessDataWithRestore(raw, server, cryptor, service)
		} else {
			old, err = batch3ProcessDataOnce(raw, server, cryptor)
		}
		if err != nil {
			t.Fatal(err)
		}
		if restored {
			new, err = processRestoredMsgpack(mp, server, service)
		} else {
			new, err = processMsgpackOnce(mp)
		}
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(decodeSyncJSON(t, old), decodeSyncJSON(t, new)) {
			t.Fatal("JSON contract changed")
		}
		owned := bytes.Clone(new)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if _, err := processMsgpackOnce(mp); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if !bytes.Equal(new, owned) || !bytes.Equal(mp, before) {
			t.Fatal("pool result or shared input mutated")
		}
	}
	sentinel := errors.New("writer failure")
	if _, err := compressSyncJSON(func(w io.Writer) error { _, _ = w.Write([]byte("partial")); return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("error lost: %v", err)
	}
	if _, err := processMsgpackOnce(mp); err != nil {
		t.Fatal("encoder pool not reusable after error:", err)
	}
}
func TestSyncRejectsRecipientsBeforeEncodingAndKeepsFallback(t *testing.T) {
	var checks atomic.Int32
	check := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { checks.Add(1); w.WriteHeader(http.StatusNotFound) }))
	defer check.Close()
	before := perfstats.Snapshot()
	var sent atomic.Int32
	sender := func(string, int64, utils.SupportedDataUploadServer, utils.UploadDataType, []byte, string, map[string]string) {
		sent.Add(1)
	}
	runDataSyncerTargets([]syncTarget{{url: "unused", sendJSONZstandard: true, checkEnabled: true, checkURL: check.URL}}, 123, utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite, []byte("invalid encrypted body"), sekai.ServerCryptor{}, nil, sender)
	after := perfstats.Snapshot()
	if checks.Load() != 1 || sent.Load() != 0 || after["sync_processed"].Count != before["sync_processed"].Count || after["sync_restored"].Count != before["sync_restored"].Count {
		t.Fatal("rejected recipient caused processing/delivery")
	}
	var format string
	runDataSyncerTargets([]syncTarget{{url: "unused", sendJSONZstandard: true}}, 123, utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite, []byte("raw"), sekai.ServerCryptor{}, nil, func(_ string, _ int64, _ utils.SupportedDataUploadServer, _ utils.UploadDataType, b []byte, f string, _ map[string]string) {
		format = f
		if string(b) != "raw" {
			t.Error("fallback body changed")
		}
	})
	if format != utils.HarukiDataSyncerDataFormatRaw {
		t.Fatal("raw fallback removed")
	}
}
func TestSyncRecipientChecksAreConcurrent(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	targets := make([]syncTarget, 4)
	for i := range targets {
		targets[i] = syncTarget{url: "unused", checkEnabled: true, checkURL: server.URL}
	}
	done := make(chan []syncTarget, 1)
	go func() {
		done <- eligibleSyncTargets(targets, 123, utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite)
	}()
	for range 4 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("checks were serialized")
		}
	}
	unblock()
	if got := <-done; len(got) != 4 {
		t.Fatal("eligible target lost")
	}
}
func BenchmarkSyncBothFormats(b *testing.B) {
	c := syncTestCryptor()
	server := utils.SupportedDataUploadServerJP
	raw, err := c.Pack(syncFixture(5000), server)
	if err != nil {
		b.Fatal(err)
	}
	service := NewSuiteRestoreService(SuiteRestoreServiceOptions{})
	b.Run("Batch3", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := batch3ProcessDataOnce(raw, server, c); err != nil {
				b.Fatal(err)
			}
			if _, err := batch3ProcessDataWithRestore(raw, server, c, service); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("SharedDecryptStreamJSON", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			mp, err := c.DecryptToMsgpack(raw, server)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := processMsgpackOnce(mp); err != nil {
				b.Fatal(err)
			}
			if _, err := processRestoredMsgpack(mp, server, service); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestSyncBothFormatsDeliverAndRestoreFallback(t *testing.T) {
	c := syncTestCryptor()
	region := utils.SupportedDataUploadServerJP
	raw, err := c.Pack(syncFixture(3), region)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	results := map[string][]byte{}
	send := func(url string, _ int64, _ utils.SupportedDataUploadServer, _ utils.UploadDataType, body []byte, format string, _ map[string]string) {
		if format != utils.HarukiDataSyncerDataFormatJsonZstd {
			t.Error("expected JSON encoding")
		}
		mu.Lock()
		results[url] = bytes.Clone(body)
		mu.Unlock()
	}
	targets := []syncTarget{{url: "processed", sendJSONZstandard: true}, {url: "restored", sendJSONZstandard: true, restoreSuite: true}}
	runDataSyncerTargets(targets, 123, region, utils.UploadDataTypeSuite, raw, c, nil, send)
	if len(results) != 2 || !bytes.Equal(results["processed"], results["restored"]) {
		t.Fatal("failed restore no longer falls back to available processed format")
	}
	// A restored-only failure must retain raw fallback rather than inventing an
	// unrequested processed representation.
	runDataSyncerTargets(targets[1:], 123, region, utils.UploadDataTypeSuite, raw, c, nil, func(_ string, _ int64, _ utils.SupportedDataUploadServer, _ utils.UploadDataType, body []byte, format string, _ map[string]string) {
		if format != utils.HarukiDataSyncerDataFormatRaw || !bytes.Equal(body, raw) {
			t.Error("restored-only raw fallback changed")
		}
	})
}
