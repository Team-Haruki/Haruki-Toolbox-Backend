package upload

import (
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"regexp"
	"strconv"
	"strings"
)

var userIDSuffixRegex = regexp.MustCompile(`user/(\d+)`)

func ExtractUploadTypeAndUserID(originalURL string) (harukiUtils.UploadDataType, int64) {
	if strings.Contains(originalURL, string(harukiUtils.UploadDataTypeSuite)) {
		match := userIDSuffixRegex.FindStringSubmatch(originalURL)
		if len(match) < 2 {
			return "", 0
		}
		userID, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return "", 0
		}
		return harukiUtils.UploadDataTypeSuite, userID
	} else if strings.Contains(originalURL, "birthday-party") && strings.Contains(originalURL, string(harukiUtils.UploadDataTypeMysekai)) {
		match := userIDSuffixRegex.FindStringSubmatch(originalURL)
		if len(match) < 2 {
			return "", 0
		}
		userID, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return "", 0
		}
		return harukiUtils.UploadDataTypeMysekaiBirthdayParty, userID
	} else if strings.Contains(originalURL, string(harukiUtils.UploadDataTypeMysekai)) {
		match := userIDSuffixRegex.FindStringSubmatch(originalURL)
		if len(match) < 2 {
			return "", 0
		}
		userID, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return "", 0
		}
		return harukiUtils.UploadDataTypeMysekai, userID
	}
	return "", 0
}
