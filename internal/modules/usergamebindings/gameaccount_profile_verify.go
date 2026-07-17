package usergamebindings

import (
	"context"
	"errors"
	"fmt"
	"strings"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekaiapi"

	"github.com/bytedance/sonic"
)

var (
	errGameAccountProfileRequestFailed = errors.New("failed to request game account profile")
	errGameAccountServerUnavailable    = errors.New("game server unavailable")
	errGameAccountNotFound             = errors.New("game account not found")
	errGameAccountProfileEmpty         = errors.New("empty game account profile response")
	errGameAccountProfileInvalid       = errors.New("invalid game account profile response")
)

// classifyGameAccountProfileError maps the (result, error) pair returned by the
// Sekai profile client onto a sentinel. For definitive upstream conditions
// (server in maintenance, account does not exist) the client returns a
// structured result *alongside* an error; those are user/upstream states, not
// transport failures, so they must be classified from the result before the
// error is treated as a generic request failure. Without this, a non-existent
// game UID surfaces as a 5xx (logged at ERROR) instead of a 400 "game account
// not found" (logged at INFO). Mirrors sendOwnedGameAccountProfile.
func classifyGameAccountProfileError(resultInfo *sekaiapi.HarukiSekaiAPIResult, err error) error {
	if err == nil {
		return nil
	}
	if resultInfo != nil {
		if !resultInfo.ServerAvailable {
			return errGameAccountServerUnavailable
		}
		if !resultInfo.AccountExists {
			return errGameAccountNotFound
		}
	}
	return fmt.Errorf("%w: %v", errGameAccountProfileRequestFailed, err)
}

func verifyGameAccountOwnership(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, gameUserIDStr, serverStr, expectedCode string) error {
	resultInfo, body, err := apiHelper.SekaiAPIClient.GetUserProfile(ctx, gameUserIDStr, serverStr)
	if err != nil {
		return classifyGameAccountProfileError(resultInfo, err)
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
