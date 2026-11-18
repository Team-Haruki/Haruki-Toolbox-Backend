package adminoauth

import (
	"context"
	"errors"
	"slices"
	"strings"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// hydraClientGrantRevocation is the outcome of revoking one client's grants subject
// by subject.
type hydraClientGrantRevocation struct {
	// records are the client's consent sessions held by local users.
	records    []hydraClientAuthorizationRecord
	recordsErr error
	// revoked counts the subjects whose revocation Hydra accepted, including a
	// subject when it held nothing.
	revoked int
	// failed lists the subjects whose revocation failed.
	failed []string
	// Local users cover subjects omitted by the consent-session listing.
	users []*postgresql.User
}

// Hydra v25.4.0 requires a subject for consent revocation by client. Revoke
// directly for both subjects of every local user: the consent list omits some
// grants whose refresh tokens are still valid, so it is for counts only.
// Subjects with no local user are outside this sweep.
//
// A local user lookup failure prevents the sweep. Consent counts are best effort
// and a failed consent listing does not prevent the revocations.
func revokeHydraClientGrantsPerSubject(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig, clientID string) (hydraClientGrantRevocation, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		// An empty client ID would revoke every client of each subject.
		return hydraClientGrantRevocation{}, errors.New("client id is required")
	}
	users, err := loadHydraAuthorizationUsers(ctx, apiHelper.DBManager.DB)
	if err != nil {
		return hydraClientGrantRevocation{}, err
	}
	records, recordsErr := collectHydraClientAuthorizationRecordsForUsers(ctx, hydraConfig, clientID, users, 0)
	result := hydraClientGrantRevocation{records: records, recordsErr: recordsErr, users: users}
	subjects := hydraClientAuthorizationSubjects(users)
	if len(subjects) == 0 {
		return result, nil
	}
	result.revoked, result.failed, err = oauth2Module.RevokeHydraConsentSessionsForSubjects(ctx, hydraConfig, clientID, subjects)
	if err != nil {
		harukiLogger.Warnf("Failed to revoke hydra consent sessions of oauth client %s: %v", clientID, err)
	}
	return result, nil
}

func hydraClientAuthorizationSubjects(users []*postgresql.User) []string {
	subjects := make([]string, 0, len(users))
	seen := make(map[string]struct{}, len(users))
	for _, user := range users {
		for _, subject := range oauth2Module.HydraSubjectsForUser(user.ID, user.KratosIdentityID) {
			if _, ok := seen[subject]; ok {
				continue
			}
			seen[subject] = struct{}{}
			subjects = append(subjects, subject)
		}
	}
	return subjects
}

func (r hydraClientGrantRevocation) complete() bool {
	return len(r.failed) == 0
}

// revokedAuthorizations counts the grants confirmed revoked: the records whose user
// had no failed subject. A record does not say which of its user's subjects holds
// the session, so a grant is not counted while any subject of its user failed.
func (r hydraClientGrantRevocation) revokedAuthorizations() int {
	count := 0
	for _, record := range r.records {
		if record.User == nil {
			continue
		}
		if !slices.ContainsFunc(oauth2Module.HydraSubjectsForUser(record.User.ID, record.User.KratosIdentityID), r.subjectFailed) {
			count++
		}
	}
	return count
}

func (r hydraClientGrantRevocation) subjectFailed(subject string) bool {
	return slices.Contains(r.failed, subject)
}

// failedSubjectsVisibleTo lists, sorted, the failed subjects of the actor and of the
// users the actor may manage; complete() still reports the others. Today only super
// admins reach the routes that call it (RequireSuperAdmin), and they may manage
// everyone, so nothing is filtered in production. The filter is defense in depth:
// admin reads about a target user must pass EnsureAdminCanManageTargetUser, so a
// plain admin must not see a super admin's subjects if the guard is ever relaxed.
// The result is never nil, so it encodes as [] rather than null.
func (r hydraClientGrantRevocation) failedSubjectsVisibleTo(actorUserID, actorRole string) []string {
	visible := make([]string, 0, len(r.failed))
	if len(r.failed) == 0 {
		return visible
	}
	owners := make(map[string]*postgresql.User)
	users := r.users
	if len(users) == 0 {
		for _, record := range r.records {
			if record.User != nil {
				users = append(users, record.User)
			}
		}
	}
	for _, user := range users {
		for _, subject := range oauth2Module.HydraSubjectsForUser(user.ID, user.KratosIdentityID) {
			owners[subject] = user
		}
	}
	for _, subject := range r.failed {
		owner := owners[subject]
		if owner == nil {
			continue
		}
		if owner.ID == actorUserID || adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, owner.ID, string(owner.Role)) == nil {
			visible = append(visible, subject)
		}
	}
	slices.Sort(visible)
	return visible
}
