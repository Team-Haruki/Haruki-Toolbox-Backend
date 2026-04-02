package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Run("reject empty path", func(t *testing.T) {
		if _, err := Load(""); err == nil {
			t.Fatalf("expected Load with empty path to fail")
		}
	})

	t.Run("load config yaml", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "cfg.yaml")
		content := []byte("backend:\n  host: 127.0.0.1\n  port: 3000\n")
		if err := os.WriteFile(cfgPath, content, 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		cfg, err := Load(cfgPath)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		if cfg.Backend.Host != "127.0.0.1" {
			t.Fatalf("Backend.Host = %q, want %q", cfg.Backend.Host, "127.0.0.1")
		}
		if cfg.Backend.Port != 3000 {
			t.Fatalf("Backend.Port = %d, want %d", cfg.Backend.Port, 3000)
		}
	})

	t.Run("expand env placeholders in yaml", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "cfg.yaml")
		content := []byte("user_system:\n  kratos_public_url: ${KRATOS_PUBLIC_BASE_URL}\n")
		if err := os.WriteFile(cfgPath, content, 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		t.Setenv("KRATOS_PUBLIC_BASE_URL", "http://kratos-from-env")

		cfg, err := Load(cfgPath)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		if cfg.UserSystem.KratosPublicURL != "http://kratos-from-env" {
			t.Fatalf("UserSystem.KratosPublicURL = %q, want %q", cfg.UserSystem.KratosPublicURL, "http://kratos-from-env")
		}
	})

	t.Run("preserve fiber logger placeholders when env is missing", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "cfg.yaml")
		content := []byte("backend:\n  access_log: \"[${time}] ${ip} | ${status} - ${latency} ${method} ${path} (Sent: ${bytesSent})\\n\"\n")
		if err := os.WriteFile(cfgPath, content, 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		cfg, err := Load(cfgPath)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		want := "[${time}] ${ip} | ${status} - ${latency} ${method} ${path} (Sent: ${bytesSent})\n"
		if cfg.Backend.AccessLog != want {
			t.Fatalf("Backend.AccessLog = %q, want %q", cfg.Backend.AccessLog, want)
		}
	})

	t.Run("env overrides scalar yaml values", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "cfg.yaml")
		content := []byte("backend:\n  port: 3000\nredis:\n  password: from-yaml\n")
		if err := os.WriteFile(cfgPath, content, 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		t.Setenv("BACKEND_PORT", "4000")
		t.Setenv("REDIS_PASSWORD", "from-env")

		cfg, err := Load(cfgPath)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		if cfg.Backend.Port != 4000 {
			t.Fatalf("Backend.Port = %d, want %d", cfg.Backend.Port, 4000)
		}
		if cfg.Redis.Password != "from-env" {
			t.Fatalf("Redis.Password = %q, want %q", cfg.Redis.Password, "from-env")
		}
	})

	t.Run("apply defaults", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "cfg.yaml")
		content := []byte("{}\n")
		if err := os.WriteFile(cfgPath, content, 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		cfg, err := Load(cfgPath)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		if cfg.Backend.AutoMigrate {
			t.Fatalf("Backend.AutoMigrate = %v, want %v", cfg.Backend.AutoMigrate, false)
		}
		if cfg.Backend.ShutdownTimeout != 10 {
			t.Fatalf("Backend.ShutdownTimeout = %d, want %d", cfg.Backend.ShutdownTimeout, 10)
		}
		if cfg.UserSystem.SMTP.TimeoutSeconds != 10 {
			t.Fatalf("UserSystem.SMTP.TimeoutSeconds = %d, want %d", cfg.UserSystem.SMTP.TimeoutSeconds, 10)
		}
		if cfg.UserSystem.AuthProvider != "kratos" {
			t.Fatalf("UserSystem.AuthProvider = %q, want %q", cfg.UserSystem.AuthProvider, "kratos")
		}
		if cfg.UserSystem.KratosRequestTimeout != 10 {
			t.Fatalf("UserSystem.KratosRequestTimeout = %d, want %d", cfg.UserSystem.KratosRequestTimeout, 10)
		}
		if cfg.UserSystem.KratosSessionHeader != "X-Session-Token" {
			t.Fatalf("UserSystem.KratosSessionHeader = %q, want %q", cfg.UserSystem.KratosSessionHeader, "X-Session-Token")
		}
		if cfg.UserSystem.KratosSessionCookie != "ory_kratos_session" {
			t.Fatalf("UserSystem.KratosSessionCookie = %q, want %q", cfg.UserSystem.KratosSessionCookie, "ory_kratos_session")
		}
		if cfg.UserSystem.AuthProxyTrustedHeader != "X-Auth-Proxy-Secret" {
			t.Fatalf("UserSystem.AuthProxyTrustedHeader = %q, want %q", cfg.UserSystem.AuthProxyTrustedHeader, "X-Auth-Proxy-Secret")
		}
		if cfg.UserSystem.AuthProxySubjectHeader != "X-Kratos-Identity-Id" {
			t.Fatalf("UserSystem.AuthProxySubjectHeader = %q, want %q", cfg.UserSystem.AuthProxySubjectHeader, "X-Kratos-Identity-Id")
		}
		if cfg.UserSystem.AuthProxyNameHeader != "X-User-Name" {
			t.Fatalf("UserSystem.AuthProxyNameHeader = %q, want %q", cfg.UserSystem.AuthProxyNameHeader, "X-User-Name")
		}
		if cfg.UserSystem.AuthProxyEmailHeader != "X-User-Email" {
			t.Fatalf("UserSystem.AuthProxyEmailHeader = %q, want %q", cfg.UserSystem.AuthProxyEmailHeader, "X-User-Email")
		}
		if cfg.UserSystem.AuthProxyEmailVerifiedHeader != "X-User-Email-Verified" {
			t.Fatalf("UserSystem.AuthProxyEmailVerifiedHeader = %q, want %q", cfg.UserSystem.AuthProxyEmailVerifiedHeader, "X-User-Email-Verified")
		}
		if cfg.UserSystem.AuthProxyUserIDHeader != "X-User-Id" {
			t.Fatalf("UserSystem.AuthProxyUserIDHeader = %q, want %q", cfg.UserSystem.AuthProxyUserIDHeader, "X-User-Id")
		}
		if cfg.UserSystem.AuthProxySessionHeader != "" {
			t.Fatalf("UserSystem.AuthProxySessionHeader = %q, want empty", cfg.UserSystem.AuthProxySessionHeader)
		}
		if !cfg.UserSystem.KratosAutoLinkByEmail {
			t.Fatalf("UserSystem.KratosAutoLinkByEmail = %v, want true", cfg.UserSystem.KratosAutoLinkByEmail)
		}
		if !cfg.UserSystem.KratosAutoProvisionUser {
			t.Fatalf("UserSystem.KratosAutoProvisionUser = %v, want true", cfg.UserSystem.KratosAutoProvisionUser)
		}
	})

	t.Run("reject legacy auth provider aliases", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "cfg.yaml")
		content := []byte("user_system:\n  auth_provider: hybrid\n")
		if err := os.WriteFile(cfgPath, content, 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		if _, err := Load(cfgPath); err == nil {
			t.Fatalf("expected legacy auth provider alias to be rejected")
		}
	})

	t.Run("fallback smtp config from SMTP_CONNECTION_URI", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "cfg.yaml")
		content := []byte("{}\n")
		if err := os.WriteFile(cfgPath, content, 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		t.Setenv("SMTP_CONNECTION_URI", "smtps://no-reply%40mail.example.com:test-pass@smtp.example.com:465")
		t.Setenv("SMTP_FROM_NAME", "Haruki Test Sender")

		cfg, err := Load(cfgPath)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		if cfg.UserSystem.SMTP.SMTPAddr != "smtp.example.com" {
			t.Fatalf("SMTPAddr = %q, want %q", cfg.UserSystem.SMTP.SMTPAddr, "smtp.example.com")
		}
		if cfg.UserSystem.SMTP.SMTPPort != 465 {
			t.Fatalf("SMTPPort = %d, want %d", cfg.UserSystem.SMTP.SMTPPort, 465)
		}
		if cfg.UserSystem.SMTP.SMTPMail != "no-reply@mail.example.com" {
			t.Fatalf("SMTPMail = %q, want %q", cfg.UserSystem.SMTP.SMTPMail, "no-reply@mail.example.com")
		}
		if cfg.UserSystem.SMTP.SMTPPass != "test-pass" {
			t.Fatalf("SMTPPass = %q, want %q", cfg.UserSystem.SMTP.SMTPPass, "test-pass")
		}
		if cfg.UserSystem.SMTP.MailName != "Haruki Test Sender" {
			t.Fatalf("MailName = %q, want %q", cfg.UserSystem.SMTP.MailName, "Haruki Test Sender")
		}
	})

	t.Run("smtp uri port overrides default config smtp_port when SMTP_PORT env is unset", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "cfg.yaml")
		content := []byte("user_system:\n  smtp:\n    smtp_port: 25\n")
		if err := os.WriteFile(cfgPath, content, 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		t.Setenv("SMTP_CONNECTION_URI", "smtps://no-reply%40mail.example.com:test-pass@smtp.example.com:465")

		cfg, err := Load(cfgPath)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		if cfg.UserSystem.SMTP.SMTPPort != 465 {
			t.Fatalf("SMTPPort = %d, want %d", cfg.UserSystem.SMTP.SMTPPort, 465)
		}
	})

	t.Run("fallback smtp mail from SMTP_FROM_ADDRESS when URI username is empty", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "cfg.yaml")
		content := []byte("{}\n")
		if err := os.WriteFile(cfgPath, content, 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		t.Setenv("SMTP_CONNECTION_URI", "smtps://:test-pass@smtp.example.com:465")
		t.Setenv("SMTP_FROM_ADDRESS", "no-reply@example.com")

		cfg, err := Load(cfgPath)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		if cfg.UserSystem.SMTP.SMTPMail != "no-reply@example.com" {
			t.Fatalf("SMTPMail = %q, want %q", cfg.UserSystem.SMTP.SMTPMail, "no-reply@example.com")
		}
	})

	t.Run("env overrides auth proxy session header", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "cfg.yaml")
		content := []byte("{}\n")
		if err := os.WriteFile(cfgPath, content, 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		t.Setenv("AUTH_PROXY_SESSION_HEADER", "X-Auth-Proxy-Session-Id")

		cfg, err := Load(cfgPath)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		if cfg.UserSystem.AuthProxySessionHeader != "X-Auth-Proxy-Session-Id" {
			t.Fatalf("UserSystem.AuthProxySessionHeader = %q, want %q", cfg.UserSystem.AuthProxySessionHeader, "X-Auth-Proxy-Session-Id")
		}
	})
}

