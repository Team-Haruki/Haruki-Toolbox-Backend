package handler

import (
	"fmt"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/nuversestruct"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/suiterestore"
)

type SuiteRestorePurpose string

const (
	SuiteRestorePurposeDatabase SuiteRestorePurpose = "database"
	SuiteRestorePurposeSync     SuiteRestorePurpose = "sync"
)

type SuiteRestoreOptions struct {
	Purpose SuiteRestorePurpose
}

type SuiteRestoreReport struct {
	Region         string              `json:"region"`
	Source         string              `json:"source,omitempty"`
	Purpose        SuiteRestorePurpose `json:"purpose"`
	Enabled        bool                `json:"enabled"`
	RestorerLoaded bool                `json:"restorerLoaded"`
	RestoredFields int                 `json:"restoredFields"`
	FailedFields   []string            `json:"failedFields,omitempty"`
}

// SuiteRestoreServiceOptions contains the immutable startup configuration for
// suite restoration. NewSuiteRestoreService defensively copies every
// collection, so later mutation of the source Config cannot affect a running
// application instance.
type SuiteRestoreServiceOptions struct {
	StructuresFile  map[string]string
	EnableRegions   []string
	SuiteRemoveKeys []string
	// MongoOnlyRemoveKeys are blanked on the way into MongoDB and nowhere else.
	// Leaving it empty reproduces the historical behaviour exactly: everything
	// listed in SuiteRemoveKeys is blanked before either store sees it.
	MongoOnlyRemoveKeys []string
}

// SuiteRestoreService owns the schema-derived restorers and their degraded
// load status for one application instance. All fields are populated during
// construction and remain read-only afterwards, making Restore safe for
// concurrent upload and data-sync requests.
type SuiteRestoreService struct {
	initialized     bool
	structuresFile  map[string]string
	enableRegions   []string
	suiteRemoveKeys []string
	// mongoOnlyRemoveKeys is the second list, and unlike suiteRemoveKeys it is
	// NOT expanded with compact spellings.
	//
	// cn/tw/kr send only the compact form, so expanding here would blank the
	// only copy MongoDB holds — and MongoDB is still the read source. The
	// PostgreSQL row would keep the value, but nothing reads it yet, so those
	// regions would lose the key on the private API until the cutover. Adding
	// the expansion is safe once reads come from PostgreSQL; until then the
	// three big keys stay unstripped for cn/tw/kr, which is exactly the state
	// they have always been in.
	mongoOnlyRemoveKeys []string

	restorers    map[string]*suiterestore.Restorer
	sources      map[string]string
	loadFailures map[string]string
}

func NewSuiteRestoreService(options SuiteRestoreServiceOptions) *SuiteRestoreService {
	service := &SuiteRestoreService{
		initialized:     true,
		structuresFile:  copyStringMap(options.StructuresFile),
		enableRegions:   append([]string(nil), options.EnableRegions...),
		suiteRemoveKeys: withCompactSpellings(options.SuiteRemoveKeys),
		// NOT expanded with compact spellings, deliberately — see the field doc.
		mongoOnlyRemoveKeys: append([]string(nil), options.MongoOnlyRemoveKeys...),
		restorers:           make(map[string]*suiterestore.Restorer),
		sources:             make(map[string]string),
		loadFailures:        make(map[string]string),
	}

	for region, path := range service.structuresFile {
		if path == "" {
			continue
		}
		restorer, err := loadSuiteRestorer(path)
		if err != nil {
			harukiLogger.Errorf("failed to load suite structure file for region %s (%s): %v", region, path, err)
			service.loadFailures[region] = err.Error()
			continue
		}
		service.restorers[region] = restorer
		service.sources[region] = path
	}

	return service
}

