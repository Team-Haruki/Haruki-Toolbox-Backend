package adminsyslog

import harukiAPIHelper "haruki-suite/utils/api"

const (
	defaultSystemLogPage       = 1
	defaultSystemLogPageSize   = 50
	maxSystemLogPageSize       = 200
	defaultSystemLogExportSize = 1000
	maxSystemLogExportSize     = 5000
	defaultSystemLogSort       = "event_time_desc"
	systemLogSortEventTimeAsc  = "event_time_asc"
	systemLogSortEventTimeDesc = "event_time_desc"
	systemLogSortIDAsc         = "id_asc"
	systemLogSortIDDesc        = "id_desc"
)

var validSystemLogActorTypes = []string{
	harukiAPIHelper.SystemLogActorTypeAnonymous,
	harukiAPIHelper.SystemLogActorTypeUser,
	harukiAPIHelper.SystemLogActorTypeAdmin,
	harukiAPIHelper.SystemLogActorTypeSystem,
}

var validSystemLogResults = []string{
	harukiAPIHelper.SystemLogResultSuccess,
	harukiAPIHelper.SystemLogResultFailure,
}

const unknownSystemLogFailureReason = "unknown"
