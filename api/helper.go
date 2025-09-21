package api

import (
	Utils "haruki-suite/utils"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func JSONResponse(c *fiber.Ctx, resp Utils.APIResponse) error {
	status := http.StatusOK
	if resp.Status != nil {
		status = *resp.Status
	}
	return c.Status(status).JSON(resp)
}

func IntPtr(v int) *int {
	return &v
}

func ExtractUploadTypeAndUserID(originalURL string) (Utils.UploadDataType, int64) {
	if strings.Contains(originalURL, string(Utils.UploadDataTypeSuite)) {
		re := regexp.MustCompile(`user/(\d+)`)
		match := re.FindStringSubmatch(originalURL)

		if len(match) < 2 {
			return "", 0
		}

		userID, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return "", 0
		}

		return Utils.UploadDataTypeSuite, userID

	} else if strings.Contains(originalURL, string(Utils.UploadDataTypeMysekai)) {
		re := regexp.MustCompile(`user/(\d+)`)
		match := re.FindStringSubmatch(originalURL)

		if len(match) < 2 {
			return "", 0
		}

		userID, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return "", 0
		}
		return Utils.UploadDataTypeMysekai, userID
	}

	return "", 0
}
