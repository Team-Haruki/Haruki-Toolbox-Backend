package handler

import (
	"fmt"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/mysekairestore"
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
	MysekaiRestorer *mysekairestore.Restorer
	StructuresFile  map[string]string
}

// SuiteRestoreService owns the schema-derived restorers and their degraded
// load status for one application instance. All fields are populated during
// construction and remain read-only afterwards, making Restore safe for
// concurrent upload and data-sync requests.
type SuiteRestoreService struct {
	mysekaiRestorer *mysekairestore.Restorer
	initialized     bool
	structuresFile  map[string]string

	restorers    map[string]*suiterestore.Restorer
	sources      map[string]string
	loadFailures map[string]string
}

func NewSuiteRestoreService(options SuiteRestoreServiceOptions) *SuiteRestoreService {
	service := &SuiteRestoreService{
		initialized:     true,
		mysekaiRestorer: options.MysekaiRestorer,
		structuresFile:  copyStringMap(options.StructuresFile),
		restorers:       make(map[string]*suiterestore.Restorer),
		sources:         make(map[string]string),
		loadFailures:    make(map[string]string),
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

// LoadStatus reports the immutable constructor result. The returned failure
// map is always a copy so health/status consumers cannot mutate service state.
func (s *SuiteRestoreService) LoadStatus() (int, map[string]string) {
	if s == nil || !s.initialized {
		return 0, map[string]string{"service": "suite restore service is not initialized"}
	}
	return len(s.restorers), copyStringMap(s.loadFailures)
}

func (s *SuiteRestoreService) MysekaiRestorer() *mysekairestore.Restorer {
	if s == nil {
		return nil
	}
	return s.mysekaiRestorer
}
