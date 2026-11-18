package postgresql

import (
	"fmt"
	"unicode/utf8"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/authorizesocialplatforminfo"
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
		length := func(column string) string {
			if s.Dialect() == dialect.Postgres {
				return "octet_length(" + s.C(column) + ")"
			}
			return "length(CAST(" + s.C(column) + " AS BLOB))"
		}
		s.Where(sql.ExprP(fmt.Sprintf("%s BETWEEN 1 AND %d AND COALESCE(%s,0) <= %d AND %s <= 16",
			length(authorizesocialplatforminfo.FieldPlatformUserID), MaxSocialGrantUserIDBytes,
			length(authorizesocialplatforminfo.FieldComment), MaxSocialGrantCommentBytes,
			length(authorizesocialplatforminfo.FieldPlatform))))
	})
}
