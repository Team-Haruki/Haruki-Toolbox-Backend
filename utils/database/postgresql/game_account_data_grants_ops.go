package postgresql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountdatagrant"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

type GameAccountDataAccess struct {
	Allowed     bool
	OwnerUserID string
	ViaGrant    bool
	GrantID     int
	ExpiresAt   *time.Time
}

type GameAccountDataGrantRecord struct {
	ID            int       `json:"id"`
	Permissions   []string  `json:"permissions"`
	OwnerUserID   string    `json:"ownerUserId"`
	GranteeUserID string    `json:"granteeUserId"`
	Server        string    `json:"server"`
	GameUserID    string    `json:"gameUserId"`
	DataType      string    `json:"dataType"`
	ExpiresAt     time.Time `json:"expiresAt"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

var (
	ErrGameAccountDataGrantOwnerNotFound   = errors.New("game account data grant owner user not found")
	ErrGameAccountDataGrantGranteeNotFound = errors.New("game account data grant grantee user not found")
)

// IsGrantableGameAccountDataType reports whether an owner may share one data
// type with another Toolbox user.
//
// profile is grantable even though it is not stored data: it is proxied live
// from the game API and is the least sensitive of the three — it is what other
// players already see in game, whereas suite is the full save. Granting it lets
// the grantee's requests reach the upstream API on the owner's account, which is
// a cost the owner takes on knowingly by granting.
func IsGrantableGameAccountDataType(dataType string) bool {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "suite", "mysekai", "profile":
		return true
	default:
		return false
	}
}

func normalizeGameAccountDataGrantIdentity(ownerUserID, granteeUserID, server, gameUserID, dataType string) (string, string, string, string, string, error) {
	ownerUserID = strings.TrimSpace(ownerUserID)
	granteeUserID = strings.TrimSpace(granteeUserID)
	server = strings.TrimSpace(server)
	gameUserID = strings.TrimSpace(gameUserID)
	dataType = strings.ToLower(strings.TrimSpace(dataType))
	switch {
	case ownerUserID == "":
		return "", "", "", "", "", fmt.Errorf("owner user id is required")
	case granteeUserID == "":
		return "", "", "", "", "", fmt.Errorf("grantee user id is required")
	case server == "":
		return "", "", "", "", "", fmt.Errorf("server is required")
	case gameUserID == "":
		return "", "", "", "", "", fmt.Errorf("game user id is required")
	case !IsGrantableGameAccountDataType(dataType):
		return "", "", "", "", "", fmt.Errorf("data type is not grantable")
	}
	return ownerUserID, granteeUserID, server, gameUserID, dataType, nil
}

func (c *Client) canonicalGameAccountDataGrantUserID(ctx context.Context, userID string, missingErr error) (string, error) {
	row, err := c.User.Query().
		Where(userSchema.IDEQ(userID)).
		Select(userSchema.FieldID).
		Only(ctx)
	if err != nil {
		if IsNotFound(err) {
			return "", missingErr
		}
		return "", err
	}
	return row.ID, nil
}

func buildGameAccountDataGrantRecord(row *GameAccountDataGrant) GameAccountDataGrantRecord {
	if row == nil {
		return GameAccountDataGrantRecord{}
	}
	return GameAccountDataGrantRecord{
		ID:            row.ID,
		Permissions:   GrantPermissions(row.CanRead, row.CanWrite),
		OwnerUserID:   row.OwnerUserID,
		GranteeUserID: row.GranteeUserID,
		Server:        row.Server,
		GameUserID:    row.GameUserID,
		DataType:      row.DataType,
		ExpiresAt:     row.ExpiresAt.UTC(),
		CreatedAt:     row.CreatedAt.UTC(),
		UpdatedAt:     row.UpdatedAt.UTC(),
	}
}

func (c *Client) CanAccessGameAccountData(ctx context.Context, requesterUserID, server, gameUserID, dataType string, now time.Time) (*GameAccountDataAccess, error) {
	return c.gameAccountDataAccess(ctx, requesterUserID, server, gameUserID, dataType, now, false)
}

func (c *Client) CanWriteGameAccountData(ctx context.Context, requesterUserID, server, gameUserID, dataType string, now time.Time) (*GameAccountDataAccess, error) {
	return c.gameAccountDataAccess(ctx, requesterUserID, server, gameUserID, dataType, now, true)
}

func (c *Client) gameAccountDataAccess(ctx context.Context, requesterUserID, server, gameUserID, dataType string, now time.Time, write bool) (*GameAccountDataAccess, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresql client is nil")
	}
	requesterUserID = strings.TrimSpace(requesterUserID)
	server = strings.TrimSpace(server)
	gameUserID = strings.TrimSpace(gameUserID)
	dataType = strings.ToLower(strings.TrimSpace(dataType))
	if write && dataType == "mysekai_birthday_party" {
		dataType = "mysekai"
	}
	if requesterUserID == "" || server == "" || gameUserID == "" || dataType == "" {
		return &GameAccountDataAccess{}, nil
	}

	binding, err := c.GameAccountBinding.Query().
		Where(
			gameaccountbinding.ServerEQ(server),
			gameaccountbinding.GameUserIDEQ(gameUserID),
			gameaccountbinding.VerifiedEQ(true),
		).
		WithUser(func(q *UserQuery) {
			q.Select(userSchema.FieldID, userSchema.FieldBanned)
		}).
		Only(ctx)
	if err != nil {
		if IsNotFound(err) {
			return &GameAccountDataAccess{}, nil
		}
		return nil, err
	}
	if binding == nil || binding.Edges.User == nil {
		return &GameAccountDataAccess{}, nil
	}

	ownerUser := binding.Edges.User
	access := &GameAccountDataAccess{OwnerUserID: strings.TrimSpace(ownerUser.ID)}
	if write && (ownerUser.Banned || (dataType != "suite" && dataType != "mysekai")) {
		return access, nil
	}
	if access.OwnerUserID == requesterUserID {
		access.Allowed = true
		return access, nil
	}
	if ownerUser.Banned || !IsGrantableGameAccountDataType(dataType) {
		return access, nil
	}

	query := c.GameAccountDataGrant.Query().
		Where(
			gameaccountdatagrant.OwnerUserIDEQ(access.OwnerUserID),
			gameaccountdatagrant.GranteeUserIDEQ(requesterUserID),
			gameaccountdatagrant.ServerEQ(server),
			gameaccountdatagrant.GameUserIDEQ(gameUserID),
			gameaccountdatagrant.DataTypeEQ(dataType),
			gameaccountdatagrant.ExpiresAtGT(now),
			gameaccountdatagrant.HasGranteeWith(userSchema.BannedEQ(false)),
		)
	if write {
		query.Where(gameaccountdatagrant.CanWriteEQ(true))
	} else {
		query.Where(gameaccountdatagrant.CanReadEQ(true))
	}
	grant, err := query.Only(ctx)
	if err != nil {
		if IsNotFound(err) {
			return access, nil
		}
		return nil, err
	}
	expiresAt := grant.ExpiresAt.UTC()
	access.Allowed = true
	access.ViaGrant = true
	access.GrantID = grant.ID
	access.ExpiresAt = &expiresAt
	return access, nil
}

func (c *Client) UpsertGameAccountDataGrant(ctx context.Context, ownerUserID, granteeUserID, server, gameUserID, dataType string, expiresAt time.Time, permissions ...[]string) (*GameAccountDataGrant, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresql client is nil")
	}
	ownerUserID, granteeUserID, server, gameUserID, dataType, err := normalizeGameAccountDataGrantIdentity(ownerUserID, granteeUserID, server, gameUserID, dataType)
	if err != nil {
		return nil, err
	}
	ownerUserID, err = c.canonicalGameAccountDataGrantUserID(ctx, ownerUserID, ErrGameAccountDataGrantOwnerNotFound)
	if err != nil {
		return nil, err
	}
	granteeUserID, err = c.canonicalGameAccountDataGrantUserID(ctx, granteeUserID, ErrGameAccountDataGrantGranteeNotFound)
	if err != nil {
		return nil, err
	}
	existing, err := c.GameAccountDataGrant.Query().
		Where(
			gameaccountdatagrant.OwnerUserIDEQ(ownerUserID),
			gameaccountdatagrant.GranteeUserIDEQ(granteeUserID),
			gameaccountdatagrant.ServerEQ(server),
			gameaccountdatagrant.GameUserIDEQ(gameUserID),
			gameaccountdatagrant.DataTypeEQ(dataType),
		).
		Only(ctx)
	if err != nil && !IsNotFound(err) {
		return nil, err
	}
	canRead, canWrite := true, false
	if existing != nil {
		canRead, canWrite = existing.CanRead, existing.CanWrite
	}
	if len(permissions) > 0 {
		var err error
		canRead, canWrite, err = ParseGrantPermissions(dataType, permissions[0])
		if err != nil {
			return nil, err
		}
	}
	if existing != nil {
		update := existing.Update().SetExpiresAt(expiresAt)
		if len(permissions) > 0 {
			update.SetCanRead(canRead).SetCanWrite(canWrite)
		}
		return update.Save(ctx)
	}
	return c.GameAccountDataGrant.Create().SetCanRead(canRead).SetCanWrite(canWrite).
		SetOwnerUserID(ownerUserID).
		SetGranteeUserID(granteeUserID).
		SetServer(server).
		SetGameUserID(gameUserID).
		SetDataType(dataType).
		SetExpiresAt(expiresAt).
		Save(ctx)
}

func (c *Client) DeleteGameAccountDataGrant(ctx context.Context, ownerUserID, granteeUserID, server, gameUserID, dataType string) (int, error) {
	if c == nil {
		return 0, fmt.Errorf("postgresql client is nil")
	}
	ownerUserID, granteeUserID, server, gameUserID, dataType, err := normalizeGameAccountDataGrantIdentity(ownerUserID, granteeUserID, server, gameUserID, dataType)
	if err != nil {
		return 0, err
	}
	return c.GameAccountDataGrant.Delete().
		Where(
			gameaccountdatagrant.OwnerUserIDEQ(ownerUserID),
			gameaccountdatagrant.GranteeUserIDEQ(granteeUserID),
			gameaccountdatagrant.ServerEQ(server),
			gameaccountdatagrant.GameUserIDEQ(gameUserID),
			gameaccountdatagrant.DataTypeEQ(dataType),
		).
		Exec(ctx)
}

func (c *Client) ListOwnedGameAccountDataGrants(ctx context.Context, ownerUserID string, now time.Time) ([]GameAccountDataGrantRecord, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresql client is nil")
	}
	rows, err := c.GameAccountDataGrant.Query().
		Where(
			gameaccountdatagrant.OwnerUserIDEQ(strings.TrimSpace(ownerUserID)),
			gameaccountdatagrant.ExpiresAtGT(now),
		).
		Order(gameaccountdatagrant.ByExpiresAt(), gameaccountdatagrant.ByID()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]GameAccountDataGrantRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, buildGameAccountDataGrantRecord(row))
	}
	return records, nil
}

func (c *Client) ListReceivedGameAccountDataGrants(ctx context.Context, granteeUserID string, now time.Time) ([]GameAccountDataGrantRecord, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresql client is nil")
	}
	rows, err := c.GameAccountDataGrant.Query().
		Where(
			gameaccountdatagrant.GranteeUserIDEQ(strings.TrimSpace(granteeUserID)),
			gameaccountdatagrant.ExpiresAtGT(now),
		).
		Order(gameaccountdatagrant.ByExpiresAt(), gameaccountdatagrant.ByID()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]GameAccountDataGrantRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, buildGameAccountDataGrantRecord(row))
	}
	return records, nil
}

func (c *Client) CleanupExpiredGameAccountDataGrants(ctx context.Context, now time.Time) (int, error) {
	if c == nil {
		return 0, fmt.Errorf("postgresql client is nil")
	}
	return c.GameAccountDataGrant.Delete().
		Where(gameaccountdatagrant.ExpiresAtLTE(now)).
		Exec(ctx)
}

func GrantPermissions(read, write bool) []string {
	p := []string{}
	if read {
		p = append(p, "read")
	}
	if write {
		p = append(p, "write")
	}
	return p
}
func ParseGrantPermissions(dataType string, permissions []string) (bool, bool, error) {
	var read, write bool
	for _, p := range permissions {
		switch p {
		case "read":
			read = true
		case "write":
			write = true
		default:
			return false, false, fmt.Errorf("invalid permission")
		}
	}
	if !read && !write || write && dataType != "suite" && dataType != "mysekai" {
		return false, false, fmt.Errorf("invalid permissions for data type")
	}
	return read, write, nil
}
