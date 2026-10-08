package config

import "testing"

func TestExampleConfigServerTimeoutsAndSekaiAPI(t *testing.T) {
	cfg, err := Load("../haruki-toolbox-configs.example.yaml")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	if cfg.Backend.ReadTimeoutSeconds != 120 || cfg.Backend.WriteTimeoutSeconds != 60 || cfg.Backend.IdleTimeoutSeconds != 120 {
		t.Fatalf("example server timeouts = %+v", cfg.Backend)
	}
	api := cfg.SekaiAPI
	if api.ProfileViewTimeoutSeconds != 5 || api.VerifyTimeoutSeconds != 10 || api.ProfileCacheTTLSeconds != 60 || len(api.ProfileViewTimeoutSecondsByServer) != 0 {
		t.Fatalf("example sekai_api = %+v", api)
	}
}
