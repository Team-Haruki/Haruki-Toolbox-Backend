package postgresql

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"haruki-suite/utils/database/postgresql/webhookendpoint"
	"haruki-suite/utils/database/postgresql/webhooksubscription"
)

type WebhookEndpointRecord struct {
	ID          string
	Credential  string
	CallbackURL string
	Bearer      *string
	Enabled     bool
}

type WebhookCallback struct {
	CallbackURL string
	Bearer      string
}

type WebhookSubscriber struct {
	UID    string `json:"uid"`
	Server string `json:"server"`
	Type   string `json:"type"`
}

func normalizeOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizeWebhookEndpointRecord(record WebhookEndpointRecord) (WebhookEndpointRecord, error) {
	record.ID = strings.TrimSpace(record.ID)
	record.Credential = strings.TrimSpace(record.Credential)
	record.CallbackURL = strings.TrimSpace(record.CallbackURL)
	record.Bearer = normalizeOptionalString(record.Bearer)
	if record.ID == "" {
		return WebhookEndpointRecord{}, fmt.Errorf("webhook id is required")
	}
	if record.Credential == "" {
		return WebhookEndpointRecord{}, fmt.Errorf("webhook credential is required")
	}
	if record.CallbackURL == "" {
		return WebhookEndpointRecord{}, fmt.Errorf("webhook callback_url is required")
	}
	return record, nil
}

func normalizeWebhookSubscriptionInput(userID, server, dataType, webhookID string) (string, string, string, string, error) {
	userID = strings.TrimSpace(userID)
	server = strings.TrimSpace(server)
	dataType = strings.TrimSpace(dataType)
	webhookID = strings.TrimSpace(webhookID)
	switch {
	case userID == "":
		return "", "", "", "", fmt.Errorf("webhook user_id is required")
	case server == "":
		return "", "", "", "", fmt.Errorf("webhook server is required")
	case dataType == "":
		return "", "", "", "", fmt.Errorf("webhook data_type is required")
	case webhookID == "":
		return "", "", "", "", fmt.Errorf("webhook id is required")
	default:
		return userID, server, dataType, webhookID, nil
	}
}

func (c *Client) GetWebhookUser(ctx context.Context, id, credential string) (*WebhookEndpointRecord, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresql client is nil")
	}
	id = strings.TrimSpace(id)
	credential = strings.TrimSpace(credential)
	if id == "" || credential == "" {
		return nil, nil
	}

	record, err := c.WebhookEndpoint.Query().
		Where(
			webhookendpoint.IDEQ(id),
			webhookendpoint.CredentialEQ(credential),
		).
		Only(ctx)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &WebhookEndpointRecord{
		ID:          record.ID,
		Credential:  record.Credential,
		CallbackURL: record.CallbackURL,
		Bearer:      normalizeOptionalString(record.Bearer),
		Enabled:     record.Enabled,
	}, nil
}

func (c *Client) GetWebhookPushAPI(ctx context.Context, userID int64, server, dataType string) ([]WebhookCallback, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresql client is nil")
	}

	subscriptions, err := c.WebhookSubscription.Query().
		Where(
			webhooksubscription.UserIDEQ(strconv.FormatInt(userID, 10)),
			webhooksubscription.ServerEQ(strings.TrimSpace(server)),
			webhooksubscription.DataTypeEQ(strings.TrimSpace(dataType)),
			webhooksubscription.HasEndpointWith(webhookendpoint.EnabledEQ(true)),
		).
		WithEndpoint().
		All(ctx)
	if err != nil {
		return nil, err
	}

	callbacks := make([]WebhookCallback, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		if subscription == nil || subscription.Edges.Endpoint == nil {
			continue
		}
		callbackURL := strings.TrimSpace(subscription.Edges.Endpoint.CallbackURL)
		if callbackURL == "" {
			continue
		}
		bearer := ""
		if subscription.Edges.Endpoint.Bearer != nil {
			bearer = strings.TrimSpace(*subscription.Edges.Endpoint.Bearer)
		}
		callbacks = append(callbacks, WebhookCallback{
			CallbackURL: callbackURL,
			Bearer:      bearer,
		})
	}
	return callbacks, nil
}

