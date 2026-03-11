package oauth2

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func PreferredHydraSubject(userID string, kratosIdentityID *string) string {
	if kratosIdentityID != nil {
		if trimmedIdentityID := strings.TrimSpace(*kratosIdentityID); trimmedIdentityID != "" {
			return trimmedIdentityID
		}
	}
	return strings.TrimSpace(userID)
}

func CurrentHydraSubjects(c fiber.Ctx) ([]string, error) {
	identityID, identityErr := userCoreModule.CurrentKratosIdentityID(c)
	userID, userErr := userCoreModule.CurrentUserID(c)
	if identityErr != nil && userErr != nil {
		return nil, userErr
	}

	subjects := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	for _, candidate := range []string{identityID, userID} {
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
