package adminstats

import harukiUtils "haruki-suite/utils"

const (
	defaultUploadLogPage     = 1
	defaultUploadLogPageSize = 50
	maxUploadLogPageSize     = 200
	defaultUploadLogSort     = "upload_time_desc"

	uploadLogSortUploadTimeDesc = "upload_time_desc"
	uploadLogSortUploadTimeAsc  = "upload_time_asc"
	uploadLogSortIDDesc         = "id_desc"
	uploadLogSortIDAsc          = "id_asc"
)

var validUploadMethods = []string{
	string(harukiUtils.UploadMethodManual),
	string(harukiUtils.UploadMethodIOSProxy),
	string(harukiUtils.UploadMethodIOSScript),
	string(harukiUtils.UploadMethodHarukiProxy),
	string(harukiUtils.UploadMethodInherit),
}
