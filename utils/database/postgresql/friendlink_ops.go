package postgresql

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"entgo.io/ent/dialect/sql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/friendlink"
)

const (
	DefaultFriendLinkPageSize = 100
	MaxFriendLinkPageSize     = 200
	MaxFriendLinkTags         = 16
	MaxFriendLinkTagBytes     = 128
	maxFriendLinkTagsJSON     = 4096
)

func ValidateFriendLinkTags(tags []string) error {
	if len(tags) > MaxFriendLinkTags {
		return fmt.Errorf("friend link tags exceed %d entries", MaxFriendLinkTags)
	}
	for _, tag := range tags {
		if len(tag) > MaxFriendLinkTagBytes || !utf8.ValidString(tag) || strings.ContainsRune(tag, 0) {
			return fmt.Errorf("friend link tag must be valid text of at most %d bytes", MaxFriendLinkTagBytes)
		}
	}
	encoded, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	// jsonb renders a space after each array separator in its text form.
	if len(encoded)+max(0, len(tags)-1) > maxFriendLinkTagsJSON {
		return fmt.Errorf("friend link tags exceed %d encoded bytes", maxFriendLinkTagsJSON)
	}
	return nil
}

// ReadFriendLinkPage bounds both the page and legacy tag bytes before decoding.
// Legacy links with oversized tags remain visible and editable with empty tags.
func (c *Client) ReadFriendLinkPage(ctx context.Context, page, pageSize int) ([]*FriendLink, bool, error) {
	if page < 1 || pageSize < 1 || pageSize > MaxFriendLinkPageSize {
		return nil, false, fmt.Errorf("invalid friend link page")
	}
	if page-1 > math.MaxInt/pageSize {
		return []*FriendLink{}, false, nil
	}
	rows, err := c.FriendLink.Query().
		Select(friendlink.FieldID, friendlink.FieldName, friendlink.FieldDescription, friendlink.FieldAvatar, friendlink.FieldURL, friendlink.FieldSortOrder).
		Order(friendlink.BySortOrder(sql.OrderAsc()), friendlink.ByID(sql.OrderAsc())).
		Offset((page - 1) * pageSize).Limit(pageSize + 1).All(ctx)
	if err != nil || len(rows) == 0 {
		return rows, false, err
	}
	hasMore := len(rows) > pageSize
	if hasMore {
		rows = rows[:pageSize]
	}
	byID := make(map[int]*FriendLink, len(rows))
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
		ids = append(ids, row.ID)
	}
	tags, err := c.FriendLink.Query().Where(friendlink.IDIn(ids...), func(s *sql.Selector) {
		s.Where(sql.ExprP(octetLengthSQL(s, friendlink.FieldTags)+" <= ?", maxFriendLinkTagsJSON))
	}).Select(friendlink.FieldID, friendlink.FieldTags).All(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, row := range tags {
		if ValidateFriendLinkTags(row.Tags) == nil {
			byID[row.ID].Tags = row.Tags
		}
	}
	return rows, hasMore, nil
}
