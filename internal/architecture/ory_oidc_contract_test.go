package architecture

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOryOIDCProviderGatewayContract(t *testing.T) {
	root := repositoryRoot(t)
	hydraConfig := filepath.Join(root, "external", "hydra", "hydra.yml")
	assertFileContainsAll(t, hydraConfig,
		"issuer:",
		"supported_types:",
		"- public",
		"- pairwise",
		"same_site_mode: Lax",
	)
	// Device authorization lives only in compose env. Hydra's schema makes the
	// user_code entropy preset and length/character_set mutually exclusive.
	assertFileContainsNone(t, hydraConfig, "entropy_preset", "device:", "device_authorization")
	assertFileContainsAll(t, filepath.Join(root, "external", "oathkeeper", "access-rules.yml"),
		"/.well-known/<.*>",
		"oauth2/jwks.json",
		"userinfo",
	)
}

// hydraDeviceComposeEnv are the Hydra variables that configure device
// authorization and point both discovery documents at the backend.
var hydraDeviceComposeEnv = []string{
	"URLS_DEVICE_VERIFICATION",
	"URLS_DEVICE_SUCCESS",
	"TTL_DEVICE_USER_CODE",
	"OAUTH2_DEVICE_AUTHORIZATION_TOKEN_POLLING_INTERVAL",
	"OAUTH2_DEVICE_AUTHORIZATION_USER_CODE_LENGTH",
	"OAUTH2_DEVICE_AUTHORIZATION_USER_CODE_CHARACTER_SET",
	"WEBFINGER_OIDC_DISCOVERY_DEVICE_AUTHORIZATION_URL",
	"WEBFINGER_OIDC_DISCOVERY_TOKEN_URL",
}

var backendDeviceComposeEnv = []string{
	"OAUTH2_DEVICE_FLOW_ENABLED",
	"OAUTH2_DEVICE_FLOW_CLIENT_ALLOWLIST",
	"OAUTH2_DEVICE_FLOW_ALLOWED_ORIGINS",
	"OAUTH2_DEVICE_FLOW_HYDRA_ISSUER_URL",
	"OAUTH2_DEVICE_FLOW_USER_CODE_LENGTH",
	"OAUTH2_DEVICE_FLOW_USER_CODE_CHARSET",
	"OAUTH2_DEVICE_FLOW_USER_CODE_TTL",
}

var deviceFlowEnvExampleKeys = []string{
	"DEVICE_FLOW_USER_CODE_LENGTH",
	"DEVICE_FLOW_USER_CODE_CHARSET",
	"DEVICE_FLOW_USER_CODE_TTL",
	"DEVICE_FLOW_POLLING_INTERVAL",
	"OAUTH2_DEVICE_FLOW_ENABLED",
	"OAUTH2_DEVICE_FLOW_CLIENT_ALLOWLIST",
	"OAUTH2_DEVICE_FLOW_ALLOWED_ORIGINS",
	"HYDRA_DEVICE_JANITOR_GRACE",
	"HYDRA_DEVICE_JANITOR_BATCH",
	"HYDRA_DEVICE_JANITOR_INTERVAL_SECONDS",
}

type composeService struct {
	Image       string            `yaml:"image"`
	Restart     string            `yaml:"restart"`
	Networks    []string          `yaml:"networks"`
	Environment map[string]string `yaml:"environment"`
	Entrypoint  []string          `yaml:"entrypoint"`
	Command     yaml.Node         `yaml:"command"`
	DependsOn   map[string]struct {
		Condition string `yaml:"condition"`
	} `yaml:"depends_on"`
	Ports []string `yaml:"ports"`
}

func loadComposeServices(t *testing.T, path string) map[string]composeService {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var compose struct {
		Services map[string]composeService `yaml:"services"`
	}
	if err := yaml.Unmarshal(contents, &compose); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return compose.Services
}

