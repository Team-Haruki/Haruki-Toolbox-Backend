package api

import (
	"context"
	"crypto/rand"
	"fmt"
	platformIdentity "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/identity"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"math/big"
	"strings"
	"time"
)

func extractKratosIdentityName(identity kratosIdentityRecord) string {
	return extractNameFromTraitValue(identity.Traits)
}

func extractNameFromTraitValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if direct, ok := typed["name"].(string); ok {
			if name := strings.TrimSpace(direct); name != "" {
				return name
			}
		}
		for _, child := range typed {
			if name := extractNameFromTraitValue(child); name != "" {
				return name
			}
		}
	case []any:
		for _, child := range typed {
			if name := extractNameFromTraitValue(child); name != "" {
				return name
			}
		}
	}
	return ""
}

func extractKratosIdentityEmail(identity kratosIdentityRecord) string {
	if direct := extractEmailFromTraitValue(identity.Traits); direct != "" {
		return direct
	}
	for _, address := range identity.VerifiableAddresses {
		if value := strings.TrimSpace(address.Value); value != "" {
			return value
		}
	}
	return ""
}

func extractKratosIdentityEmailVerification(identity kratosIdentityRecord) *bool {
	traitsEmail := platformIdentity.NormalizeEmail(extractEmailFromTraitValue(identity.Traits))
	for _, address := range identity.VerifiableAddresses {
		addressEmail := platformIdentity.NormalizeEmail(address.Value)
		if addressEmail == "" {
			continue
		}
		if traitsEmail != "" && addressEmail != traitsEmail {
			continue
		}

		verified := address.Verified
		if !verified && strings.EqualFold(strings.TrimSpace(address.Status), "completed") {
			verified = true
		}
		return &verified
	}
	return nil
}

func extractEmailFromTraitValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if direct, ok := typed["email"].(string); ok {
			if email := strings.TrimSpace(direct); email != "" {
				return email
			}
		}
		for _, child := range typed {
			if email := extractEmailFromTraitValue(child); email != "" {
				return email
			}
		}
	case []any:
		for _, child := range typed {
			if email := extractEmailFromTraitValue(child); email != "" {
				return email
			}
		}
	case string:
		if email := strings.TrimSpace(typed); strings.Contains(email, "@") {
			return email
		}
	}
	return ""
}

func (s *SessionHandler) resolveKratosIdentity(ctx context.Context, identityID string, email string) (string, error) {
	if s.KratosIdentityResolver != nil {
		return s.KratosIdentityResolver(ctx, identityID, email)
	}
	if s.DBClient == nil {
		return "", fmt.Errorf("%w: database client is nil", errUserStoreUnavailable)
	}

	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return "", fmt.Errorf("%w: identity id is empty", errSessionUnauthorized)
	}

	matchedByIdentity, err := s.DBClient.User.Query().
		Where(userSchema.KratosIdentityIDEQ(identityID)).
		Select(userSchema.FieldID).
		Only(ctx)
	if err == nil {
		return matchedByIdentity.ID, nil
	}
	if err != nil && !postgresql.IsNotFound(err) {
		return "", fmt.Errorf("%w: query kratos identity map: %v", errUserStoreUnavailable, err)
	}

	if email == "" {
		return "", fmt.Errorf("%w: identity email is empty", errKratosIdentityUnmapped)
	}

	targetUser, err := s.DBClient.User.Query().
		Where(userSchema.EmailEqualFold(email)).
		Select(userSchema.FieldID, userSchema.FieldKratosIdentityID).
		Only(ctx)
	if err != nil {
		if postgresql.IsNotFound(err) {
			if !s.KratosAutoProvisionUser {
				return "", fmt.Errorf("%w: email is not linked", errKratosIdentityUnmapped)
			}
			provisionedUserID, provisionErr := s.createKratosProvisionedUser(ctx, identityID, email)
			if provisionErr == nil {
				return provisionedUserID, nil
			}
			matchedByIdentity, retryErr := s.DBClient.User.Query().
				Where(userSchema.KratosIdentityIDEQ(identityID)).
				Select(userSchema.FieldID).
				Only(ctx)
			if retryErr == nil {
				return matchedByIdentity.ID, nil
			}
			if retryErr != nil && !postgresql.IsNotFound(retryErr) {
				return "", fmt.Errorf("%w: re-query kratos identity map: %v", errUserStoreUnavailable, retryErr)
			}
			return "", provisionErr
		}
		return "", fmt.Errorf("%w: query user by email: %v", errUserStoreUnavailable, err)
	}

	if targetUser.KratosIdentityID != nil {
		boundIdentityID := strings.TrimSpace(*targetUser.KratosIdentityID)
		if boundIdentityID == identityID {
			return targetUser.ID, nil
		}
		if boundIdentityID != "" {
			return "", fmt.Errorf("%w: email already linked to another identity", errKratosIdentityUnmapped)
		}
	}
	if !s.KratosAutoLinkByEmail {
		return "", fmt.Errorf("%w: auto-link by email is disabled", errKratosIdentityUnmapped)
	}

	_, err = s.DBClient.User.Update().
		Where(userSchema.IDEQ(targetUser.ID)).
		SetKratosIdentityID(identityID).
		Save(ctx)
	if err != nil {
		if postgresql.IsConstraintError(err) {
			return "", fmt.Errorf("%w: identity already linked", errKratosIdentityUnmapped)
		}
		return "", fmt.Errorf("%w: bind identity to user: %v", errUserStoreUnavailable, err)
	}
	return targetUser.ID, nil
}

