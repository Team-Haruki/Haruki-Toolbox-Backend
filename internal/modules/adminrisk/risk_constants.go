package adminrisk

const (
	defaultRiskEventPage     = 1
	defaultRiskEventPageSize = 50
	maxRiskEventPageSize     = 200
	defaultRiskEventSort     = "event_time_desc"

	riskEventSortEventTimeDesc = "event_time_desc"
	riskEventSortEventTimeAsc  = "event_time_asc"
	riskEventSortIDDesc        = "id_desc"
	riskEventSortIDAsc         = "id_asc"
)

var validRiskStatuses = []string{"open", "resolved"}

var validRiskSeverities = []string{"low", "medium", "high", "critical"}