func (c *Client) AddWebhookPushUser(ctx context.Context, userID, server, dataType, webhookID string) error {
	if c == nil {
		return fmt.Errorf("postgresql client is nil")
	}

	userID, server, dataType, webhookID, err := normalizeWebhookSubscriptionInput(userID, server, dataType, webhookID)
	if err != nil {
		return err
	}

	exists, err := c.WebhookEndpoint.Query().Where(webhookendpoint.IDEQ(webhookID)).Exist(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("webhook endpoint %q not found", webhookID)
	}

	existing, err := c.WebhookSubscription.Query().
		Where(
			webhooksubscription.UserIDEQ(userID),
			webhooksubscription.ServerEQ(server),
			webhooksubscription.DataTypeEQ(dataType),
			webhooksubscription.WebhookIDEQ(webhookID),
		).
		Exist(ctx)
	if err != nil {
		return err
	}
	if existing {
		return nil
	}

	_, err = c.WebhookSubscription.Create().
		SetUserID(userID).
		SetServer(server).
		SetDataType(dataType).
		SetWebhookID(webhookID).
		Save(ctx)
	if IsConstraintError(err) {
		return nil
	}
	return err
}

func (c *Client) RemoveWebhookPushUser(ctx context.Context, userID, server, dataType, webhookID string) error {
	if c == nil {
		return fmt.Errorf("postgresql client is nil")
	}

	userID, server, dataType, webhookID, err := normalizeWebhookSubscriptionInput(userID, server, dataType, webhookID)
	if err != nil {
		return err
	}

	_, err = c.WebhookSubscription.Delete().
		Where(
			webhooksubscription.UserIDEQ(userID),
			webhooksubscription.ServerEQ(server),
			webhooksubscription.DataTypeEQ(dataType),
			webhooksubscription.WebhookIDEQ(webhookID),
		).
		Exec(ctx)
	return err
}

func (c *Client) GetWebhookSubscribers(ctx context.Context, webhookID string) ([]WebhookSubscriber, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresql client is nil")
	}

	webhookID = strings.TrimSpace(webhookID)
	if webhookID == "" {
		return []WebhookSubscriber{}, nil
	}

	subscriptions, err := c.WebhookSubscription.Query().
		Where(webhooksubscription.WebhookIDEQ(webhookID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]WebhookSubscriber, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		if subscription == nil {
			continue
		}
		result = append(result, WebhookSubscriber{
			UID:    subscription.UserID,
			Server: subscription.Server,
			Type:   subscription.DataType,
		})
	}
	return result, nil
}

func (c *Client) UpsertWebhookEndpoint(ctx context.Context, record WebhookEndpointRecord) error {
	if c == nil {
		return fmt.Errorf("postgresql client is nil")
	}

	record, err := normalizeWebhookEndpointRecord(record)
	if err != nil {
		return err
	}

	existing, err := c.WebhookEndpoint.Query().Where(webhookendpoint.IDEQ(record.ID)).Only(ctx)
	if IsNotFound(err) {
		create := c.WebhookEndpoint.Create().
			SetID(record.ID).
			SetCredential(record.Credential).
			SetCallbackURL(record.CallbackURL).
			SetEnabled(record.Enabled)
		if record.Bearer != nil {
			create.SetBearer(*record.Bearer)
		}
		_, err = create.Save(ctx)
		return err
	}
	if err != nil {
		return err
	}

	update := c.WebhookEndpoint.UpdateOneID(existing.ID).
		SetCredential(record.Credential).
		SetCallbackURL(record.CallbackURL).
		SetEnabled(record.Enabled)
	if record.Bearer != nil {
		update.SetBearer(*record.Bearer)
	} else {
		update.ClearBearer()
	}
	return update.Exec(ctx)
}

func (c *Client) DeleteAllWebhookData(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("postgresql client is nil")
	}
	if _, err := c.WebhookSubscription.Delete().Exec(ctx); err != nil {
		return err
	}
	if _, err := c.WebhookEndpoint.Delete().Exec(ctx); err != nil {
		return err
	}
	return nil
}
