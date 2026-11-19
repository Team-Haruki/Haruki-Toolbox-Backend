package adminoauth

import (
	"context"
	"errors"
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"golang.org/x/sync/errgroup"
)

type hydraClientAuthorizationRecord struct {
	User    *postgresql.User
	Session oauth2Module.HydraConsentSession
	Subject string
}

const (
	hydraClientAuthorizationScanWorkers = 8
	maxAuthorizationScanSubjects        = 1024
	maxAuthorizationScanRecords         = 4096
)

var authorizationScanSlots = make(chan struct{}, 2)
var errAuthorizationScanLimited = errors.New("oauth authorization scan capacity exceeded")

func scopeHydraClientAuthorizationRecordsForActor(records []hydraClientAuthorizationRecord, actorUserID, actorRole string) []hydraClientAuthorizationRecord {
	visible := make([]hydraClientAuthorizationRecord, 0, len(records))
	for _, record := range records {
		if record.User == nil {
			continue
		}
		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, record.User.ID, string(record.User.Role)); err != nil {
			continue
		}
		visible = append(visible, record)
	}
	return visible
}

func collectHydraClientAuthorizationRecords(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig, clientID, actorRole string) ([]hydraClientAuthorizationRecord, error) {
	select {
	case authorizationScanSlots <- struct{}{}:
		defer func() { <-authorizationScanSlots }()
	default:
		return nil, errAuthorizationScanLimited
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	subjects, err := hydraConfig.ListConsentSubjects(ctx, clientID, maxAuthorizationScanSubjects+1)
	query := apiHelper.DBManager.DB.User.Query().
		Select(userSchema.FieldID, userSchema.FieldName, userSchema.FieldEmail, userSchema.FieldRole, userSchema.FieldBanned, userSchema.FieldKratosIdentityID).
		Limit(maxAuthorizationScanSubjects + 1)
	if actorRole != adminCoreModule.RoleSuperAdmin {
		query.Where(userSchema.RoleNEQ(userSchema.RoleSuperAdmin))
	}
	switch {
	case errors.Is(err, harukiOAuth2.ErrConsentIndexUnavailable):
		// The Admin API is subject-scoped. Keep compatibility for small
		// installations, with a hard bound before an upstream scan begins.
	case err != nil:
		return nil, err
	case len(subjects) > maxAuthorizationScanSubjects:
		return nil, errAuthorizationScanLimited
	case len(subjects) == 0:
		return []hydraClientAuthorizationRecord{}, nil
	default:
		query.Where(userSchema.Or(userSchema.IDIn(subjects...), userSchema.KratosIdentityIDIn(subjects...)))
	}
	users, err := query.Order(userSchema.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(users) > maxAuthorizationScanSubjects {
		return nil, errAuthorizationScanLimited
	}
	return collectHydraClientAuthorizationRecordsForUsers(ctx, hydraConfig, clientID, users, maxAuthorizationScanRecords)
}

func loadHydraAuthorizationUsers(ctx context.Context, db *postgresql.Client) ([]*postgresql.User, error) {
	return db.User.Query().
		Select(userSchema.FieldID, userSchema.FieldName, userSchema.FieldEmail, userSchema.FieldRole, userSchema.FieldBanned, userSchema.FieldKratosIdentityID).
		Order(userSchema.ByID()).
		All(ctx)
}

func collectHydraClientAuthorizationRecordsForUsers(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string, users []*postgresql.User, recordLimit int) ([]hydraClientAuthorizationRecord, error) {
	if len(users) == 0 {
		return []hydraClientAuthorizationRecord{}, nil
	}

	workerCount := hydraClientAuthorizationScanWorkers
	if len(users) < workerCount {
		workerCount = len(users)
	}

	g, groupCtx := errgroup.WithContext(ctx)
	userCh := make(chan *postgresql.User)
	records := make([]hydraClientAuthorizationRecord, 0, len(users)) // pre-allocate for expected records
	var recordsMu sync.Mutex
	var scanned atomic.Int64

	for i := 0; i < workerCount; i++ {
		g.Go(func() error {
			localRecords := make([]hydraClientAuthorizationRecord, 0, len(users)/workerCount+1)
			for user := range userCh {
				subjects := oauth2Module.HydraSubjectsForUser(user.ID, user.KratosIdentityID)
				if len(subjects) == 0 {
					continue
				}
				seen := make(map[string]struct{})
				preferred := oauth2Module.PreferredHydraSubject(user.ID, user.KratosIdentityID)
				visit := func(session oauth2Module.HydraConsentSession) error {
					if recordLimit > 0 && scanned.Add(1) > int64(recordLimit) {
						return errAuthorizationScanLimited
					}
					if strings.TrimSpace(session.ConsentRequest.Client.ClientID) != clientID {
						return nil
					}
					sessionID := strings.TrimSpace(session.ConsentRequestID)
					if _, exists := seen[sessionID]; exists {
						return nil
					}
					seen[sessionID] = struct{}{}
					localRecords = append(localRecords, hydraClientAuthorizationRecord{
						User:    user,
						Session: session,
						Subject: preferred,
					})
					return nil
				}
				for _, subject := range subjects {
					if err := oauth2Module.WalkHydraConsentSessions(groupCtx, hydraConfig, subject, visit); err != nil {
						return err
					}
				}
			}
			if len(localRecords) == 0 {
				return nil
			}
			recordsMu.Lock()
			defer recordsMu.Unlock()
			records = append(records, localRecords...)
			return nil
		})
	}

	for _, user := range users {
		select {
		case <-groupCtx.Done():
			close(userCh)
			if err := g.Wait(); err != nil {
				return nil, err
			}
			return nil, groupCtx.Err()
		case userCh <- user:
		}
	}
	close(userCh)

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return records, nil
}

func hydraClientCreatedAt(client *oauth2Module.HydraOAuthClient) time.Time {
	if client == nil || client.CreatedAt == nil {
		return time.Time{}
	}
	return client.CreatedAt.UTC()
}

func hydraConsentHandledAt(session oauth2Module.HydraConsentSession) time.Time {
	if session.HandledAt != nil {
		return session.HandledAt.UTC()
	}
	return time.Time{}
}

func stableAdminHydraAuthorizationID(consentRequestID, clientID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.TrimSpace(consentRequestID)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(clientID)))
	return int(h.Sum32() & 0x7fffffff)
}
