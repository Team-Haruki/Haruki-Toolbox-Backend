package postgresql

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/oauth2clientwebhookendpoint"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

type OAuth2WebhookOwner struct {
	UserID           string
	KratosIdentityID *string
	Banned           bool
}

type OAuth2ClientWebhookCallback struct {
	ClientID    string
	CallbackURL string
	Bearer      string
}

func (c *Client) GetOAuth2WebhookOwnerForGameAccount(ctx context.Context, userID int64, server string) (*OAuth2WebhookOwner, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresql client is nil")
	}
	binding, err := c.GameAccountBinding.Query().
		Where(
			gameaccountbinding.ServerEQ(strings.TrimSpace(server)),
			gameaccountbinding.GameUserIDEQ(strconv.FormatInt(userID, 10)),
			gameaccountbinding.VerifiedEQ(true),
		).
		WithUser(func(q *UserQuery) {
			q.Select(userSchema.FieldID, userSchema.FieldKratosIdentityID, userSchema.FieldBanned)
		}).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	if binding == nil || binding.Edges.User == nil {
		return nil, nil
	}
	owner := binding.Edges.User
	return &OAuth2WebhookOwner{
		UserID:           strings.TrimSpace(owner.ID),
		KratosIdentityID: owner.KratosIdentityID,
		Banned:           owner.Banned,
	}, nil
}

func (c *Client) GetOAuth2ClientWebhookCallbacks(ctx context.Context, clientIDs []string) ([]OAuth2ClientWebhookCallback, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresql client is nil")
	}
	normalized := make([]string, 0, len(clientIDs))
	seen := make(map[string]struct{}, len(clientIDs))
	for _, raw := range clientIDs {
		clientID := strings.TrimSpace(raw)
		if clientID == "" {
			continue
		}
		if _, ok := seen[clientID]; ok {
			continue
		}
		seen[clientID] = struct{}{}
		normalized = append(normalized, clientID)
	}
	if len(normalized) == 0 {
		return nil, nil
	}

	rows, err := c.OAuth2ClientWebhookEndpoint.Query().
		Where(
			oauth2clientwebhookendpoint.ClientIDIn(normalized...),
			oauth2clientwebhookendpoint.EnabledEQ(true),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}

	callbacks := make([]OAuth2ClientWebhookCallback, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		callbackURL := strings.TrimSpace(row.CallbackURL)
		if callbackURL == "" {
			continue
		}
		bearer := ""
		if row.Bearer != nil {
			bearer = strings.TrimSpace(*row.Bearer)
		}
		callbacks = append(callbacks, OAuth2ClientWebhookCallback{
			ClientID:    strings.TrimSpace(row.ClientID),
			CallbackURL: callbackURL,
			Bearer:      bearer,
		})
	}
	return callbacks, nil
}
