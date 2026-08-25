package usergamebindings

import (
	"context"
	"sort"
	"time"

	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

// accessibleGameAccountsTimeout bounds the three PostgreSQL reads behind the
// aggregate; Fiber v3 request contexts carry no deadline of their own.
const accessibleGameAccountsTimeout = 3 * time.Second

const (
	accessibleGameAccountOwnershipOwn     = "own"
	accessibleGameAccountOwnershipGranted = "granted"
)

// gameAccountCapabilityRecommend is derived, not stored: it reports that
// recommend-data is callable for this account at all. Its dependency table is
// deckRecommendRequiredDataTypes, so the read path and this read model cannot
// drift. A client that wants the mysekai deck mode checks the "mysekai"
// capability alongside it, exactly as the read path does.
const gameAccountCapabilityRecommend = "recommend"

// accessibleGameAccountCapability describes one readable data type. An absent
// ExpiresAt means the access does not expire (the requester owns the account).
type accessibleGameAccountCapability struct {
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type accessibleGameAccountOwner struct {
	UserID     string  `json:"userId"`
	Name       string  `json:"name"`
	AvatarPath *string `json:"avatarPath"`
}

type accessibleGameAccountItem struct {
	Server       string                                     `json:"server"`
	GameUserID   string                                     `json:"gameUserId"`
	Ownership    string                                     `json:"ownership"`
	Verified     bool                                       `json:"verified"`
	IsDefault    bool                                       `json:"isDefault"`
	Capabilities map[string]accessibleGameAccountCapability `json:"capabilities"`
	Owner        *accessibleGameAccountOwner                `json:"owner"`
}

// grantedGameAccountAggregate pairs a granted account with the recency key it is
// ordered by, keeping the sort input out of the serialized response shape.
type grantedGameAccountAggregate struct {
	item      accessibleGameAccountItem
	grantedAt time.Time
}

type accessibleGameAccountListResponse struct {
	GeneratedAt time.Time                   `json:"generatedAt"`
	Total       int                         `json:"total"`
	Accounts    []accessibleGameAccountItem `json:"accounts"`
}

func handleListAccessibleGameAccounts(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), accessibleGameAccountsTimeout)
		defer cancel()

		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		now := gameAccountGrantNowUTC()
		accessible, err := apiHelper.DBManager.DB.ListAccessibleGameAccounts(ctx, userID, now)
		if err != nil {
			harukiLogger.Errorf("Failed to list accessible game accounts: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to list accessible game accounts")
		}

		accounts := buildAccessibleGameAccountItems(accessible)
		resp := accessibleGameAccountListResponse{
			GeneratedAt: now,
			Total:       len(accounts),
			Accounts:    accounts,
		}
		return harukiAPIHelper.SuccessResponse(c, "ok", &resp)
	}
}