// TestOryDeviceFlowDeploymentContract pins the deployment side of the device
// authorization grant (docs/oauth2-device-flow-design §10.1 / §10.4).
func TestOryDeviceFlowDeploymentContract(t *testing.T) {
	root := repositoryRoot(t)
	composePath := filepath.Join(root, "docker-compose.yml")
	assertFileContainsAll(t, composePath, append(append(append([]string{},
		hydraDeviceComposeEnv...), backendDeviceComposeEnv...),
		"hydra-device-janitor",
		`entrypoint: ["/bin/sh", "-c"]`,
		"<<'SQL'",
	)...)
	// A folded command scalar loses the janitor's SQL quotes; the preset is
	// mutually exclusive with LENGTH + CHARACTER_SET.
	assertFileContainsNone(t, composePath,
		"command: >",
		"OAUTH2_DEVICE_AUTHORIZATION_USER_CODE_ENTROPY_PRESET",
	)
	assertFileContainsAll(t, filepath.Join(root, ".env.example"), envKeyAssignments(deviceFlowEnvExampleKeys)...)

	services := loadComposeServices(t, composePath)
	hydra, ok := services["hydra"]
	if !ok {
		t.Fatal("compose has no hydra service")
	}
	wantHydra := map[string]string{
		"URLS_DEVICE_VERIFICATION":                            "${FRONTEND_PUBLIC_URL}/device",
		"URLS_DEVICE_SUCCESS":                                 "${FRONTEND_PUBLIC_URL}/device/done",
		"TTL_DEVICE_USER_CODE":                                "${DEVICE_FLOW_USER_CODE_TTL:-10m}",
		"OAUTH2_DEVICE_AUTHORIZATION_TOKEN_POLLING_INTERVAL":  "${DEVICE_FLOW_POLLING_INTERVAL:-5s}",
		"OAUTH2_DEVICE_AUTHORIZATION_USER_CODE_LENGTH":        "${DEVICE_FLOW_USER_CODE_LENGTH:-8}",
		"OAUTH2_DEVICE_AUTHORIZATION_USER_CODE_CHARACTER_SET": "${DEVICE_FLOW_USER_CODE_CHARSET:-BCDFGHJKLMNPQRSTVWXZ}",
		"WEBFINGER_OIDC_DISCOVERY_DEVICE_AUTHORIZATION_URL":   "${BACKEND_PUBLIC_BASE_URL}/api/oauth2/device/auth",
		"WEBFINGER_OIDC_DISCOVERY_TOKEN_URL":                  "${BACKEND_PUBLIC_BASE_URL}/api/oauth2/token",
		"URLS_SELF_ISSUER":                                    "${HYDRA_PUBLIC_BASE_URL}",
	}
	assertComposeEnv(t, "hydra", hydra.Environment, wantHydra)
	// The device accept redirect is built from Hydra's public URL; the backend
	// rewrites it against OAUTH2_DEVICE_FLOW_HYDRA_ISSUER_URL (= the issuer).
	if value, ok := hydra.Environment["URLS_SELF_PUBLIC"]; ok {
		t.Errorf("hydra must not set URLS_SELF_PUBLIC (got %q) unless OAUTH2_DEVICE_FLOW_HYDRA_ISSUER_URL follows it", value)
	}

	backend, ok := services["backend"]
	if !ok {
		t.Fatal("compose has no backend service")
	}
	wantBackend := map[string]string{
		"OAUTH2_DEVICE_FLOW_ENABLED":           "${OAUTH2_DEVICE_FLOW_ENABLED:-false}",
		"OAUTH2_DEVICE_FLOW_CLIENT_ALLOWLIST":  "${OAUTH2_DEVICE_FLOW_CLIENT_ALLOWLIST:-}",
		"OAUTH2_DEVICE_FLOW_ALLOWED_ORIGINS":   "${OAUTH2_DEVICE_FLOW_ALLOWED_ORIGINS:-}",
		"OAUTH2_DEVICE_FLOW_HYDRA_ISSUER_URL":  "${HYDRA_PUBLIC_BASE_URL}",
		"OAUTH2_DEVICE_FLOW_USER_CODE_LENGTH":  "${DEVICE_FLOW_USER_CODE_LENGTH:-8}",
		"OAUTH2_DEVICE_FLOW_USER_CODE_CHARSET": "${DEVICE_FLOW_USER_CODE_CHARSET:-BCDFGHJKLMNPQRSTVWXZ}",
		"OAUTH2_DEVICE_FLOW_USER_CODE_TTL":     "${DEVICE_FLOW_USER_CODE_TTL:-10m}",
	}
	assertComposeEnv(t, "backend", backend.Environment, wantBackend)
	for key := range backend.Environment {
		if strings.HasPrefix(key, "OAUTH2_DEVICE_FLOW_") && !slices.Contains(backendDeviceComposeEnv, key) {
			t.Errorf("backend sets unexpected device-flow variable %s", key)
		}
	}

	assertHydraDeviceJanitor(t, services)
}

func assertComposeEnv(t *testing.T, service string, got, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s environment %s = %q, want %q", service, key, got[key], value)
		}
	}
}

