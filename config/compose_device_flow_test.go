package config

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var composeDefaultRef = regexp.MustCompile(`^\$\{([A-Z0-9_]+):-([^}]*)\}$`)

// TestDeviceUserCodeDefaultsMatchCompose: Hydra and the backend read the
// user-code charset, length and TTL from the same DEVICE_FLOW_* variables, and
// the compose fallbacks and .env.example values equal the backend defaults.
// Any drift makes every user code invalid, so it is caught here rather than
// in production, where only the device/auth runtime self-check (which answers
// 500 charset_mismatch / ttl_mismatch) would notice it.
func TestDeviceUserCodeDefaultsMatchCompose(t *testing.T) {
	contents, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	var compose struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(contents, &compose); err != nil {
		t.Fatalf("parse compose: %v", err)
	}
	defaults := defaultOAuth2DeviceFlowConfig()
	envExample := readEnvExample(t, "../.env.example")

	cases := []struct {
		variable   string
		hydraKey   string
		backendKey string
		want       string
	}{
		{"DEVICE_FLOW_USER_CODE_LENGTH", "OAUTH2_DEVICE_AUTHORIZATION_USER_CODE_LENGTH", "OAUTH2_DEVICE_FLOW_USER_CODE_LENGTH", strconv.Itoa(defaults.UserCodeLength)},
		{"DEVICE_FLOW_USER_CODE_CHARSET", "OAUTH2_DEVICE_AUTHORIZATION_USER_CODE_CHARACTER_SET", "OAUTH2_DEVICE_FLOW_USER_CODE_CHARSET", defaults.UserCodeCharset},
		{"DEVICE_FLOW_USER_CODE_TTL", "TTL_DEVICE_USER_CODE", "OAUTH2_DEVICE_FLOW_USER_CODE_TTL", defaults.UserCodeTTL},
	}
	for _, tc := range cases {
		for service, key := range map[string]string{"hydra": tc.hydraKey, "backend": tc.backendKey} {
			value := compose.Services[service].Environment[key]
			match := composeDefaultRef.FindStringSubmatch(value)
			if match == nil {
				t.Errorf("%s %s = %q, want ${%s:-%s}", service, key, value, tc.variable, tc.want)
				continue
			}
			if match[1] != tc.variable {
				t.Errorf("%s %s reads ${%s}, want the shared ${%s}", service, key, match[1], tc.variable)
			}
			if match[2] != tc.want {
				t.Errorf("%s %s falls back to %q, backend default is %q", service, key, match[2], tc.want)
			}
		}
		if got, ok := envExample[tc.variable]; !ok || got != tc.want {
			t.Errorf(".env.example %s = %q (present %v), backend default is %q", tc.variable, got, ok, tc.want)
		}
	}
}

// readEnvExample parses KEY=VALUE lines, dropping one level of surrounding
// double quotes.
func readEnvExample(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[key] = strings.TrimSuffix(strings.TrimPrefix(value, `"`), `"`)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return values
}