// buildAccessibleGameAccountItems folds the read model into one entry per game
// account. Capabilities are the only gate a client needs: a data type appears
// only when the read path would accept it right now.
func buildAccessibleGameAccountItems(accessible *postgresql.AccessibleGameAccounts) []accessibleGameAccountItem {
	if accessible == nil {
		return []accessibleGameAccountItem{}
	}

	owned := make([]accessibleGameAccountItem, 0, len(accessible.Owned))
	for _, binding := range accessible.Owned {
		if binding == nil {
			continue
		}
		owned = append(owned, accessibleGameAccountItem{
			Server:     binding.Server,
			GameUserID: binding.GameUserID,
			Ownership:  accessibleGameAccountOwnershipOwn,
			Verified:   binding.Verified,
			IsDefault:  binding.IsDefault,
			// An unverified binding is unreadable even by its owner
			// (CanAccessGameAccountData requires verified), so it is listed with
			// no capabilities rather than hidden — the client can show it as
			// present but unusable.
			Capabilities: ownedGameAccountCapabilities(binding.Verified),
		})
	}
	sort.SliceStable(owned, func(i, j int) bool {
		if owned[i].IsDefault != owned[j].IsDefault {
			return owned[i].IsDefault
		}
		if owned[i].Server != owned[j].Server {
			return owned[i].Server < owned[j].Server
		}
		return owned[i].GameUserID < owned[j].GameUserID
	})

	grantedIndex := make(map[accessibleGameAccountKey]int, len(accessible.Grants))
	aggregates := make([]grantedGameAccountAggregate, 0, len(accessible.Grants))
	for _, grant := range accessible.Grants {
		key := accessibleGameAccountKey{server: grant.Server, gameUserID: grant.GameUserID}
		idx, ok := grantedIndex[key]
		if !ok {
			owner := grant.Owner
			aggregates = append(aggregates, grantedGameAccountAggregate{
				item: accessibleGameAccountItem{
					Server:       grant.Server,
					GameUserID:   grant.GameUserID,
					Ownership:    accessibleGameAccountOwnershipGranted,
					Verified:     true,
					IsDefault:    false,
					Capabilities: map[string]accessibleGameAccountCapability{},
					Owner: &accessibleGameAccountOwner{
						UserID:     owner.UserID,
						Name:       owner.Name,
						AvatarPath: owner.AvatarPath,
					},
				},
			})
			idx = len(aggregates) - 1
			grantedIndex[key] = idx
		}
		expiresAt := grant.ExpiresAt
		aggregates[idx].item.Capabilities[grant.DataType] = accessibleGameAccountCapability{ExpiresAt: &expiresAt}
		if grant.GrantedAt.After(aggregates[idx].grantedAt) {
			aggregates[idx].grantedAt = grant.GrantedAt
		}
	}
	sort.SliceStable(aggregates, func(i, j int) bool {
		if !aggregates[i].grantedAt.Equal(aggregates[j].grantedAt) {
			return aggregates[i].grantedAt.After(aggregates[j].grantedAt)
		}
		if aggregates[i].item.Server != aggregates[j].item.Server {
			return aggregates[i].item.Server < aggregates[j].item.Server
		}
		return aggregates[i].item.GameUserID < aggregates[j].item.GameUserID
	})

	granted := make([]accessibleGameAccountItem, 0, len(aggregates))
	for _, aggregate := range aggregates {
		applyDerivedGameAccountCapabilities(aggregate.item.Capabilities)
		granted = append(granted, aggregate.item)
	}

	return append(owned, granted...)
}

// ownedGameAccountCapabilities lists what the owner of a verified binding may
// read. profile stays owner-only (it is not grantable), and recommend is always
// available because the owner holds every data type it depends on.
func ownedGameAccountCapabilities(verified bool) map[string]accessibleGameAccountCapability {
	capabilities := map[string]accessibleGameAccountCapability{}
	if !verified {
		return capabilities
	}
	capabilities[string(ownedGameAccountDataTypeSuite)] = accessibleGameAccountCapability{}
	capabilities[string(ownedGameAccountDataTypeMysekai)] = accessibleGameAccountCapability{}
	capabilities[string(ownedGameAccountDataTypeProfile)] = accessibleGameAccountCapability{}
	capabilities[gameAccountCapabilityRecommend] = accessibleGameAccountCapability{}
	return capabilities
}

// applyDerivedGameAccountCapabilities adds capabilities that are not stored as
// grants but follow from the ones that are. recommend-data is derived from the
// data types its base mode reads, taken from the same table the handler uses, so
// the two cannot drift; it expires with the earliest of them. A client wanting
// the mysekai deck mode additionally checks the mysekai capability — precisely
// the extra data type that mode requires.
func applyDerivedGameAccountCapabilities(capabilities map[string]accessibleGameAccountCapability) {
	if capabilities == nil {
		return
	}
	var earliest *time.Time
	for _, required := range deckRecommendRequiredDataTypes(deckRecommendDataModeSuite) {
		capability, ok := capabilities[string(required)]
		if !ok {
			return
		}
		if capability.ExpiresAt != nil && (earliest == nil || capability.ExpiresAt.Before(*earliest)) {
			earliest = capability.ExpiresAt
		}
	}
	capabilities[gameAccountCapabilityRecommend] = accessibleGameAccountCapability{ExpiresAt: earliest}
}

// accessibleGameAccountKey identifies one game account across the two sources
// the aggregate merges.
type accessibleGameAccountKey struct {
	server     string
	gameUserID string
}
