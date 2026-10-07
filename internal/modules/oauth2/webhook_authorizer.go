package oauth2

import (
	"context"
	"strings"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
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
	return activeWebhookClientIDs(ctx, gameDataWebhookClientIDs(sessions), checkHydraOAuth2ClientActive(a.HydraConfig)), nil
}

// activeWebhookClientIDs drops clients an admin has disabled (or deleted).
// Hydra keeps their consent sessions and ignores metadata.haruki.active, so
// without this a disabled client would still be told about the user's
// game-data updates. clientIDs is already de-duplicated, so a fan-out looks
// each client up at most once. A failed lookup skips that client only.
func activeWebhookClientIDs(ctx context.Context, clientIDs []string, isActive harukiOAuth2.ClientActiveChecker) []string {
	activeIDs := make([]string, 0, len(clientIDs))
	for _, clientID := range clientIDs {
		active, err := isActive(ctx, clientID)
		if err != nil {
			harukiLogger.Warnf("Skip OAuth2 webhook client whose active state is unknown: client=%s err=%v", clientID, err)
			continue
		}
		if active {
			activeIDs = append(activeIDs, clientID)
		}
	}
	return activeIDs
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
