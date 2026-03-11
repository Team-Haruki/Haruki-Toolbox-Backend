package adminoauth

import (
	"testing"
	"time"
)

func TestBuildAdminOAuthBucketExpressionSQL(t *testing.T) {
	t.Parallel()

	hourExpr, err := buildAdminOAuthBucketExpressionSQL(adminOAuthClientTrendBucketHour, "created_at")
	if err != nil {
		t.Fatalf("buildAdminOAuthBucketExpressionSQL(hour) returned error: %v", err)
	}
	if hourExpr == "" {
		t.Fatalf("hour expression should not be empty")
	}

	dayExpr, err := buildAdminOAuthBucketExpressionSQL(adminOAuthClientTrendBucketDay, "created_at")
	if err != nil {
		t.Fatalf("buildAdminOAuthBucketExpressionSQL(day) returned error: %v", err)
	}
	if dayExpr == "" {
		t.Fatalf("day expression should not be empty")
	}

	if _, err := buildAdminOAuthBucketExpressionSQL("minute", "created_at"); err == nil {
		t.Fatalf("expected invalid bucket to fail")
	}
}

func TestAggregateTrendCountsFromTimes(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, time.March, 8, 0, 15, 0, 0, time.UTC)
	to := time.Date(2026, time.March, 8, 3, 45, 0, 0, time.UTC)
	times := []time.Time{
		time.Date(2026, time.March, 8, 0, 20, 0, 0, time.UTC),
		time.Date(2026, time.March, 8, 1, 10, 0, 0, time.UTC),
		time.Date(2026, time.March, 8, 1, 50, 0, 0, time.UTC),
		time.Date(2026, time.March, 8, 4, 0, 0, 0, time.UTC), // out of range
	}
	counts := aggregateTrendCountsFromTimes(times, from, to, adminOAuthClientTrendBucketHour)

	if counts[time.Date(2026, time.March, 8, 0, 0, 0, 0, time.UTC).Unix()] != 1 {
		t.Fatalf("hour 00 count = %d, want 1", counts[time.Date(2026, time.March, 8, 0, 0, 0, 0, time.UTC).Unix()])
	}
	if counts[time.Date(2026, time.March, 8, 1, 0, 0, 0, time.UTC).Unix()] != 2 {
		t.Fatalf("hour 01 count = %d, want 2", counts[time.Date(2026, time.March, 8, 1, 0, 0, 0, time.UTC).Unix()])
	}
	if _, ok := counts[time.Date(2026, time.March, 8, 4, 0, 0, 0, time.UTC).Unix()]; ok {
		t.Fatalf("hour 04 should be out of range")
	}
}
