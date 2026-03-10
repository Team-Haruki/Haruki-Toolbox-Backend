package sekai

import (
	"context"
	"encoding/base64"
	"fmt"
	harukiUtils "haruki-suite/utils"
	"strconv"
	"strings"
	"time"
)

var retrieverSleep = time.Sleep

const (
	retrieverSuiteFollowupQuery = "?isForceAllReload=false&name=user_colorful_pass,user_colorful_pass_v2,user_offline_event"
	retrieverSystemPath         = "/system"
	retrieverInformationPath    = "/information"

	moduleMySekai     = "MYSEKAI"
	moduleMySekaiRoom = "MYSEKAI_ROOM"
)

func upperServerName(server harukiUtils.SupportedInheritUploadServer) string {
	return strings.ToUpper(string(server))
}

func userIDString(userID int64) string {
	return strconv.FormatInt(userID, 10)
}

func suiteBasePath(userID int64) string {
	return fmt.Sprintf("/suite/user/%s", userIDString(userID))
}

func suiteFollowupPath(userID int64) string {
	return suiteBasePath(userID) + retrieverSuiteFollowupQuery
}

func invitationPath(userID int64) string {
	return fmt.Sprintf("/user/%s/invitation", userIDString(userID))
}

func homeRefreshPath(userID int64) string {
	return fmt.Sprintf("/user/%s/home/refresh", userIDString(userID))
}

func mysekaiPath(userID int64) string {
	return fmt.Sprintf("/user/%s/mysekai?isForceAllReloadOnlyMySekai=True", userIDString(userID))
}

func mysekaiRoomPath(userID int64) string {
	uid := userIDString(userID)
	return fmt.Sprintf("/user/%s/mysekai/%s/room", uid, uid)
}

func mysekaiDiarkisPath(userID int64) string {
	return fmt.Sprintf("/user/%s/diarkis-auth?diarkisServerType=mysekai", userIDString(userID))
}

func moduleMaintenancePath(module string) string {
	return fmt.Sprintf("/module-maintenance/%s", module)
}

func hasUserFriends(unpacked map[string]any) bool {
	if unpacked == nil {
		return false
	}
	friends, ok := unpacked["userFriends"]
	return ok && friends != nil
}

func selectRefreshPayload(login bool) map[string]any {
	if login {
		return RequestDataRefreshLogin
	}
	return RequestDataRefresh
}

func shouldRetrieveMysekai(uploadType harukiUtils.UploadDataType) bool {
	return uploadType == harukiUtils.UploadDataTypeMysekai
}

func decodeGeneralRequestData() ([]byte, error) {
	return base64.StdEncoding.DecodeString(RequestDataGeneral)
}

func unpackResponseToMap(body []byte, server harukiUtils.SupportedInheritUploadServer) (map[string]any, error) {
	unpacked, err := Unpack(body, harukiUtils.SupportedDataUploadServer(server))
	if err != nil {
		return nil, err
	}
	unpackedMap, ok := unpacked.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected response type %T", unpacked)
	}
	return unpackedMap, nil
}

func checkMaintenanceFromBody(body []byte, server harukiUtils.SupportedInheritUploadServer) (bool, error) {
	unpacked, err := unpackResponseToMap(body, server)
	if err != nil {
		return false, err
	}
	return unpacked["isOngoing"] == true, nil
}

func callAndIgnoreError(ctx context.Context, c *HarukiSekaiClient, path, method string, body []byte) error {
	_, _, err := c.callAPI(ctx, path, method, body, nil)
	return err
}
