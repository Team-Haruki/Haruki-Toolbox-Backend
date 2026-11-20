package adminstats

import (
	"time"

	adminCore "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/uploadlog"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

func buildDashboardStatistics(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, windowHours int) (*dashboardStatisticsResponse, error) {
	db := apiHelper.DBManager.DB
	dbCtx := ctx.Context()

	actorID, role, err := adminCore.CurrentAdminActor(ctx)
	if err != nil {
		return nil, err
	}
	all := adminCore.NormalizeRole(role) == adminCore.RoleSuperAdmin
	visibleUsers := visibleStatisticsUsers(actorID, all)
	users := db.User.Query().Where(visibleUsers)
	bindings := db.GameAccountBinding.Query()
	if !all {
		bindings.Where(gameaccountbinding.HasUserWith(visibleUsers))
	}
	uploads, err := scopeUploadLogsForAdminActor(dbCtx, db, db.UploadLog.Query(), role, actorID)
	if err != nil {
		return nil, err
	}

	now := adminNowUTC()
	windowStart := now.Add(-time.Duration(windowHours) * time.Hour)

	userTotal, err := users.Clone().Count(dbCtx)
	if err != nil {
		return nil, err
	}
	userBanned, err := users.Clone().Where(userSchema.BannedEQ(true)).Count(dbCtx)
	if err != nil {
		return nil, err
	}
	adminCount, err := users.Clone().Where(userSchema.RoleEQ(userSchema.RoleAdmin)).Count(dbCtx)
	if err != nil {
		return nil, err
	}
	superAdminCount, err := users.Clone().Where(userSchema.RoleEQ(userSchema.RoleSuperAdmin)).Count(dbCtx)
	if err != nil {
		return nil, err
	}

	bindingTotal, err := bindings.Clone().Count(dbCtx)
	if err != nil {
		return nil, err
	}
	bindingVerified, err := bindings.Clone().Where(gameaccountbinding.VerifiedEQ(true)).Count(dbCtx)
	if err != nil {
		return nil, err
	}
	var bindingByServerRows []struct {
		Key   string `json:"server"`
		Count int    `json:"count"`
	}
	if err := bindings.Clone().
		GroupBy(gameaccountbinding.FieldServer).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(dbCtx, &bindingByServerRows); err != nil {
		return nil, err
	}
	bindingByServer := make([]groupedFieldCount, 0, len(bindingByServerRows))
	for _, row := range bindingByServerRows {
		bindingByServer = append(bindingByServer, groupedFieldCount{Key: row.Key, Count: row.Count})
	}

	uploadTotalAllTime, err := uploads.Clone().Count(dbCtx)
	if err != nil {
		return nil, err
	}
	uploadTotalWindow, err := uploads.Clone().Where(uploadlog.UploadTimeGTE(windowStart)).Count(dbCtx)
	if err != nil {
		return nil, err
	}
	uploadSuccessWindow, err := uploads.Clone().Where(uploadlog.UploadTimeGTE(windowStart), uploadlog.SuccessEQ(true)).Count(dbCtx)
	if err != nil {
		return nil, err
	}
	uploadFailedWindow := uploadTotalWindow - uploadSuccessWindow
	if uploadFailedWindow < 0 {
		uploadFailedWindow = 0
	}

	var uploadByMethodRows []struct {
		Key   string `json:"upload_method"`
		Count int    `json:"count"`
	}
	if err := uploads.Clone().
		Where(uploadlog.UploadTimeGTE(windowStart)).
		GroupBy(uploadlog.FieldUploadMethod).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(dbCtx, &uploadByMethodRows); err != nil {
		return nil, err
	}
	uploadByMethod := make([]groupedFieldCount, 0, len(uploadByMethodRows))
	for _, row := range uploadByMethodRows {
		uploadByMethod = append(uploadByMethod, groupedFieldCount{Key: row.Key, Count: row.Count})
	}

	var uploadByDataTypeRows []struct {
		Key   string `json:"data_type"`
		Count int    `json:"count"`
	}
	if err := uploads.Clone().
		Where(uploadlog.UploadTimeGTE(windowStart)).
		GroupBy(uploadlog.FieldDataType).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(dbCtx, &uploadByDataTypeRows); err != nil {
		return nil, err
	}
	uploadByDataType := make([]groupedFieldCount, 0, len(uploadByDataTypeRows))
	for _, row := range uploadByDataTypeRows {
		uploadByDataType = append(uploadByDataType, groupedFieldCount{Key: row.Key, Count: row.Count})
	}

	var methodDataTypeRows []groupedMethodDataTypeCount
	if err := uploads.Clone().
		Where(uploadlog.UploadTimeGTE(windowStart)).
		GroupBy(uploadlog.FieldUploadMethod, uploadlog.FieldDataType).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(dbCtx, &methodDataTypeRows); err != nil {
		return nil, err
	}

	resp := &dashboardStatisticsResponse{
		GeneratedAt: now,
		Users: dashboardUserStats{
			Total:      userTotal,
			Banned:     userBanned,
			Admin:      adminCount,
			SuperAdmin: superAdminCount,
		},
		Bindings: dashboardGameBindingStats{
			Total:    bindingTotal,
			Verified: bindingVerified,
			ByServer: normalizeCategoryCounts(bindingByServer),
		},
		Uploads: dashboardUploadStats{
			WindowHours:         windowHours,
			WindowStart:         windowStart,
			WindowEnd:           now,
			TotalAllTime:        uploadTotalAllTime,
			Total:               uploadTotalWindow,
			Success:             uploadSuccessWindow,
			Failed:              uploadFailedWindow,
			ByMethod:            normalizeCategoryCounts(uploadByMethod),
			ByDataType:          normalizeCategoryCounts(uploadByDataType),
			ByMethodAndDataType: normalizeMethodDataTypeCounts(methodDataTypeRows),
		},
	}

	return resp, nil
}
