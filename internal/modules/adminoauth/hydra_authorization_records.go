package adminoauth

import (
	"context"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"golang.org/x/sync/errgroup"
)

type hydraClientAuthorizationRecord struct {
	User    *postgresql.User
	Session oauth2Module.HydraConsentSession
	Subject string
}

const hydraClientAuthorizationScanWorkers = 8

func collectHydraClientAuthorizationRecords(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientID string) ([]hydraClientAuthorizationRecord, error) {
	users, err := apiHelper.DBManager.DB.User.Query().
		Select(userSchema.FieldID, userSchema.FieldName, userSchema.FieldEmail, userSchema.FieldRole, userSchema.FieldBanned, userSchema.FieldKratosIdentityID).
		All(ctx)
	if err != nil {
		return nil, err
	}
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

	for i := 0; i < workerCount; i++ {
		g.Go(func() error {
			localRecords := make([]hydraClientAuthorizationRecord, 0, len(users)/workerCount+1)
			for user := range userCh {
				subjects := oauth2Module.HydraSubjectsForUser(user.ID, user.KratosIdentityID)
				if len(subjects) == 0 {
					continue
				}
				sessions, err := oauth2Module.ListHydraConsentSessionsForSubjects(groupCtx, subjects)
				if err != nil {
					return err
				}
				for _, session := range sessions {
					if strings.TrimSpace(session.ConsentRequest.Client.ClientID) != clientID {
						continue
					}
					localRecords = append(localRecords, hydraClientAuthorizationRecord{
						User:    user,
						Session: session,
						Subject: oauth2Module.PreferredHydraSubject(user.ID, user.KratosIdentityID),
					})
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
