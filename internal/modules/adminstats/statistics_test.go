package adminstats

import (
	"haruki-suite/utils/database/postgresql"
	"strings"
	"testing"
	"time"
)

func TestParseStatisticsWindowHours(t *testing.T) {
	t.Run("default when empty", func(t *testing.T) {
		hours, err := parseStatisticsWindowHours("")
		if err != nil {
			t.Fatalf("parseStatisticsWindowHours returned error: %v", err)
		}
		if hours != defaultStatisticsWindowHours {
			t.Fatalf("hours = %d, want %d", hours, defaultStatisticsWindowHours)
		}
	})

	t.Run("accept valid value", func(t *testing.T) {
		hours, err := parseStatisticsWindowHours("48")
		if err != nil {
			t.Fatalf("parseStatisticsWindowHours returned error: %v", err)
		}
		if hours != 48 {
			t.Fatalf("hours = %d, want %d", hours, 48)
		}
	})

	t.Run("reject non integer", func(t *testing.T) {
		if _, err := parseStatisticsWindowHours("abc"); err == nil {
			t.Fatalf("expected error for non-integer hours")
		}
	})

	t.Run("reject non positive", func(t *testing.T) {
		if _, err := parseStatisticsWindowHours("0"); err == nil {
			t.Fatalf("expected error for zero hours")
		}
		if _, err := parseStatisticsWindowHours("-1"); err == nil {
			t.Fatalf("expected error for negative hours")
		}
	})

	t.Run("reject over max", func(t *testing.T) {
		if _, err := parseStatisticsWindowHours("9999"); err == nil {
			t.Fatalf("expected error for too-large hours")
		}
	})
}

func TestNormalizeCategoryCounts(t *testing.T) {
	rows := []groupedFieldCount{
		{Key: "b", Count: 3},
		{Key: "a", Count: 3},
		{Key: "c", Count: 5},
	}

	got := normalizeCategoryCounts(rows)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if got[0].Key != "c" || got[0].Count != 5 {
		t.Fatalf("got[0] = %#v, want key=c,count=5", got[0])
	}
	if got[1].Key != "a" || got[1].Count != 3 {
		t.Fatalf("got[1] = %#v, want key=a,count=3", got[1])
	}
	if got[2].Key != "b" || got[2].Count != 3 {
		t.Fatalf("got[2] = %#v, want key=b,count=3", got[2])
	}
}

func TestNormalizeMethodDataTypeCounts(t *testing.T) {
	rows := []groupedMethodDataTypeCount{
		{UploadMethod: "manual", DataType: "suite", Count: 2},
		{UploadMethod: "inherit", DataType: "suite", Count: 2},
		{UploadMethod: "manual", DataType: "mysekai", Count: 3},
	}

	got := normalizeMethodDataTypeCounts(rows)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if got[0].UploadMethod != "manual" || got[0].DataType != "mysekai" || got[0].Count != 3 {
		t.Fatalf("got[0] = %#v, want manual/mysekai/3", got[0])
	}
	if got[1].UploadMethod != "inherit" || got[1].DataType != "suite" || got[1].Count != 2 {
		t.Fatalf("got[1] = %#v, want inherit/suite/2", got[1])
	}
	if got[2].UploadMethod != "manual" || got[2].DataType != "suite" || got[2].Count != 2 {
		t.Fatalf("got[2] = %#v, want manual/suite/2", got[2])
	}
}

func TestParseStatisticsTimeseriesBucket(t *testing.T) {
	value, err := parseStatisticsTimeseriesBucket("")
	if err != nil {
		t.Fatalf("parseStatisticsTimeseriesBucket returned error: %v", err)
	}
	if value != timeseriesBucketHour {
		t.Fatalf("value = %q, want %q", value, timeseriesBucketHour)
	}

	value, err = parseStatisticsTimeseriesBucket("day")
	if err != nil {
		t.Fatalf("parseStatisticsTimeseriesBucket returned error: %v", err)
	}
	if value != timeseriesBucketDay {
		t.Fatalf("value = %q, want %q", value, timeseriesBucketDay)
	}

	if _, err := parseStatisticsTimeseriesBucket("week"); err == nil {
		t.Fatalf("expected invalid bucket to fail")
	}
}

func TestAccumulateRegistrationTimeseriesFromUsers(t *testing.T) {
	from := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)
	points := initializeTimeseriesPoints(from, to, timeseriesBucketHour)
	pointByTime := make(map[time.Time]*statisticsTimeseriesPoint, len(points))
	for i := range points {
		pointByTime[points[i].Time] = &points[i]
	}

	t1 := from.Add(10 * time.Minute)
	t2 := from.Add(70 * time.Minute)
	rows := []*postgresql.User{
		{CreatedAt: &t1},
		{CreatedAt: &t2},
		{CreatedAt: nil}, // historical users with null created_at should be ignored.
	}

	accumulateRegistrationTimeseriesFromUsers(rows, pointByTime, timeseriesBucketHour)

	if pointByTime[from].Registrations != 1 {
		t.Fatalf("first bucket registrations = %d, want 1", pointByTime[from].Registrations)
	}
	secondBucket := from.Add(time.Hour)
	if pointByTime[secondBucket].Registrations != 1 {
		t.Fatalf("second bucket registrations = %d, want 1", pointByTime[secondBucket].Registrations)
	}
	thirdBucket := from.Add(2 * time.Hour)
	if pointByTime[thirdBucket].Registrations != 0 {
		t.Fatalf("third bucket registrations = %d, want 0", pointByTime[thirdBucket].Registrations)
	}
}

func TestBuildStatisticsBucketExpressionSQL(t *testing.T) {
	t.Parallel()

	hourExpr, err := buildStatisticsBucketExpressionSQL(timeseriesBucketHour, "created_at")
	if err != nil {
		t.Fatalf("buildStatisticsBucketExpressionSQL(hour) returned error: %v", err)
	}
	if !strings.Contains(hourExpr, "date_trunc('hour'") {
		t.Fatalf("hour expression = %q, expected hour date_trunc", hourExpr)
	}

	dayExpr, err := buildStatisticsBucketExpressionSQL(timeseriesBucketDay, "upload_time")
	if err != nil {
		t.Fatalf("buildStatisticsBucketExpressionSQL(day) returned error: %v", err)
	}
	if !strings.Contains(dayExpr, "date_trunc('day'") {
		t.Fatalf("day expression = %q, expected day date_trunc", dayExpr)
	}

	if _, err := buildStatisticsBucketExpressionSQL("minute", "created_at"); err == nil {
		t.Fatalf("expected invalid bucket to fail")
	}
}
