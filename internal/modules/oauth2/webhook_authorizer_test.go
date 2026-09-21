package oauth2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
)

func TestWebhookAuthorizerUsesIdentityAndLocalSubjectFallback(t *testing.T) {
	seenSubjects := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/auth/sessions/consent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		subject := r.URL.Query().Get("subject")
		seenSubjects = append(seenSubjects, subject)
		w.Header().Set("Content-Type", "application/json")
		switch subject {
		case "kratos-1":
			_, _ = w.Write([]byte(`[
				{"consent_request_id":"identity-data","grant_scope":["game-data:read"],"consent_request":{"client":{"client_id":"client-a"}}},
				{"consent_request_id":"identity-profile","grant_scope":["user:read"],"consent_request":{"client":{"client_id":"profile-only"}}}
			]`))
		case "u-1":
			_, _ = w.Write([]byte(`[
				{"consent_request_id":"legacy-duplicate","grant_scope":["bindings:read","game-data:read"],"consent_request":{"client":{"client_id":"client-a"}}},
				{"consent_request_id":"legacy-data","grant_scope":["game-data:read"],"consent_request":{"client":{"client_id":"client-b"}}}
			]`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		Provider:       "hydra",
		AdminURL:       server.URL,
		RequestTimeout: 5 * time.Second,
	})

	identityID := "kratos-1"
	clientIDs, err := (WebhookAuthorizer{HydraConfig: hydraConfig}).AuthorizedClientIDs(context.Background(), "u-1", &identityID)
	if err != nil {
		t.Fatalf("AuthorizedClientIDs returned error: %v", err)
	}
	if want := []string{"client-a", "client-b"}; !reflect.DeepEqual(clientIDs, want) {
		t.Fatalf("client ids = %#v, want %#v", clientIDs, want)
	}
	if want := []string{"kratos-1", "u-1"}; !reflect.DeepEqual(seenSubjects, want) {
		t.Fatalf("subjects = %#v, want %#v", seenSubjects, want)
	}
}

func TestWebhookAuthorizerSkipsNonHydraProvider(t *testing.T) {
	disabledConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{Provider: "builtin"})
	authorizer := WebhookAuthorizer{HydraConfig: disabledConfig}
	if authorizer.Enabled() {
		t.Fatalf("builtin provider should not enable OAuth2 webhook authorization")
	}
	clientIDs, err := authorizer.AuthorizedClientIDs(context.Background(), "u-1", nil)
	if err != nil {
		t.Fatalf("AuthorizedClientIDs returned error: %v", err)
	}
	if len(clientIDs) != 0 {
		t.Fatalf("client ids = %#v, want none", clientIDs)
	}
}