func (s *SessionHandler) createKratosProvisionedUser(ctx context.Context, identityID string, email string) (string, error) {
	if s.DBClient == nil {
		return "", fmt.Errorf("%w: database client is nil", errUserStoreUnavailable)
	}
	identityID = strings.TrimSpace(identityID)
	email = platformIdentity.NormalizeEmail(email)
	if identityID == "" || email == "" {
		return "", fmt.Errorf("%w: missing identity data", errKratosIdentityUnmapped)
	}

	for range kratosProvisionUserIDAttempts {
		uid, err := generateProvisionedUserID(time.Now().UTC())
		if err != nil {
			return "", fmt.Errorf("%w: generate user id: %v", errUserStoreUnavailable, err)
		}
		uploadCode, err := generateProvisionedUploadCode()
		if err != nil {
			return "", fmt.Errorf("%w: generate upload code: %v", errUserStoreUnavailable, err)
		}

		tx, err := s.DBClient.Tx(ctx)
		if err != nil {
			return "", fmt.Errorf("%w: start transaction: %v", errUserStoreUnavailable, err)
		}

		_, err = tx.User.Create().
			SetID(uid).
			SetName(deriveProvisionedUserName(email)).
			SetEmail(email).
			SetNillableAvatarPath(nil).
			SetKratosIdentityID(identityID).
			SetCreatedAt(time.Now().UTC()).
			Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			if postgresql.IsConstraintError(err) {
				continue
			}
			return "", fmt.Errorf("%w: create user: %v", errUserStoreUnavailable, err)
		}

		if _, err := tx.IOSScriptCode.Create().
			SetUserID(uid).
			SetUploadCode(uploadCode).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			if postgresql.IsConstraintError(err) {
				continue
			}
			return "", fmt.Errorf("%w: create iOS upload code: %v", errUserStoreUnavailable, err)
		}

		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			return "", fmt.Errorf("%w: commit transaction: %v", errUserStoreUnavailable, err)
		}
		return uid, nil
	}

	return "", fmt.Errorf("%w: exhausted retries while creating user", errUserStoreUnavailable)
}

func deriveProvisionedUserName(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return "kratos-user"
	}
	parts := strings.SplitN(email, "@", 2)
	candidate := strings.TrimSpace(parts[0])
	if candidate == "" {
		return email
	}
	return candidate
}

func generateProvisionedUploadCode() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", raw), nil
}

func generateProvisionedUserID(now time.Time) (string, error) {
	tsSuffix := now.UnixMicro() % kratosProvisionUserIDTimeModulo
	randomPart, err := rand.Int(rand.Reader, big.NewInt(kratosProvisionUserIDRandomRange))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d%06d", tsSuffix, randomPart.Int64()), nil
}
