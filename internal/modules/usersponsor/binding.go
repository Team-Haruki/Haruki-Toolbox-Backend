package usersponsor

import (
	"context"
	"errors"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

var errBindingConflict = errors.New("afdian binding conflict")

// Both proofs share the same unique identity claim. Neither another order
// from the same sponsor nor another verification method bypasses ownership.
func (h *handlers) bindIdentity(ctx context.Context, uid, afdianID, method string) error {
	update := h.api.DBManager.DB.User.Update().Where(user.IDEQ(uid),
		user.Or(user.AfdianUserIDIsNil(), user.AfdianUserIDEQ(afdianID)))
	if method == "order" {
		// Do not downgrade an OAuth proof, including pre-method-column links.
		update.Where(user.Or(user.AfdianUserIDIsNil(), user.AfdianVerificationMethodEQ("order")))
	}
	n, err := update.SetAfdianUserID(afdianID).SetAfdianBoundAt(time.Now().UTC()).
		SetAfdianVerificationMethod(user.AfdianVerificationMethod(method)).Save(ctx)
	if postgresql.IsConstraintError(err) {
		return errBindingConflict
	}
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	if method == "order" {
		matches, err := h.api.DBManager.DB.User.Query().Where(user.IDEQ(uid), user.AfdianUserIDEQ(afdianID)).Exist(ctx)
		if err != nil {
			return err
		}
		if matches {
			return nil
		}
	}
	return errBindingConflict
}