func copyStringMap(source map[string]string) map[string]string {
	copy := make(map[string]string, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func loadSuiteRestorer(path string) (*suiterestore.Restorer, error) {
	return nuversestruct.NewRestorerFromFile(path)
}

func (s *SuiteRestoreService) Restore(
	server utils.SupportedDataUploadServer,
	data map[string]any,
	options SuiteRestoreOptions,
) (map[string]any, SuiteRestoreReport, error) {
	purpose := normalizeSuiteRestorePurpose(options.Purpose)
	report := SuiteRestoreReport{
		Region:  string(server),
		Purpose: purpose,
		Enabled: true,
	}
	if s == nil || !s.initialized {
		report.Enabled = false
		return data, report, fmt.Errorf("suite restore service is not initialized")
	}

	if purpose == SuiteRestorePurposeDatabase {
		data = s.cleanSuite(data)
		if !s.shouldRestoreSuiteForDB(server) {
			report.Enabled = false
			return data, report, nil
		}
	}

	restorer := s.restorers[string(server)]
	report.Source = s.sources[string(server)]
	report.RestorerLoaded = restorer != nil
	if restorer == nil {
		return data, report, nil
	}

	restored, restoreReport := restorer.RestoreFieldsWithReport(data)
	report.RestoredFields = restoreReport.RestoredFields
	report.FailedFields = append(report.FailedFields, restoreReport.FailedFields...)
	return restored, report, nil
}

func normalizeSuiteRestorePurpose(purpose SuiteRestorePurpose) SuiteRestorePurpose {
	switch purpose {
	case SuiteRestorePurposeDatabase, SuiteRestorePurposeSync:
		return purpose
	default:
		return SuiteRestorePurposeDatabase
	}
}

// withCompactSpellings returns keys plus, for every key the game compacts, its
// compact spelling.
//
// Blanking by exact name alone silently missed cn/tw/kr for as long as the
// feature has existed: those clients send `compactUserCostume3dShopItems`, not
// `userCostume3dShopItems`, so 5,821 of 5,822 cn rows still carried the full
// value. An empty array is a valid stored value for either spelling — the
// stored form is self-describing, an object is compact and an array is row form
// — so one blanking rule covers both.
//
// Applied to the list blanked in EVERY store, where a missed spelling means the
// key stays readable somewhere. Deliberately NOT applied to the MongoDB-only
// list; see the mongoOnlyRemoveKeys field doc.
func withCompactSpellings(keys []string) []string {
	out := make([]string, 0, len(keys)*2)
	seen := make(map[string]bool, len(keys)*2)
	add := func(k string) {
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, k)
	}
	for _, k := range keys {
		add(k)
		if compact, ok := catalog.CompactPairs[k]; ok {
			add(compact)
		}
	}
	return out
}

func blankKeys(suite map[string]any, keys []string) {
	for _, key := range keys {
		if _, ok := suite[key]; ok {
			suite[key] = []any{}
		}
	}
}

func (s *SuiteRestoreService) cleanSuite(suite map[string]any) map[string]any {
	blankKeys(suite, s.suiteRemoveKeys)
	return suite
}

// StripForMongoStore returns a copy of a suite upload with the MongoDB-only
// keys blanked, leaving the caller's map — the one the PostgreSQL game-data
// store receives — untouched.
//
// The copy is the whole point. cleanSuite blanks in place, so handing it the
// upload map would empty the very keys the game-data store exists to keep. A
// shallow copy is enough: only top-level entries are replaced, never mutated.
func (s *SuiteRestoreService) StripForMongoStore(suite map[string]any) map[string]any {
	if s == nil || !s.initialized || len(s.mongoOnlyRemoveKeys) == 0 || suite == nil {
		return suite
	}
	out := make(map[string]any, len(suite))
	for k, v := range suite {
		out[k] = v
	}
	blankKeys(out, s.mongoOnlyRemoveKeys)
	return out
}

func (s *SuiteRestoreService) shouldRestoreSuiteForDB(server utils.SupportedDataUploadServer) bool {
	for _, region := range s.enableRegions {
		if region == string(server) {
			return true
		}
	}
	return false
}

// LoadStatus reports the immutable constructor result. The returned failure
// map is always a copy so health/status consumers cannot mutate service state.
func (s *SuiteRestoreService) LoadStatus() (int, map[string]string) {
	if s == nil || !s.initialized {
		return 0, map[string]string{"service": "suite restore service is not initialized"}
	}
	return len(s.restorers), copyStringMap(s.loadFailures)
}
