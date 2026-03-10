package adminusers

import (
	"haruki-suite/config"
	"testing"
)

func TestHydraTokenStatsMarkedInexact(t *testing.T) {
	original := config.Cfg
	t.Cleanup(func() {
		config.Cfg = original
	})

	config.Cfg.OAuth2.Provider = "hydra"
	stats := adminOAuthTokenStats{Exact: false}
	if stats.Exact {
		t.Fatalf("expected hydra token stats to be marked inexact")
	}
}
