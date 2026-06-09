package bootstrap

import (
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"strings"
)

func validateUserSystemConfig(cfg harukiConfig.Config) error {
	provider := strings.ToLower(strings.TrimSpace(cfg.UserSystem.AuthProvider))
	if provider == "" {
		provider = "kratos"
	}
	if provider != "kratos" {
		return fmt.Errorf("user_system.auth_provider=%q is unsupported; only kratos is supported", strings.TrimSpace(cfg.UserSystem.AuthProvider))
	}
	if strings.TrimSpace(cfg.UserSystem.KratosPublicURL) == "" {
		return fmt.Errorf("user_system.kratos_public_url is required")
	}
	if strings.TrimSpace(cfg.UserSystem.KratosAdminURL) == "" {
		return fmt.Errorf("user_system.kratos_admin_url is required")
	}
	if cfg.UserSystem.AuthProxyEnabled {
		if strings.TrimSpace(cfg.UserSystem.AuthProxyTrustedHeader) == "" {
			return fmt.Errorf("user_system.auth_proxy_trusted_header is required when auth_proxy_enabled=true")
		}
		if strings.TrimSpace(cfg.UserSystem.AuthProxyTrustedValue) == "" {
			return fmt.Errorf("user_system.auth_proxy_trusted_value is required when auth_proxy_enabled=true")
		}
		if strings.TrimSpace(cfg.UserSystem.AuthProxySubjectHeader) == "" {
			return fmt.Errorf("user_system.auth_proxy_subject_header is required when auth_proxy_enabled=true")
		}
		if strings.TrimSpace(cfg.UserSystem.AuthProxySessionHeader) == "" {
			return fmt.Errorf("user_system.auth_proxy_session_header is required when auth_proxy_enabled=true")
		}
	}
	return nil
}

func validateOAuth2ProviderConfig(cfg harukiConfig.Config) error {
	provider := strings.ToLower(strings.TrimSpace(cfg.OAuth2.Provider))
	if provider == "" || provider == harukiOAuth2.ProviderHydra {
		if strings.TrimSpace(cfg.OAuth2.HydraPublicURL) == "" {
			return fmt.Errorf("oauth2.hydra_public_url is required when oauth2.provider=hydra")
		}
		if strings.TrimSpace(cfg.OAuth2.HydraAdminURL) == "" {
			return fmt.Errorf("oauth2.hydra_admin_url is required when oauth2.provider=hydra")
		}
		return nil
	}
	return fmt.Errorf("oauth2.provider=%q is unsupported; only hydra is supported", strings.TrimSpace(cfg.OAuth2.Provider))
}
