package usersponsor

import (
	"context"
	"errors"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

var errBindingConflict = errors.New("afdian binding conflict")

// Before the method field was introduced, OAuth was the only binding path.
func hasAfdianOAuthProof(u *postgresql.User) bool {
	return u != nil && u.AfdianUserID != nil && *u.AfdianUserID != "" &&
		(u.AfdianVerificationMethod == nil || *u.AfdianVerificationMethod == user.AfdianVerificationMethodOauth2)
}

func (h *handlers) bindIdentity(ctx context.Context, uid, afdianID string) error {
	n, err := h.api.DBManager.DB.User.Update().Where(user.IDEQ(uid),
		user.Or(user.AfdianUserIDIsNil(), user.AfdianUserIDEQ(afdianID))).
		SetAfdianUserID(afdianID).SetAfdianBoundAt(time.Now().UTC()).
		SetAfdianVerificationMethod(user.AfdianVerificationMethodOauth2).Save(ctx)
	if postgresql.IsConstraintError(err) {
		return errBindingConflict
	}
	if err != nil {
		return err
	}
	if n != 1 {
		return errBindingConflict
	}
	return nil
}
