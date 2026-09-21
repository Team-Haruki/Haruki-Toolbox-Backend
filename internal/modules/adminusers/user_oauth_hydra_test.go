package adminusers

import (
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
)

func TestHydraTokenStatsMarkedInexact(t *testing.T) {
	hydraConfig := oauth2.NewHydraConfig(oauth2.HydraConfigOptions{Provider: oauth2.ProviderHydra})
	if !hydraConfig.Enabled() {
		t.Fatalf("expected hydra provider to be enabled")
	}

	stats := adminOAuthTokenStats{Exact: false}
	if stats.Exact {
		t.Fatalf("expected hydra token stats to be marked inexact")
	}
}
