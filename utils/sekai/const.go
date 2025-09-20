package sekai

const General = "/6O9YhTzP+c8ty/uImK+2w=="

var Refresh = map[string]interface{}{
	"refreshableTypes": []string{
		"new_pending_friend_request",
		"user_report_thanks_message",
		"streaming_virtual_live_reward_status",
	},
}
var RefreshLogin = map[string]interface{}{
	"refreshableTypes": []string{
		"new_pending_friend_request",
		"login_bonus",
		"user_report_thanks_message",
		"streaming_virtual_live_reward_status",
	},
}
var MySekaiRoom = map[string]interface{}{
	"roomProperty": map[string]interface{}{
		"isRSend": 1,
		"values":  map[string]interface{}{},
	},
}
