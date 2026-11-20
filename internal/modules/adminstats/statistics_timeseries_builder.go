package adminstats

import (
	"time"

	adminCore "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"

	"github.com/gofiber/fiber/v3"
)

func buildStatisticsTimeseries(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, from, to time.Time, bucket, tz string, loc *time.Location) (*statisticsTimeseriesResponse, error) {
	points := initializeTimeseriesPoints(from, to, bucket, loc)

	actorID, role, err := adminCore.CurrentAdminActor(ctx)
	if err != nil {
		return nil, err
	}
	all := adminCore.NormalizeRole(role) == adminCore.RoleSuperAdmin
	db := apiHelper.DBManager.DB
	var (
		registrationCounts map[int64]int
		uploadCounts       map[int64]statisticsUploadBucketCount
	)
	if sqlDB := db.PostgresSQLDB(); sqlDB != nil {
		registrationCounts, err = queryRegistrationCountsRawSQL(ctx.Context(), sqlDB, from, to, bucket, tz, actorID, all)
		if err != nil {
			return nil, err
		}
		uploadCounts, err = queryUploadCountsRawSQL(ctx.Context(), sqlDB, from, to, bucket, tz, actorID, all)
		if err != nil {
			return nil, err
		}
	} else {
		registrationCounts, err = queryRegistrationCountsFallback(ctx, apiHelper, from, to, bucket, loc, actorID, all)
		if err != nil {
			return nil, err
		}
		uploadCounts, err = queryUploadCountsFallback(ctx, apiHelper, from, to, bucket, loc, actorID, role)
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
		Timezone:    tz,
		Points:      points,
	}
	return resp, nil
}
