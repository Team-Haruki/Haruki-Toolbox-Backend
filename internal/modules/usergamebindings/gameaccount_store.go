package usergamebindings

import (
	"context"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountdatagrant"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"
)

func clearGameAccountPublicCaches(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, serverStr, gameUserIDStr string) {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil {
		return
	}
	gameUserID, err := strconv.ParseInt(strings.TrimSpace(gameUserIDStr), 10, 64)
	if err != nil {
		harukiLogger.Warnf("Failed to parse game user id for cache clear: server=%s gameUserID=%s err=%v", serverStr, gameUserIDStr, err)
		return
	}
	if err := apiHelper.DBManager.Redis.ClearPublicGameDataCaches(ctx, serverStr, gameUserID); err != nil {
		harukiLogger.Warnf("Failed to clear public game data caches: server=%s gameUserID=%s err=%v", serverStr, gameUserIDStr, err)
	}
}

func getUserBindings(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string) ([]harukiAPIHelper.GameAccountBinding, error) {
	bindings, err := apiHelper.DBManager.DB.GameAccountBinding.
		Query().
		Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(userID))).
		All(ctx)
	if err != nil {
		return nil, err
	}
	var resp []harukiAPIHelper.GameAccountBinding
	for _, b := range bindings {
		resp = append(resp, harukiAPIHelper.GameAccountBinding{
			Server:   utils.SupportedDataUploadServer(b.Server),
			UserID:   b.GameUserID,
			Verified: b.Verified,
			Suite:    b.Suite,
			Mysekai:  b.Mysekai,
		})
	}
	return resp, nil
}

func queryExistingBinding(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, serverStr, gameUserIDStr string) (*postgresql.GameAccountBinding, error) {
	existing, err := apiHelper.DBManager.DB.GameAccountBinding.
		Query().
		Where(
			gameaccountbinding.ServerEQ(serverStr),
			gameaccountbinding.GameUserID(gameUserIDStr),
		).
		WithUser().
		Only(ctx)
	if err != nil {
		if postgresql.IsNotFound(err) {
			return nil, nil
		}
		harukiLogger.Errorf("Failed to query existing binding: %v", err)
		return nil, err
	}
	return existing, nil
}

type gameAccountBindingSaveResult struct {
	Transferred         bool
	PreviousOwnerUserID string
	PreviousOwnerEmail  string
}

var sendGameAccountBindingTransferMail = func(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, email, body string) error {
	return apiHelper.SMTPClient.Send([]string{email}, "游戏账号绑定变更通知 | Haruki工具箱", body, "Haruki工具箱 | 星云科技")
}

func saveGameAccountBinding(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, existing *postgresql.GameAccountBinding, serverStr, gameUserIDStr, userID string, req harukiAPIHelper.CreateGameAccountBindingPayload) (*gameAccountBindingSaveResult, error) {
	result := &gameAccountBindingSaveResult{}
	if existing != nil {
		previousOwnerID := bindingOwnerID(existing)
		if previousOwnerID != "" && previousOwnerID != strings.TrimSpace(userID) {
			result.Transferred = true
			result.PreviousOwnerUserID = previousOwnerID
			if existing.Edges.User != nil {
				result.PreviousOwnerEmail = strings.TrimSpace(existing.Edges.User.Email)
			}
		}

		tx, err := apiHelper.DBManager.DB.Tx(ctx)
		if err != nil {
			return nil, err
		}
		update := tx.GameAccountBinding.UpdateOneID(existing.ID).
			SetVerified(true).
			SetSuite(req.Suite).
			SetMysekai(req.MySekai).
			SetUserID(userID)
		if _, err = update.Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if result.Transferred {
			if _, err = tx.GameAccountDataGrant.Delete().
				Where(
					gameaccountdatagrant.OwnerUserIDEQ(result.PreviousOwnerUserID),
					gameaccountdatagrant.ServerEQ(serverStr),
					gameaccountdatagrant.GameUserIDEQ(gameUserIDStr),
				).
				Exec(ctx); err != nil {
				_ = tx.Rollback()
				return nil, err
			}
		}
		if err = tx.Commit(); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		return result, nil
	}

	_, err := apiHelper.DBManager.DB.GameAccountBinding.
		Create().
		SetServer(serverStr).
		SetGameUserID(gameUserIDStr).
		SetVerified(true).
		SetSuite(req.Suite).
		SetMysekai(req.MySekai).
		SetUserID(userID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func notifyPreviousGameAccountBindingOwner(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, transfer *gameAccountBindingSaveResult, serverStr, gameUserIDStr string, transferTime time.Time) {
	if transfer == nil || !transfer.Transferred {
		return
	}
	email := strings.TrimSpace(transfer.PreviousOwnerEmail)
	if email == "" {
		harukiLogger.Warnf("Skip game account binding transfer notification because previous owner email is empty: userID=%s server=%s gameUserID=%s", transfer.PreviousOwnerUserID, serverStr, gameUserIDStr)
		return
	}
	if apiHelper == nil || apiHelper.SMTPClient == nil {
		harukiLogger.Warnf("Skip game account binding transfer notification because SMTP client is unavailable: userID=%s server=%s gameUserID=%s", transfer.PreviousOwnerUserID, serverStr, gameUserIDStr)
		return
	}
	body := buildGameAccountBindingTransferMailBody(serverStr, gameUserIDStr, transferTime)
	if err := sendGameAccountBindingTransferMail(apiHelper, email, body); err != nil {
		harukiLogger.Warnf("Failed to send game account binding transfer notification to previous owner %s: %v", transfer.PreviousOwnerUserID, err)
	}
}

func buildGameAccountBindingTransferMailBody(serverStr, gameUserIDStr string, transferTime time.Time) string {
	body := smtp.GameAccountBindingTransferTemplate
	replacements := map[string]string{
		"{{SERVER}}":        html.EscapeString(strings.ToUpper(strings.TrimSpace(serverStr))),
		"{{GAME_USER_ID}}":  html.EscapeString(strings.TrimSpace(gameUserIDStr)),
		"{{TRANSFER_TIME}}": html.EscapeString(transferTime.UTC().Format(time.RFC3339)),
	}
	for old, newValue := range replacements {
		body = strings.ReplaceAll(body, old, newValue)
	}
	return body
}
