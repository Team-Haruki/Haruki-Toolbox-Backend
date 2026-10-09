package bootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sponsorModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	_ "github.com/mattn/go-sqlite3"
)

var schedulerDBSeq atomic.Int64

func newSchedulerDB(t *testing.T) *dbManager.Client {
	t.Helper()
	db := enttest.Open(t, "sqlite3", fmt.Sprintf("file:scheduler-%d-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano(), schedulerDBSeq.Add(1)))
	t.Cleanup(func() { _ = db.Close() })
	// One hand-entered row still carrying the legacy single expiry.
	if err := db.Sponsor.Create().SetID("manual_legacy").SetSource(sponsorSchema.SourceManual).
		SetPlanExpiresAt(time.Now().Add(72 * time.Hour)).SetPaidAt(time.Now().Add(-24 * time.Hour)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}

// countingAfdian answers every list call with an empty page and counts the
// query-order calls.
func countingAfdian(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var orderCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/query-order") {
			orderCalls.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ec":200,"data":{"total_page":1,"list":[]}}`))
	}))
	t.Cleanup(server.Close)
	return server, &orderCalls
}

func schedulerLogger() *harukiLogger.Logger {
	return harukiLogger.NewLogger("AfdianSchedulerTest", "DEBUG", io.Discard)
}

func pendingSplit(t *testing.T, db *dbManager.Client) bool {
	t.Helper()
	pending, err := sponsorModule.HasUnsplitSponsors(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	return pending
}

func TestAfdianSchedulerSplitsWithoutCredentials(t *testing.T) {
	db := newSchedulerDB(t)
	wait := startAfdianSponsorSyncScheduler(t.Context(), db, sponsorModule.NewAfdianConfig(sponsorModule.AfdianConfigOptions{}), schedulerLogger())
	wait()
	if pendingSplit(t, db) {
		t.Fatalf("legacy row not split when webhook orders are the only history")
	}
}

func TestAfdianSchedulerRunsOneFullPassWhenSyncDisabled(t *testing.T) {
	server, orderCalls := countingAfdian(t)
	cfg := sponsorModule.NewAfdianConfig(sponsorModule.AfdianConfigOptions{UserID: "dev", APIToken: "token", APIBaseURL: server.URL})

	db := newSchedulerDB(t)
	startAfdianSponsorSyncScheduler(t.Context(), db, cfg, schedulerLogger())()
	if pendingSplit(t, db) || orderCalls.Load() != 1 {
		t.Fatalf("pending=%v order calls=%d, want one full pass that splits", pendingSplit(t, db), orderCalls.Load())
	}

	// Nothing pending: no pass at all.
	startAfdianSponsorSyncScheduler(t.Context(), db, cfg, schedulerLogger())()
	if orderCalls.Load() != 1 {
		t.Fatalf("order calls = %d, want no extra pass", orderCalls.Load())
	}
}

func TestAfdianSchedulerPeriodicPasses(t *testing.T) {
	server, orderCalls := countingAfdian(t)
	cfg := sponsorModule.NewAfdianConfig(sponsorModule.AfdianConfigOptions{
		UserID: "dev", APIToken: "token", APIBaseURL: server.URL,
		SyncEnabled: true, SyncInterval: 10 * time.Millisecond,
	})
	db := newSchedulerDB(t)
	ctx, cancel := context.WithCancel(t.Context())
	wait := startAfdianSponsorSyncScheduler(ctx, db, cfg, schedulerLogger())
	deadline := time.Now().Add(5 * time.Second)
	for orderCalls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	wait()
	if orderCalls.Load() < 3 || pendingSplit(t, db) {
		t.Fatalf("order calls = %d pending=%v", orderCalls.Load(), pendingSplit(t, db))
	}
}

func TestAfdianSchedulerLogsFailedPass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	cfg := sponsorModule.NewAfdianConfig(sponsorModule.AfdianConfigOptions{UserID: "dev", APIToken: "token", APIBaseURL: server.URL})
	db := newSchedulerDB(t)
	runAfdianSponsorSync(t.Context(), db, cfg, schedulerLogger(), sponsorModule.SyncOptions{Full: true})
	if !pendingSplit(t, db) {
		t.Fatalf("a failed pass must not split")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	runAfdianSponsorSync(canceled, db, cfg, schedulerLogger(), sponsorModule.SyncOptions{})
}
