package admincontent

import (
	"time"
)

const adminAuditTargetIDAll = "all"

const (
	adminFailureReasonInvalidRequestPayload              = "invalid_request_payload"
	adminFailureReasonQueryFriendLinksFailed             = "query_friend_links_failed"
	adminFailureReasonResolveFriendLinkNextIdFailed      = "resolve_friend_link_next_id_failed"
	adminFailureReasonFriendLinkConflict                 = "friend_link_conflict"
	adminFailureReasonCreateFriendLinkFailed             = "create_friend_link_failed"
	adminFailureReasonInvalidFriendLinkId                = "invalid_friend_link_id"
	adminFailureReasonFriendLinkNotFound                 = "friend_link_not_found"
	adminFailureReasonUpdateFriendLinkFailed             = "update_friend_link_failed"
	adminFailureReasonDeleteFriendLinkFailed             = "delete_friend_link_failed"
	adminFailureReasonQueryFriendGroupsFailed            = "query_friend_groups_failed"
	adminFailureReasonFriendGroupConflict                = "friend_group_conflict"
	adminFailureReasonCreateFriendGroupFailed            = "create_friend_group_failed"
	adminFailureReasonInvalidGroupId                     = "invalid_group_id"
	adminFailureReasonStartTransactionFailed             = "start_transaction_failed"
	adminFailureReasonDeleteGroupItemsFailed             = "delete_group_items_failed"
	adminFailureReasonFriendGroupNotFound                = "friend_group_not_found"
	adminFailureReasonDeleteFriendGroupFailed            = "delete_friend_group_failed"
	adminFailureReasonCommitTransactionFailed            = "commit_transaction_failed"
	adminFailureReasonQueryGroupFailed                   = "query_group_failed"
	adminFailureReasonResolveFriendGroupItemNextIdFailed = "resolve_friend_group_item_next_id_failed"
	adminFailureReasonFriendGroupItemConflict            = "friend_group_item_conflict"
	adminFailureReasonCreateFriendGroupItemFailed        = "create_friend_group_item_failed"
	adminFailureReasonInvalidItemId                      = "invalid_item_id"
	adminFailureReasonFriendGroupItemNotFound            = "friend_group_item_not_found"
	adminFailureReasonQueryFriendGroupItemFailed         = "query_friend_group_item_failed"
	adminFailureReasonUpdateFriendGroupItemFailed        = "update_friend_group_item_failed"
	adminFailureReasonDeleteFriendGroupItemFailed        = "delete_friend_group_item_failed"
)

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}