// assertHydraDeviceJanitor checks the janitor sidecar's shape and the exact
// SQL. compose turns `$$` into a literal `$` for the container shell.
func assertHydraDeviceJanitor(t *testing.T, services map[string]composeService) {
	t.Helper()
	janitor, ok := services["hydra-device-janitor"]
	if !ok {
		t.Fatal("compose has no hydra-device-janitor service")
	}
	if janitor.Image != "postgres:18-alpine" || janitor.Restart != "unless-stopped" {
		t.Errorf("janitor image/restart = %q/%q", janitor.Image, janitor.Restart)
	}
	if !slices.Equal(janitor.Networks, []string{"haruki-net"}) {
		t.Errorf("janitor networks = %v, want [haruki-net]", janitor.Networks)
	}
	if len(janitor.Ports) != 0 {
		t.Errorf("janitor must not publish ports, got %v", janitor.Ports)
	}
	if janitor.DependsOn["postgres"].Condition != "service_healthy" ||
		janitor.DependsOn["hydra-migrate"].Condition != "service_completed_successfully" {
		t.Errorf("janitor depends_on = %+v", janitor.DependsOn)
	}
	assertComposeEnv(t, "hydra-device-janitor", janitor.Environment, map[string]string{
		"PGHOST":                   "postgres",
		"PGDATABASE":               "hydra",
		"PGUSER":                   "${HYDRA_DB_USER}",
		"PGPASSWORD":               "${HYDRA_DB_PASSWORD}",
		"JANITOR_GRACE":            "${HYDRA_DEVICE_JANITOR_GRACE:-1 hour}",
		"JANITOR_BATCH":            "${HYDRA_DEVICE_JANITOR_BATCH:-5000}",
		"JANITOR_INTERVAL_SECONDS": "${HYDRA_DEVICE_JANITOR_INTERVAL_SECONDS:-3600}",
	})
	if !slices.Equal(janitor.Entrypoint, []string{"/bin/sh", "-c"}) {
		t.Errorf("janitor entrypoint = %v, want [/bin/sh -c]", janitor.Entrypoint)
	}
	command := janitor.Command
	if command.Kind != yaml.SequenceNode || len(command.Content) != 1 || command.Content[0].Kind != yaml.ScalarNode {
		t.Fatalf("janitor command must be a one-element list, got kind %v with %d items", command.Kind, len(command.Content))
	}
	if command.Content[0].Style != yaml.LiteralStyle {
		t.Errorf("janitor command script must be a literal (|) block scalar")
	}
	script := command.Content[0].Value
	for _, fragment := range []string{
		"psql -X -v ON_ERROR_STOP=1 -At -v grace=\"$$JANITOR_GRACE\" -v batch=\"$$JANITOR_BATCH\" <<'SQL'",
		"SELECT device_code_signature, nid FROM hydra_oauth2_device_auth_codes",
		"WHERE expires_at IS NOT NULL",
		"AND expires_at < (now() AT TIME ZONE 'UTC') - :'grace'::interval",
		"ORDER BY expires_at LIMIT :batch",
		"DELETE FROM hydra_oauth2_device_auth_codes t USING doomed",
		"WHERE t.device_code_signature = doomed.device_code_signature AND t.nid = doomed.nid",
		"\nSQL\n",
		`echo "hydra-device-janitor: delete failed"`,
		`[ "$$n" -lt "$$JANITOR_BATCH" ] && break`,
		`echo "hydra-device-janitor: deleted=$$total remaining=$$left"`,
		`sleep "$$JANITOR_INTERVAL_SECONDS"`,
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("janitor script is missing %q", fragment)
		}
	}
	// Every shell expansion must be escaped for compose; a bare `$` would be
	// interpolated (or rejected) when the file is rendered.
	if strings.Contains(strings.ReplaceAll(script, "$$", ""), "$") {
		t.Errorf("janitor script has an unescaped $ (use $$)")
	}
}

func envKeyAssignments(keys []string) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = "\n" + key + "="
	}
	return out
}

func assertFileContainsAll(t *testing.T, path string, expected ...string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, fragment := range expected {
		if !strings.Contains(string(contents), fragment) {
			t.Errorf("%s is missing OIDC contract fragment %q", path, fragment)
		}
	}
}

func assertFileContainsNone(t *testing.T, path string, forbidden ...string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, fragment := range forbidden {
		if strings.Contains(string(contents), fragment) {
			t.Errorf("%s must not contain %q", path, fragment)
		}
	}
}
