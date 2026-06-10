package api

import (
	"context"
	platformIdentity "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/identity"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"strings"
)

func (s *SessionHandler) syncResolvedUserProfile(ctx context.Context, userID string, identityID string, email string, displayName *string) {
	if s == nil || s.DBClient == nil {
		return
	}

	userID = strings.TrimSpace(userID)
	if userID == "" {
		return
	}
	identityID = strings.TrimSpace(identityID)
	email = platformIdentity.NormalizeEmail(email)

	currentUser, err := s.DBClient.User.Query().
		Where(userSchema.IDEQ(userID)).
		Select(userSchema.FieldID, userSchema.FieldName, userSchema.FieldEmail, userSchema.FieldKratosIdentityID).
		Only(ctx)
	if err != nil {
		if !postgresql.IsNotFound(err) {
			harukiLogger.Warnf("Failed to query resolved user profile for sync: user=%s err=%v", userID, err)
		}
		return
	}

	update := s.DBClient.User.Update().Where(userSchema.IDEQ(userID))
	needsUpdate := false

	if identityID != "" {
		currentIdentityID := ""
		if currentUser.KratosIdentityID != nil {
			currentIdentityID = strings.TrimSpace(*currentUser.KratosIdentityID)
		}
		if currentIdentityID != identityID {
			update.SetKratosIdentityID(identityID)
			needsUpdate = true
		}
	}

	if email != "" && !strings.EqualFold(strings.TrimSpace(currentUser.Email), email) {
		update.SetEmail(email)
		needsUpdate = true
	}

	if displayName != nil {
		trimmedName := strings.TrimSpace(*displayName)
		if trimmedName != "" && strings.TrimSpace(currentUser.Name) != trimmedName {
			update.SetName(trimmedName)
			needsUpdate = true
		}
	}

	if !needsUpdate {
		return
	}

	if _, err := update.Save(ctx); err != nil {
		harukiLogger.Warnf("Failed to sync resolved user profile: user=%s identity=%s err=%v", userID, identityID, err)
	}
}