func TestLoadGlobalFromEnvOrDefault(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "cfg.yaml")
	content := []byte("user_system:\n  kratos_public_url: http://kratos-public\n  kratos_admin_url: http://kratos-admin\n")
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	prevCfg := Cfg
	t.Cleanup(func() { Cfg = prevCfg })

	t.Setenv("HARUKI_CONFIG_PATH", cfgPath)
	resolvedPath, err := LoadGlobalFromEnvOrDefault()
	if err != nil {
		t.Fatalf("LoadGlobalFromEnvOrDefault returned error: %v", err)
	}
	if !sameExistingPath(resolvedPath, cfgPath) {
		t.Fatalf("resolvedPath = %q, want %q", resolvedPath, cfgPath)
	}
	if Cfg.UserSystem.KratosPublicURL != "http://kratos-public" {
		t.Fatalf("Cfg.UserSystem.KratosPublicURL = %q, want %q", Cfg.UserSystem.KratosPublicURL, "http://kratos-public")
	}
}

func TestFindConfigPath(t *testing.T) {
	t.Run("finds file in current directory", func(t *testing.T) {
		tmp := t.TempDir()
		filename := "cfg.yaml"
		target := filepath.Join(tmp, filename)
		if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		withWorkingDir(t, tmp, func() {
			got := findConfigPath(filename)
			if !sameExistingPath(got, target) {
				t.Fatalf("findConfigPath returned %q, want %q", got, target)
			}
		})
	})

	t.Run("finds file in parent directory", func(t *testing.T) {
		tmp := t.TempDir()
		filename := "cfg.yaml"
		target := filepath.Join(tmp, filename)
		if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		nested := filepath.Join(tmp, "a", "b")
		if err := os.MkdirAll(nested, 0755); err != nil {
			t.Fatalf("MkdirAll failed: %v", err)
		}

		withWorkingDir(t, nested, func() {
			got := findConfigPath(filename)
			if !sameExistingPath(got, target) {
				t.Fatalf("findConfigPath returned %q, want %q", got, target)
			}
		})
	})

	t.Run("returns filename when not found", func(t *testing.T) {
		tmp := t.TempDir()
		withWorkingDir(t, tmp, func() {
			got := findConfigPath("missing.yaml")
			if got != "missing.yaml" {
				t.Fatalf("findConfigPath returned %q, want %q", got, "missing.yaml")
			}
		})
	})
}

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s) failed: %v", dir, err)
	}
	defer func() {
		if chdirErr := os.Chdir(wd); chdirErr != nil {
			t.Fatalf("failed to restore working directory: %v", chdirErr)
		}
	}()
	fn()
}

func sameExistingPath(a, b string) bool {
	aEval, aErr := filepath.EvalSymlinks(a)
	bEval, bErr := filepath.EvalSymlinks(b)
	if aErr == nil && bErr == nil {
		return aEval == bEval
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
