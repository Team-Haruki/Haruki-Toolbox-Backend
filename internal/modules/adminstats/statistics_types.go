package adminstats

import "time"

type categoryCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type methodDataTypeCount struct {
	UploadMethod string `json:"uploadMethod"`
	DataType     string `json:"dataType"`
	Count        int    `json:"count"`
}

type dashboardUserStats struct {
	Total      int `json:"total"`
	Banned     int `json:"banned"`
	Admin      int `json:"admin"`
	SuperAdmin int `json:"superAdmin"`
}

type dashboardGameBindingStats struct {
	Total    int             `json:"total"`
	Verified int             `json:"verified"`
	ByServer []categoryCount `json:"byServer"`
}

type dashboardUploadStats struct {
	WindowHours         int                   `json:"windowHours"`
	WindowStart         time.Time             `json:"windowStart"`
	WindowEnd           time.Time             `json:"windowEnd"`
	TotalAllTime        int                   `json:"totalAllTime"`
	Total               int                   `json:"total"`
	Success             int                   `json:"success"`
	Failed              int                   `json:"failed"`
	ByMethod            []categoryCount       `json:"byMethod"`
	ByDataType          []categoryCount       `json:"byDataType"`
	ByMethodAndDataType []methodDataTypeCount `json:"byMethodAndDataType"`
}

type dashboardStatisticsResponse struct {
	GeneratedAt time.Time                 `json:"generatedAt"`
	Users       dashboardUserStats        `json:"users"`
	Bindings    dashboardGameBindingStats `json:"bindings"`
	Uploads     dashboardUploadStats      `json:"uploads"`
}

type statisticsTimeseriesPoint struct {
	Time            time.Time `json:"time"`
	Registrations   int       `json:"registrations"`
	Uploads         int       `json:"uploads"`
	UploadSuccesses int       `json:"uploadSuccesses"`
	UploadFailures  int       `json:"uploadFailures"`
}

type statisticsTimeseriesResponse struct {
	GeneratedAt time.Time                   `json:"generatedAt"`
	From        time.Time                   `json:"from"`
	To          time.Time                   `json:"to"`
	Bucket      string                      `json:"bucket"`
	Points      []statisticsTimeseriesPoint `json:"points"`
}

type groupedFieldCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type groupedMethodDataTypeCount struct {
	UploadMethod string `json:"upload_method"`
	DataType     string `json:"data_type"`
	Count        int    `json:"count"`
}
