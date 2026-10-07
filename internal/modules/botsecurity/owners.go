package botsecurity

import (
	"context"
	"errors"
	"strconv"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg"
	botUser "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg/user"
)

// ErrBotDBNotConfigured is returned by an OwnerLookup when haruki_bot.db_url
// is not set. Alerts are then listed without owners and nothing is logged.
var ErrBotDBNotConfigured = errors.New("bot database not configured")

// OwnerLookup resolves bot ids to the owner's QQ number from the HarukiBot
// database (table "user": bot_id -> owner_user_id). Bot ids missing from the
// result have no registered owner.
type OwnerLookup interface {
	OwnerQQs(ctx context.Context, botIDs []int) (map[int]int64, error)
}

// botDBOwnerLookup reads the bot database through a getter so a client
// attached after route registration is still used.
type botDBOwnerLookup struct {
	client func() *neopg.Client
}

// NewBotDBOwnerLookup returns an OwnerLookup over the HarukiBot database the
// getter returns; a nil client means the bot database is not configured.
func NewBotDBOwnerLookup(client func() *neopg.Client) OwnerLookup {
	return botDBOwnerLookup{client: client}
}

// helperBotDBOwnerLookup reads apiHelper.DBManager.BotDB.
func helperBotDBOwnerLookup(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) OwnerLookup {
	return NewBotDBOwnerLookup(func() *neopg.Client {
		if apiHelper == nil || apiHelper.DBManager == nil {
			return nil
		}
		return apiHelper.DBManager.BotDB
	})
}

func (l botDBOwnerLookup) OwnerQQs(ctx context.Context, botIDs []int) (map[int]int64, error) {
	var client *neopg.Client
	if l.client != nil {
		client = l.client()
	}
	if client == nil {
		return nil, ErrBotDBNotConfigured
	}
	if len(botIDs) == 0 {
		return map[int]int64{}, nil
	}
	rows, err := client.User.Query().
		Where(botUser.BotIDIn(botIDs...)).
		Select(botUser.FieldBotID, botUser.FieldOwnerUserID).
		All(ctx)
	if err != nil {
		return nil, err
	}
	owners := make(map[int]int64, len(rows))
	for _, row := range rows {
		owners[row.BotID] = row.OwnerUserID
	}
	return owners, nil
}

// numericBotID parses a stored bot id for the bot database lookup. Ids that
// are not positive integers have no owner.
func numericBotID(botID string) (int, bool) {
	id, err := strconv.Atoi(botID)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
