package upload

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
)

// ClientMetadata contains untrusted diagnostics, never authentication claims.
type ClientMetadata struct {
	Name          string
	Version       string
	Channel       string
	Format        string
	Protocol      string
	Platform      string
	OSVersion     string
	OSBuild       string
	OSArch        string
	AppArch       string
	OAuthClientID string
}

// Attempt is owned by one upload operation. Copy it before asynchronous use.
type Attempt struct {
	ActorUserID         string
	AuthMethod          string
	AuthorizationSource string
	GrantID             int
	Client              ClientMetadata
	RequestID           string
	ReceivedAt          time.Time
	RequestBytes        int64
	DurationMS          int64
	IdentityVerified    bool
	ErrorCode           string
	FailureStage        string
	Retryable           bool
	HTTPStatus          int
}

var strictVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
var channelPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func ParseClientVersion(raw string) (*version.Version, string, error) {
	if len(raw) == 0 || len(raw) > 128 {
		return nil, "", fmt.Errorf("invalid client version length")
	}
	m := strictVersionPattern.FindStringSubmatch(raw)
	if m == nil {
		return nil, "", fmt.Errorf("invalid semantic version")
	}
	for _, part := range strings.Split(m[4], ".") {
		numeric := part != ""
		for _, c := range part {
			if c < '0' || c > '9' {
				numeric = false
			}
		}
		if numeric && len(part) > 1 && part[0] == '0' {
			return nil, "", fmt.Errorf("leading zero in prerelease")
		}
	}
	v, err := version.NewVersion(raw)
	channel := "stable"
	if m[4] != "" {
		channel = strings.Split(m[4], ".")[0]
		if channel == "stable" {
			return nil, "", fmt.Errorf("stable is reserved for releases without prerelease identifiers")
		}
	}
	return v, channel, err
}

// ClientPolicy is immutable after compilation. Minimum returns no mutable state.
type ClientPolicy struct {
	minimum map[string]*version.Version
}

func DefaultClientPolicy() *ClientPolicy {
	p, _ := CompileClientPolicy([]string{"stable", "preview", "beta", "rc", "dev"}, map[string]string{
		"stable": "3.0.0", "preview": "3.0.0-preview", "beta": "3.0.0-beta", "rc": "3.0.0-rc", "dev": "3.0.0-dev",
	})
	return p
}

func CompileClientPolicy(channels []string, minimum map[string]string) (*ClientPolicy, error) {
	if len(channels) == 0 || len(channels) > 32 || len(channels) != len(minimum) {
		return nil, fmt.Errorf("v3 client policy requires 1-32 channels and matching minimum versions")
	}
	p := &ClientPolicy{minimum: make(map[string]*version.Version)}
	for _, channel := range channels {
		if !channelPattern.MatchString(channel) || p.minimum[channel] != nil {
			return nil, fmt.Errorf("invalid or duplicate v3 channel %q", channel)
		}
		raw := strings.TrimPrefix(minimum[channel], "v")
		v, actual, err := ParseClientVersion(raw)
		if err != nil || strings.Contains(raw, "+") || actual != channel {
			return nil, fmt.Errorf("invalid minimum version for v3 channel %q", channel)
		}
		p.minimum[channel] = v
	}
	return p, nil
}

func (p *ClientPolicy) Minimum(channel string) (*version.Version, bool) {
	v, ok := p.minimum[channel]
	return v, ok
}
