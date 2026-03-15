package oauth2

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func normalizeHydraSubjects(values ...string) []string {
	subjects := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, candidate := range values {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		subjects = append(subjects, candidate)
	}
	return subjects
}

func HydraSubjectsForUser(userID string, kratosIdentityID *string) []string {
	identityID := ""
	if kratosIdentityID != nil {
		identityID = *kratosIdentityID
	}
	return normalizeHydraSubjects(identityID, userID)
}

func PreferredHydraSubject(userID string, kratosIdentityID *string) string {
	subjects := HydraSubjectsForUser(userID, kratosIdentityID)
	if len(subjects) > 0 {
		return subjects[0]
	}
	return ""
}

func CurrentHydraSubjects(c fiber.Ctx) ([]string, error) {
	identityID, identityErr := userCoreModule.CurrentKratosIdentityID(c)
	userID, userErr := userCoreModule.CurrentUserID(c)
	if identityErr != nil && userErr != nil {
		return nil, userErr
	}

	subjects := normalizeHydraSubjects(identityID, userID)
	if len(subjects) == 0 {
		if userErr != nil {
			return nil, userErr
		}
		return nil, identityErr
	}
	return subjects, nil
}

func CurrentHydraSubject(c fiber.Ctx) (string, error) {
	subjects, err := CurrentHydraSubjects(c)
	if err != nil {
		return "", err
	}
	return subjects[0], nil
}

func CurrentHydraSubjectMatches(c fiber.Ctx, subject string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return nil
	}
	subjects, err := CurrentHydraSubjects(c)
	if err != nil {
		return err
	}
	for _, candidate := range subjects {
		if subject == candidate {
			return nil
		}
	}
	return fiber.NewError(fiber.StatusForbidden, "consent request subject does not match current user")
}
