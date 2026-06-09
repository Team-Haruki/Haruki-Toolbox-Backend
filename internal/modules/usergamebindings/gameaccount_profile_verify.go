package usergamebindings

import (
	"errors"
	"fmt"
	"strings"

	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/bytedance/sonic"
)

var (
	errGameAccountProfileRequestFailed = errors.New("failed to request game account profile")
	errGameAccountServerUnavailable    = errors.New("game server unavailable")
	errGameAccountNotFound             = errors.New("game account not found")
	errGameAccountProfileEmpty         = errors.New("empty game account profile response")
	errGameAccountProfileInvalid       = errors.New("invalid game account profile response")
)

func verifyGameAccountOwnership(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, gameUserIDStr, serverStr, expectedCode string) error {
	resultInfo, body, err := apiHelper.SekaiAPIClient.GetUserProfile(gameUserIDStr, serverStr)
	if err != nil {
		return fmt.Errorf("%w: %v", errGameAccountProfileRequestFailed, err)
	}
	if resultInfo == nil {
		return errGameAccountProfileRequestFailed
	}
	if !resultInfo.ServerAvailable {
		return errGameAccountServerUnavailable
	}
	if !resultInfo.AccountExists {
		return errGameAccountNotFound
	}
	if !resultInfo.Body || len(body) == 0 {
		return errGameAccountProfileEmpty
	}

	var data map[string]any
	if err := sonic.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("%w: %v", errGameAccountProfileInvalid, err)
	}

	if _, hasError := data["errorCode"]; hasError {
		return errGameAccountNotFound
	}
	userProfile, ok := data["userProfile"].(map[string]any)
	if !ok {
		return errGameAccountProfileInvalid
	}
	word, ok := userProfile["word"].(string)
	if !ok {
		return errGameAccountVerificationCodeMissing
	}
	word = strings.TrimSpace(word)
	if !strings.Contains(word, expectedCode) {
		return errGameAccountVerificationCodeMismatch
	}
	return nil
}
