package handler

import (
	"fmt"
	"haruki-suite/utils"
	"regexp"
	"strconv"
	"time"
)

var (
	jpenImagePathPattern  = regexp.MustCompile(`^[a-f0-9]{64}/[a-f0-9]{64}$`)
	otherImagePathPattern = regexp.MustCompile(`^(\d+)_([0-9a-fA-F-]{36})$`)
)

func (h *DataHandler) PreHandleData(
	data map[string]any,
	expectedUserID *int64,
	parsedUserID *int64,
	server utils.SupportedDataUploadServer,
	dataType utils.UploadDataType,
) (map[string]any, error) {
	if err := validateUserIDMatch(expectedUserID, parsedUserID, dataType); err != nil {
		return nil, err
	}
	if dataType == utils.UploadDataTypeMysekai {
		if err := h.validateMysekaiData(data, expectedUserID, server); err != nil {
			return nil, err
		}
	}
	if dataType == utils.UploadDataTypeSuite {
		if err := validateSuiteData(data); err != nil {
			return nil, err
		}
		data = cleanSuite(data)
		if shouldRestoreSuiteForDB(server) {
			if r := getSuiteRestorer(server); r != nil {
				data = r.RestoreFields(data)
			}
		}
	}
	if dataType == utils.UploadDataTypeMysekaiBirthdayParty {
		if err := validateBirthdayPartyData(data); err != nil {
			return nil, err
		}
		data = extractBirthdayPartyData(data)
	}
	data["upload_time"] = time.Now().Unix()
	data["_id"] = expectedUserID
	data["server"] = string(server)
	return data, nil
}

func validateUserIDMatch(expectedUserID, parsedUserID *int64, dataType utils.UploadDataType) error {
	if dataType == utils.UploadDataTypeSuite && parsedUserID != nil && expectedUserID != nil && *expectedUserID != *parsedUserID {
		return fmt.Errorf(
			"invalid userID: %s, expected: %s",
			strconv.FormatInt(*parsedUserID, 10),
			strconv.FormatInt(*expectedUserID, 10),
		)
	}
	return nil
}

func (h *DataHandler) validateMysekaiData(
	data map[string]any,
	expectedUserID *int64,
	server utils.SupportedDataUploadServer,
) error {
	updatedResources, ok := data["updatedResources"].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid data: missing updatedResources")
	}
	photos, ok := updatedResources["userMysekaiPhotos"].([]any)
	if !ok || len(photos) == 0 {
		return fmt.Errorf("no userMysekaiPhotos found, it seems you may not have taken a photo yet")
	}
	firstPhoto, ok := photos[0].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid photo data")
	}
	imagePath, ok := firstPhoto["imagePath"].(string)
	if imagePath == "" || !ok {
		return fmt.Errorf("missing imagePath")
	}
	return h.validateImagePath(imagePath, expectedUserID, server)
}

func (h *DataHandler) validateImagePath(
	imagePath string,
	expectedUserID *int64,
	server utils.SupportedDataUploadServer,
) error {
	if server == utils.SupportedDataUploadServerJP || server == utils.SupportedDataUploadServerEN {
		return validateJPENImagePath(imagePath, server)
	}
	return h.validateOtherServerImagePath(imagePath, expectedUserID, server)
}

func validateJPENImagePath(imagePath string, server utils.SupportedDataUploadServer) error {
	if !jpenImagePathPattern.MatchString(imagePath) {
		return fmt.Errorf("invalid server: %s", server)
	}
	return nil
}

func (h *DataHandler) validateOtherServerImagePath(
	imagePath string,
	expectedUserID *int64,
	server utils.SupportedDataUploadServer,
) error {
	matches := otherImagePathPattern.FindStringSubmatch(imagePath)
	if len(matches) != 3 {
		return fmt.Errorf("invalid imagePath format")
	}
	uid := matches[1]
	uidInt, err := strconv.ParseInt(uid, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid uid format")
	}
	if expectedUserID == nil {
		return fmt.Errorf("expected user ID is nil")
	}
	if uidInt != *expectedUserID {
		return fmt.Errorf("userId %s does not match expected UserId %d", uid, *expectedUserID)
	}

	return h.verifyGameAccountExists(uid, server)
}

func (h *DataHandler) verifyGameAccountExists(uid string, server utils.SupportedDataUploadServer) error {
	resultInfo, _, err := h.SekaiAPIClient.GetUserProfile(uid, string(server))
	if resultInfo == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed to get user profile")
	}
	if !resultInfo.ServerAvailable {
		return fmt.Errorf("sekai api is unavailable")
	}
	if !resultInfo.AccountExists {
		return fmt.Errorf("game account not found")
	}
	return nil
}

func validateSuiteData(data map[string]any) error {
	_, ok := data["userGamedata"]
	_, ok2 := data["userProfile"]
	if !ok && !ok2 {
		return fmt.Errorf("invalid data, it seems you may have uploaded a wrong suite data")
	}
	return nil
}

func validateBirthdayPartyData(data map[string]any) error {
	updatedResources, ok := data["updatedResources"].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid data: missing updatedResources")
	}
	harvestMaps, ok := updatedResources["userMysekaiHarvestMaps"]
	if !ok || harvestMaps == nil {
		return fmt.Errorf("no userMysekaiHarvestMaps found, it seems you may not have participated in the birthday party event yet")
	}
	return nil
}

func extractBirthdayPartyData(data map[string]any) map[string]any {
	updatedResources, _ := data["updatedResources"].(map[string]any)
	harvestMaps := updatedResources["userMysekaiHarvestMaps"]
	return map[string]any{
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": harvestMaps,
		},
	}
}
