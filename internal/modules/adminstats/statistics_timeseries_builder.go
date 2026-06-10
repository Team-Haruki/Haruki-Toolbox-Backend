package adminstats

import (
	"time"

	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

func buildStatisticsTimeseries(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, from, to time.Time, bucket string) (*statisticsTimeseriesResponse, error) {
	points := initializeTimeseriesPoints(from, to, bucket)

	db := apiHelper.DBManager.DB
	var (
		registrationCounts map[int64]int
		uploadCounts       map[int64]statisticsUploadBucketCount
		err                error
	)
	if sqlDB := db.SQLDB(); sqlDB != nil {
		registrationCounts, err = queryRegistrationCountsRawSQL(ctx.Context(), sqlDB, from, to, bucket)
		if err != nil {
			return nil, err
		}
		uploadCounts, err = queryUploadCountsRawSQL(ctx.Context(), sqlDB, from, to, bucket)
		if err != nil {
			return nil, err
		}
	} else {
		registrationCounts, err = queryRegistrationCountsFallback(ctx, apiHelper, from, to, bucket)
		if err != nil {
			return nil, err
		}
		uploadCounts, err = queryUploadCountsFallback(ctx, apiHelper, from, to, bucket)
		if err != nil {
			return nil, err
		}
	}

	for i := range points {
		key := points[i].Time.Unix()
		if registrations, ok := registrationCounts[key]; ok {
			points[i].Registrations = registrations
		}
		if upload, ok := uploadCounts[key]; ok {
			points[i].Uploads = upload.Total
			points[i].UploadSuccesses = upload.Success
			points[i].UploadFailures = upload.Failure
		}
	}

	resp := &statisticsTimeseriesResponse{
		GeneratedAt: adminNowUTC(),
		From:        from.UTC(),
		To:          to.UTC(),
		Bucket:      bucket,
		Points:      points,
	}
	return resp, nil
}
