package admin

import "time"

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}
