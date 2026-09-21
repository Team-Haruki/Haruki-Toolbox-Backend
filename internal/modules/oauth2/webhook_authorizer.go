package oauth2

import (
	"context"
	"strings"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
)

// WebhookAuthorizer adapts Hydra consent state to the narrow capability used
// by upload webhook fanout. The identity subject remains preferred while the
// local user ID is retained as a compatibility fallback.
type WebhookAuthorizer struct {
	HydraConfig *harukiOAuth2.HydraConfig
}

func (a WebhookAuthorizer) Enabled() bool {
	return HydraOAuthManagementEnabled(a.HydraConfig)
}

func (a WebhookAuthorizer) AuthorizedClientIDs(ctx context.Context, userID string, kratosIdentityID *string) ([]string, error) {
	if !HydraOAuthManagementEnabled(a.HydraConfig) {
		return nil, nil
	}

	subjects := HydraSubjectsForUser(userID, kratosIdentityID)
	if len(subjects) == 0 {
		return nil, nil
	}
	sessions, err := ListHydraConsentSessionsForSubjects(ctx, a.HydraConfig, subjects)
	if err != nil {
		return nil, err
	}
	return gameDataWebhookClientIDs(sessions), nil
}

func gameDataWebhookClientIDs(sessions []HydraConsentSession) []string {
	clientIDs := make([]string, 0, len(sessions))
	seen := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		if !harukiOAuth2.HasScope(session.GrantScope, harukiOAuth2.ScopeGameDataRead) {
			continue
		}
		clientID := strings.TrimSpace(session.ConsentRequest.Client.ClientID)
		if clientID == "" {
			continue
		}
		if _, ok := seen[clientID]; ok {
			continue
		}
		seen[clientID] = struct{}{}
		clientIDs = append(clientIDs, clientID)
	}
	return clientIDs
}
