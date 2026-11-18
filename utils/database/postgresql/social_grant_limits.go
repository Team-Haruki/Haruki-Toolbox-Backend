package postgresql

import (
	"context"
	"fmt"
	"unicode/utf8"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/authorizesocialplatforminfo"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/socialplatforminfo"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

const (
	MaxSocialGrantSlots        = 128
	MaxSocialGrantUserIDBytes  = 128
	MaxSocialGrantCommentBytes = 1024
)

func ValidSocialGrantFields(userID, comment string) bool {
	return len(userID) > 0 && len(userID) <= MaxSocialGrantUserIDBytes && len(comment) <= MaxSocialGrantCommentBytes && utf8.ValidString(userID) && utf8.ValidString(comment)
}

// BoundSocialGrants excludes legacy grants outside the supported slots or byte
// limits before rows are sent to the application. Deletion remains owner/slot
// scoped and can still remove an excluded legacy row.
func BoundSocialGrants(q *AuthorizeSocialPlatformInfoQuery) {
	q.Where(authorizesocialplatforminfo.PlatformIDGT(0), authorizesocialplatforminfo.PlatformIDLTE(MaxSocialGrantSlots), func(s *sql.Selector) {
		s.Where(sql.ExprP(fmt.Sprintf("%s BETWEEN 1 AND %d AND COALESCE(%s,0) <= %d AND %s <= 16",
			octetLengthSQL(s, authorizesocialplatforminfo.FieldPlatformUserID), MaxSocialGrantUserIDBytes,
			octetLengthSQL(s, authorizesocialplatforminfo.FieldComment), MaxSocialGrantCommentBytes,
			octetLengthSQL(s, authorizesocialplatforminfo.FieldPlatform))))
	})
}

func octetLengthSQL(s *sql.Selector, column string) string {
	if s.Dialect() == dialect.Postgres {
		return "octet_length(" + s.C(column) + "::text)"
	}
	return "length(CAST(" + s.C(column) + " AS BLOB))"
}

// ActiveSocialGrants applies the bounds used by lists and requires the
// delegating account to still have a verified social link.
func ActiveSocialGrants(q *AuthorizeSocialPlatformInfoQuery) {
	BoundSocialGrants(q)
	q.Where(authorizesocialplatforminfo.HasUserWith(user.HasSocialPlatformInfoWith(socialplatforminfo.Verified(true))))
}

// ClearSocialLinkAndGrants removes the primary link and any legacy or current
// delegations in one transaction. The caller holds the user's mutation lock.
func (c *Client) ClearSocialLinkAndGrants(ctx context.Context, userID string) (int, error) {
	tx, err := c.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	count, err := tx.SocialPlatformInfo.Delete().Where(socialplatforminfo.UserSocialPlatformInfoEQ(userID)).Exec(ctx)
	if err != nil {
		return 0, err
	}
	grants, err := tx.AuthorizeSocialPlatformInfo.Delete().Where(authorizesocialplatforminfo.UserIDEQ(userID)).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return count + grants, tx.Commit()
}
