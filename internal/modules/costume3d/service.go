// Package costume3d serves the content the CostumeShop 3D kernel reads (Haruki-3D
// docs/api.md "Content"), in the raw-bundle mirror layout its createMirrorContent takes:
//
//	/api/misc/costume3d/{region}/current.json             the mirror's synced version
//	/api/misc/costume3d/{region}/catalogs/{version}.msgpack the decrypted AssetBundleInfo
//	/api/misc/costume3d/{region}/bundles/{name}.bundle      one CostumeShop bundle (decrypted UnityFS)
//	/api/misc/costume3d/{region}/master/characters/{id}.json one character's master rows
//	/api/misc/costume3d/{region}/ui/archive-background/{file} the "archive" background textures
//
// Bundles, catalogs and current.json come from the region's raw-bundle mirror (rawsync.py),
// limited to the bundle families the kernel reads (the en / tw / kr / cn mirrors are
// complete trees whose bundles shared with jp are hard links to jp's files). The three
// archive background textures (APK player data,
// scripts/content/extract-archive-background.py in Haruki-3D) are placed in the mirror under
// ui/archive-background/; master rows come from the master registry, cut down per character.
package costume3d

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/masterregistry"
)

// How long a registry content hash or the mirror's current.json is trusted before it is
// asked again.
const versionCheckInterval = time.Minute

type Service struct {
	registry *masterregistry.Client
	mirrors  map[string]string // region -> mirror root, ending in "/"
	client   *http.Client
	ua       string
	logger   *harukiLogger.Logger

	mu      sync.Mutex
	masters map[string]*regionMaster
	mirror  map[string]*regionMirror
}

// regionMaster is the per-character gzip JSON of one registry content hash.
type regionMaster struct {
	build     sync.Mutex // one build at a time
	hash      string
	checkedAt time.Time
	slices    map[int][]byte
}

// regionMirror caches the mirror's current.json and its catalog.
type regionMirror struct {
	mu          sync.Mutex
	current     []byte
	catalogName string // "catalogs/<version>.msgpack"
	checkedAt   time.Time
	catalogFile string
	catalogGzip []byte
}

// New validates the configuration; nil means the feature is off.
func New(cfg config.Costume3DConfig, logger *harukiLogger.Logger) (*Service, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.RegistryBaseURL == "" {
		return nil, fmt.Errorf("costume3d: registry_base_url is required")
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "Haruki-Toolbox-Backend"
	}
	timeout := time.Duration(cfg.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	client := &http.Client{Timeout: timeout}
	mirrors := map[string]string{}
	for region, base := range cfg.MirrorBaseURLs {
		if base = strings.TrimSpace(base); base != "" {
			mirrors[region] = strings.TrimRight(base, "/") + "/"
		}
	}
	return &Service{
		registry: masterregistry.New(cfg.RegistryBaseURL, ua, client),
		mirrors:  mirrors,
		client:   client,
		ua:       ua,
		logger:   logger,
		masters:  map[string]*regionMaster{},
		mirror:   map[string]*regionMirror{},
	}, nil
}

// characterMaster returns one character's master rows (gzip JSON) and the content hash
// they belong to; nil rows for a character the master does not have.
func (s *Service) characterMaster(ctx context.Context, region string, characterID int) ([]byte, string, error) {
	s.mu.Lock()
	m := s.masters[region]
	if m == nil {
		m = &regionMaster{}
		s.masters[region] = m
	}
	s.mu.Unlock()

	m.build.Lock()
	defer m.build.Unlock()
	if m.slices == nil || time.Since(m.checkedAt) > versionCheckInterval {
		if err := s.refreshMaster(ctx, region, m); err != nil {
			return nil, "", err
		}
		m.checkedAt = time.Now()
	}
	return m.slices[characterID], m.hash, nil
}

// refreshMaster asks the registry for the region's content hash and rebuilds the slices
// when it changed. While the registry is unreachable the slices built before stay in use.
// The caller holds m.build.
func (s *Service) refreshMaster(ctx context.Context, region string, m *regionMaster) error {
	hash, err := s.registry.ContentHash(ctx, region)
	if err != nil {
		if m.slices == nil {
			return err
		}
		s.logger.Warnf("costume3d: %s master version check failed: %v", region, err)
		return nil
	}
	if hash == m.hash && m.slices != nil {
		return nil
	}
	tables, err := fetchMasterTables(ctx, s.registry, region, hash)
	if err != nil {
		return err
	}
	slices, err := tables.sliceAll(hash)
	if err != nil {
		return err
	}
	m.hash, m.slices = hash, slices
	return nil
}

func (s *Service) regionMirror(region string) (*regionMirror, string, bool) {
	base, ok := s.mirrors[region]
	if !ok {
		return nil, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.mirror[region]
	if m == nil {
		m = &regionMirror{}
		s.mirror[region] = m
	}
	return m, base, true
}
