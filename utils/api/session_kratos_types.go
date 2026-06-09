package api

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type kratosRequestMetadataKey struct{}

type kratosRequestMetadata struct {
	UserAgent string
	ClientIP  string
}

func WithHTTPRequestMetadata(ctx context.Context, userAgent string, clientIP string) context.Context {
	return context.WithValue(ctx, kratosRequestMetadataKey{}, kratosRequestMetadata{
		UserAgent: strings.TrimSpace(userAgent),
		ClientIP:  strings.TrimSpace(clientIP),
	})
}

type kratosRequestMetadataTransport struct {
	base http.RoundTripper
}

func (t *kratosRequestMetadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	if metadata, ok := clone.Context().Value(kratosRequestMetadataKey{}).(kratosRequestMetadata); ok {
		if metadata.UserAgent != "" && strings.TrimSpace(clone.Header.Get("User-Agent")) == "" {
			clone.Header.Set("User-Agent", metadata.UserAgent)
		}
		if metadata.ClientIP != "" {
			if strings.TrimSpace(clone.Header.Get("X-Forwarded-For")) == "" {
				clone.Header.Set("X-Forwarded-For", metadata.ClientIP)
			}
			if strings.TrimSpace(clone.Header.Get("X-Real-IP")) == "" {
				clone.Header.Set("X-Real-IP", metadata.ClientIP)
			}
		}
	}
	return base.RoundTrip(clone)
}

type resolvedKratosSession struct {
	UserID        string
	IdentityID    string
	DisplayName   *string
	EmailVerified *bool
}

type kratosSessionWhoamiResponse struct {
	ID       string               `json:"id"`
	Active   bool                 `json:"active"`
	Identity kratosIdentityRecord `json:"identity"`
}

type kratosAdminSessionRecord struct {
	ID        string     `json:"id"`
	Active    bool       `json:"active"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type kratosIdentityRecord struct {
	ID                  string                   `json:"id"`
	Traits              map[string]any           `json:"traits"`
	VerifiableAddresses []kratosVerifiableRecord `json:"verifiable_addresses"`
}

type kratosVerifiableRecord struct {
	Value    string `json:"value"`
	Verified bool   `json:"verified"`
	Status   string `json:"status"`
}

type kratosFlowResponse struct {
	ID string `json:"id"`
}

type kratosAuthSubmitResponse struct {
	SessionToken string `json:"session_token"`
}

type kratosRecoveryFlowSubmitResponse struct {
	State        string                   `json:"state"`
	ContinueWith []kratosContinueWithItem `json:"continue_with"`
}

type kratosContinueWithItem struct {
	Action          string `json:"action"`
	OrySessionToken string `json:"ory_session_token"`
}

type kratosErrorPayload struct {
	Error *struct {
		Reason string `json:"reason"`
		Text   string `json:"text"`
	} `json:"error"`
	UI *struct {
		Messages []struct {
			Text string `json:"text"`
		} `json:"messages"`
	} `json:"ui"`
}
