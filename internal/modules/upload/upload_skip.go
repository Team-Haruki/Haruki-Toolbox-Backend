package upload

import (
	"errors"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"

	"github.com/gofiber/fiber/v3"
)

// A skipped upload is neither a success nor a failure: the payload was valid
// but carried nothing to store (today only a birthday party delivery that did
// not refresh the harvest points). Every upload endpoint answers it with 200,
// so clients that predate the skip never count it as a failure, plus
// X-Haruki-Upload-Result: skipped and X-Haruki-Upload-Reason: <reason>. JSON
// endpoints also put upload_result and reason in updatedData.
const proxyUploadReasonHeader = "X-Haruki-Upload-Reason"

// uploadSkippedError is what HandleUpload returns for a skipped upload. It
// writes no upload log and leaves the stored data alone, so callers must
// check for it before treating the error as a failure.
type uploadSkippedError struct {
	reason string
}

func (e *uploadSkippedError) Error() string {
	return "upload skipped: " + e.reason
}

// uploadSkipReason returns the public reason when err is a skipped upload.
func uploadSkipReason(err error) (string, bool) {
	var skipped *uploadSkippedError
	if errors.As(err, &skipped) {
		return skipped.reason, true
	}
	return "", false
}

// uploadSkippedMessages are the response messages; clients match the reason,
// not the message.
var uploadSkippedMessages = map[string]string{
	platform.SkipReasonBirthdayPartyNotRefreshed: "Upload skipped: the birthday party delivery did not refresh the harvest points",
}

func uploadSkippedMessage(reason string) string {
	if message, ok := uploadSkippedMessages[reason]; ok {
		return message
	}
	return "Upload skipped"
}

// setUploadSkipped marks a response as a skipped upload.
func setUploadSkipped(c fiber.Ctx, reason string) {
	c.Set(proxyUploadResultHeader, proxyUploadSkipped)
	c.Set(proxyUploadReasonHeader, reason)
}

type uploadSkippedData struct {
	UploadResult string `json:"upload_result"`
	Reason       string `json:"reason"`
}

// uploadSkippedResponse answers a skipped upload on the endpoints that use the
// plain response envelope (manual, OAuth2 and the iOS script upload).
func uploadSkippedResponse(c fiber.Ctx, reason string) error {
	setUploadSkipped(c, reason)
	return harukiAPIHelper.Responses.UpdatedDataResponse(c, fiber.StatusOK, uploadSkippedMessage(reason), &uploadSkippedData{UploadResult: proxyUploadSkipped, Reason: reason})
}
