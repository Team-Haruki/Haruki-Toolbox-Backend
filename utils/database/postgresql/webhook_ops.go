package postgresql

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/webhookendpoint"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/webhooksubscription"
)

const (
	MaxWebhookSubscriptions      = 5000
	DefaultWebhookSubscriberPage = 200
	MaxWebhookSubscriberPage     = 500
)

var (
	ErrWebhookSubscriptionLimit = errors.New("webhook endpoint subscription limit reached")
	ErrInvalidWebhookGameUserID = errors.New("game user ID must be a positive 64-bit integer")
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

func NormalizeWebhookGameUserID(raw string) (string, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return "", ErrInvalidWebhookGameUserID
	}
	return strconv.FormatInt(id, 10), nil
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
	userID, err = NormalizeWebhookGameUserID(userID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Keep burst traffic for one endpoint waiting outside the shared DB pool.
	// The endpoint row then serializes mutations across replicas.
	release, err := lockLocalMutations(ctx, []string{"webhook-subscriptions:" + webhookID})
	if err != nil {
		return err
	}
	defer release()

	// A writer waiting on the endpoint lock must see subscriptions committed
	// by the previous writer when it checks the quota in the next statement.
	opts := &stdsql.TxOptions{}
	if c.driver.Dialect() == dialect.Postgres {
		opts.Isolation = stdsql.LevelReadCommitted
	}
	tx, err := c.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.WebhookEndpoint.Query().Where(webhookendpoint.IDEQ(webhookID), func(s *sql.Selector) {
		if s.Dialect() == dialect.Postgres {
			s.ForUpdate()
		}
	}).OnlyID(ctx); err != nil {
		return err
	}

	subscription := tx.WebhookSubscription.Query().Where(
		webhooksubscription.ServerEQ(server),
		webhooksubscription.DataTypeEQ(dataType),
		webhooksubscription.WebhookIDEQ(webhookID),
	)
	existing, err := subscription.Clone().Where(webhooksubscription.UserIDEQ(userID)).Exist(ctx)
	if err != nil {
		return err
	}
	if existing {
		return nil
	}
	// Repair an existing legacy subscription in place before applying the
	// quota. Callback delivery uses canonical IDs.
	aliasID, err := subscription.Where(matchesLegacyWebhookUserID(userID)).
		Order(webhooksubscription.ByID()).FirstID(ctx)
	if err == nil {
		if err := tx.WebhookSubscription.UpdateOneID(aliasID).SetUserID(userID).Exec(ctx); err == nil {
			return tx.Commit()
		} else if !IsNotFound(err) {
			return err
		}
	} else if !IsNotFound(err) {
		return err
	}
	last, err := tx.WebhookSubscription.Query().Where(webhooksubscription.WebhookIDEQ(webhookID)).
		Offset(MaxWebhookSubscriptions - 1).Limit(1).IDs(ctx)
	if err != nil {
		return err
	}
	if len(last) != 0 {
		return ErrWebhookSubscriptionLimit
	}

	_, err = tx.WebhookSubscription.Create().
		SetUserID(userID).
		SetServer(server).
		SetDataType(dataType).
		SetWebhookID(webhookID).
		Save(ctx)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func matchesLegacyWebhookUserID(canonical string) func(*sql.Selector) {
	return func(s *sql.Selector) {
		column := s.C(webhooksubscription.FieldUserID)
		// Old writes trimmed surrounding whitespace. Remove at most one plus
		// sign and leading zeros without casting unrelated legacy text.
		s.Where(sql.P(func(b *sql.Builder) {
			b.WriteString("LTRIM(CASE WHEN SUBSTR(").WriteString(column).
				WriteString(", 1, 1) = '+' THEN SUBSTR(").WriteString(column).
				WriteString(", 2) ELSE ").WriteString(column).
				WriteString(" END, '0') = ").Arg(canonical)
		}))
	}
}

func (c *Client) RemoveWebhookPushUser(ctx context.Context, userID, server, dataType, webhookID string) error {
	if c == nil {
		return fmt.Errorf("postgresql client is nil")
	}

	userID, server, dataType, webhookID, err := normalizeWebhookSubscriptionInput(userID, server, dataType, webhookID)
	if err != nil {
		return err
	}

	ids := []string{userID}
	if canonical, err := NormalizeWebhookGameUserID(userID); err == nil && canonical != userID {
		ids = append(ids, canonical)
	}
	_, err = c.WebhookSubscription.Delete().
		Where(
			webhooksubscription.UserIDIn(ids...),
			webhooksubscription.ServerEQ(server),
			webhooksubscription.DataTypeEQ(dataType),
			webhooksubscription.WebhookIDEQ(webhookID),
		).
		Exec(ctx)
	return err
}

func (c *Client) GetWebhookSubscribers(ctx context.Context, webhookID string, beforeID, pageSize int) ([]WebhookSubscriber, int, error) {
	if c == nil {
		return nil, 0, fmt.Errorf("postgresql client is nil")
	}

	webhookID = strings.TrimSpace(webhookID)
	if webhookID == "" {
		return []WebhookSubscriber{}, 0, nil
	}

	query := c.WebhookSubscription.Query().Where(webhooksubscription.WebhookIDEQ(webhookID))
	subscriptions, nextBeforeID, err := ReadWebhookSubscriberPage(ctx, query, beforeID, pageSize)
	if err != nil {
		return nil, 0, err
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
	return result, nextBeforeID, nil
}

// ReadWebhookSubscriberPage keeps paging scoped by the caller's endpoint and
// audience predicates. Legacy identifiers exceeding supported field sizes are
// excluded before retrieving their payload, and can still be removed.
func ReadWebhookSubscriberPage(ctx context.Context, query *WebhookSubscriptionQuery, beforeID, pageSize int) ([]*WebhookSubscription, int, error) {
	if pageSize <= 0 || pageSize > MaxWebhookSubscriberPage {
		return nil, 0, fmt.Errorf("invalid webhook subscriber page size")
	}
	if beforeID > 0 {
		query.Where(webhooksubscription.IDLT(beforeID))
	}
	rows, err := query.Where(func(s *sql.Selector) {
		s.Where(sql.ExprP(octetLengthSQL(s, webhooksubscription.FieldUserID) + " <= 128 AND " +
			octetLengthSQL(s, webhooksubscription.FieldServer) + " <= 16 AND " +
			octetLengthSQL(s, webhooksubscription.FieldDataType) + " <= 16"))
	}).Order(webhooksubscription.ByID(sql.OrderDesc())).Limit(pageSize + 1).All(ctx)
	if err != nil || len(rows) <= pageSize {
		return rows, 0, err
	}
	return rows[:pageSize], rows[pageSize-1].ID, nil
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
