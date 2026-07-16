package manager

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestUploadTimeFromResult(t *testing.T) {
	cases := []struct {
		name   string
		result bson.D
		want   int64
	}{
		{
			name:   "int64 stamp",
			result: bson.D{{Key: "_id", Value: int64(1)}, {Key: "upload_time", Value: int64(1752600000)}},
			want:   1752600000,
		},
		{
			name:   "int32 stamp from legacy document",
			result: bson.D{{Key: "_id", Value: int64(1)}, {Key: "upload_time", Value: int32(1234)}},
			want:   1234,
		},
		{
			name:   "double stamp from legacy document",
			result: bson.D{{Key: "_id", Value: int64(1)}, {Key: "upload_time", Value: float64(1752600000)}},
			want:   1752600000,
		},
		{
			name:   "document without stamp",
			result: bson.D{{Key: "_id", Value: int64(1)}},
			want:   0,
		},
		{
			name:   "non-numeric stamp",
			result: bson.D{{Key: "_id", Value: int64(1)}, {Key: "upload_time", Value: bson.D{}}},
			want:   0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := uploadTimeFromResult(tc.result); got != tc.want {
				t.Fatalf("uploadTimeFromResult(%v) = %d, want %d", tc.result, got, tc.want)
			}
		})
	}
}
