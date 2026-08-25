package postgresql

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountdatagrant"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/predicate"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

// AccessibleGameAccountOwner carries the display identity of the user who owns a
// granted game account. Only fields the grantee already has a relationship with
// are selected — never email, role, or ban state.
type AccessibleGameAccountOwner struct {
	UserID     string
	Name       string
	AvatarPath *string
}

// AccessibleGameAccountGrant is one live grant the requester holds on another
// user's game account. Every record returned has already passed the same
// predicates CanAccessGameAccountData applies at read time (binding present and
// verified, binding owner not banned and still the granting owner, grantee not
// banned, grant unexpired), so a listed capability is one the read path accepts.
type AccessibleGameAccountGrant struct {
	Server     string
	GameUserID string
	DataType   string
	ExpiresAt  time.Time
	GrantedAt  time.Time
	Owner      AccessibleGameAccountOwner
}

// AccessibleGameAccounts is the read model behind the accessible-accounts
// endpoint: the requester's own bindings plus every account they can read via a
// live grant. Accounts the requester owns are never repeated in Grants.
type AccessibleGameAccounts struct {
	Owned  []*GameAccountBinding
	Grants []AccessibleGameAccountGrant
}

type accessibleGameAccountKey struct {
	server     string
	gameUserID string
}

// ListAccessibleGameAccounts resolves every game account the requester may read
// data for. It runs a bounded number of queries regardless of grant count: one
// for owned bindings, one for received grants (joining the owner's display
// fields), and one for the bindings backing those grants.
func (c *Client) ListAccessibleGameAccounts(ctx context.Context, requesterUserID string, now time.Time) (*AccessibleGameAccounts, error) {
	if c == nil {
		return nil, fmt.Errorf("postgresql client is nil")
	}
	requesterUserID = strings.TrimSpace(requesterUserID)
	result := &AccessibleGameAccounts{}
	if requesterUserID == "" {
		return result, nil
	}

	owned, err := c.GameAccountBinding.Query().
		Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(requesterUserID))).
		All(ctx)
	if err != nil {
		return nil, err
	}
	result.Owned = owned

	ownedKeys := make(map[accessibleGameAccountKey]struct{}, len(owned))
	for _, binding := range owned {
		ownedKeys[accessibleGameAccountKey{server: binding.Server, gameUserID: binding.GameUserID}] = struct{}{}
	}

	grants, err := c.GameAccountDataGrant.Query().
		Where(
			gameaccountdatagrant.GranteeUserIDEQ(requesterUserID),
			gameaccountdatagrant.ExpiresAtGT(now),
			gameaccountdatagrant.HasGranteeWith(userSchema.BannedEQ(false)),
		).
		WithOwner(func(q *UserQuery) {
			q.Select(userSchema.FieldID, userSchema.FieldName, userSchema.FieldAvatarPath)
		}).
		Order(gameaccountdatagrant.ByID()).
		All(ctx)
	if err != nil {
		return nil, err
	}

	candidates := make([]*GameAccountDataGrant, 0, len(grants))
	pending := make(map[accessibleGameAccountKey]struct{}, len(grants))
	for _, grant := range grants {
		if grant == nil || grant.Edges.Owner == nil {
			continue
		}
		if !IsGrantableGameAccountDataType(grant.DataType) {
			continue
		}
		key := accessibleGameAccountKey{server: grant.Server, gameUserID: grant.GameUserID}
		// An account the requester owns is reported as owned, never as granted.
		if _, ok := ownedKeys[key]; ok {
			continue
		}
		candidates = append(candidates, grant)
		pending[key] = struct{}{}
	}
	if len(candidates) == 0 {
		return result, nil
	}

	// The authoritative owner of a game account is the binding's user, not the
	// grant row's owner_user_id — the same resolution CanAccessGameAccountData
	// performs. A rebound account therefore drops out here even if a stale grant
	// row survived.
	bindingOwners, err := c.resolveVerifiedBindingOwners(ctx, pending)
	if err != nil {
		return nil, err
	}

	result.Grants = make([]AccessibleGameAccountGrant, 0, len(candidates))
	for _, grant := range candidates {
		key := accessibleGameAccountKey{server: grant.Server, gameUserID: grant.GameUserID}
		ownerID, ok := bindingOwners[key]
		if !ok || ownerID != strings.TrimSpace(grant.OwnerUserID) {
			continue
		}
		owner := grant.Edges.Owner
		result.Grants = append(result.Grants, AccessibleGameAccountGrant{
			Server:     grant.Server,
			GameUserID: grant.GameUserID,
			DataType:   strings.ToLower(strings.TrimSpace(grant.DataType)),
			ExpiresAt:  grant.ExpiresAt.UTC(),
			GrantedAt:  grant.UpdatedAt.UTC(),
			Owner: AccessibleGameAccountOwner{
				UserID:     strings.TrimSpace(owner.ID),
				Name:       owner.Name,
				AvatarPath: owner.AvatarPath,
			},
		})
	}
	return result, nil
}

// resolveVerifiedBindingOwners maps each (server, game user id) pair to the user
// id of its verified, non-banned owner. Pairs without such a binding are absent
// from the result.
func (c *Client) resolveVerifiedBindingOwners(ctx context.Context, keys map[accessibleGameAccountKey]struct{}) (map[accessibleGameAccountKey]string, error) {
	owners := make(map[accessibleGameAccountKey]string, len(keys))
	if len(keys) == 0 {
		return owners, nil
	}
	pairs := make([]accessibleGameAccountKey, 0, len(keys))
	for key := range keys {
		pairs = append(pairs, key)
	}
	// Deterministic predicate order keeps the emitted SQL stable across calls.
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].server != pairs[j].server {
			return pairs[i].server < pairs[j].server
		}
		return pairs[i].gameUserID < pairs[j].gameUserID
	})
	predicates := make([]predicate.GameAccountBinding, 0, len(pairs))
	for _, key := range pairs {
		predicates = append(predicates, gameaccountbinding.And(
			gameaccountbinding.ServerEQ(key.server),
			gameaccountbinding.GameUserIDEQ(key.gameUserID),
		))
	}

	rows, err := c.GameAccountBinding.Query().
		Where(
			gameaccountbinding.VerifiedEQ(true),
			gameaccountbinding.HasUserWith(userSchema.BannedEQ(false)),
			gameaccountbinding.Or(predicates...),
		).
		WithUser(func(q *UserQuery) {
			q.Select(userSchema.FieldID, userSchema.FieldBanned)
		}).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row == nil || row.Edges.User == nil {
			continue
		}
		ownerID := strings.TrimSpace(row.Edges.User.ID)
		if ownerID == "" {
			continue
		}
		owners[accessibleGameAccountKey{server: row.Server, gameUserID: row.GameUserID}] = ownerID
	}
	return owners, nil
}
