package handler

import (
	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/nuversestruct"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/suiterestore"
	"sync"
)

var (
	suiteRestorerOnce         sync.Once
	suiteRestorerMap          map[string]*suiterestore.Restorer
	suiteRestorerSourceMap    map[string]string
	suiteRestorerLoadFailures map[string]string
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

func initSuiteRestorers() {
	suiteRestorerOnce.Do(func() {
		suiteRestorerMap = make(map[string]*suiterestore.Restorer)
		suiteRestorerSourceMap = make(map[string]string)
		suiteRestorerLoadFailures = make(map[string]string)
		for region, path := range harukiConfig.Cfg.RestoreSuite.StructuresFile {
			if path == "" {
				continue
			}
			r, err := loadSuiteRestorer(path)
			if err != nil {
				harukiLogger.Errorf("failed to load suite structure file for region %s (%s): %v", region, path, err)
				suiteRestorerLoadFailures[region] = err.Error()
				continue
			}
			suiteRestorerMap[region] = r
			suiteRestorerSourceMap[region] = path
		}
	})
}

func loadSuiteRestorer(path string) (*suiterestore.Restorer, error) {
	return nuversestruct.NewRestorerFromFile(path)
}

func getSuiteRestorer(server utils.SupportedDataUploadServer) *suiterestore.Restorer {
	initSuiteRestorers()
	return suiteRestorerMap[string(server)]
}

func getSuiteRestorerSource(server utils.SupportedDataUploadServer) string {
	initSuiteRestorers()
	return suiteRestorerSourceMap[string(server)]
}

func RestoreSuite(
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

	if purpose == SuiteRestorePurposeDatabase {
		data = cleanSuite(data)
		if !shouldRestoreSuiteForDB(server) {
			report.Enabled = false
			return data, report, nil
		}
	}

	restorer := getSuiteRestorer(server)
	report.Source = getSuiteRestorerSource(server)
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

func cleanSuite(suite map[string]any) map[string]any {
	removeKeys := harukiConfig.Cfg.SekaiClient.SuiteRemoveKeys
	for _, key := range removeKeys {
		if _, ok := suite[key]; ok {
			suite[key] = []any{}
		}
	}
	return suite
}

func shouldRestoreSuiteForDB(server utils.SupportedDataUploadServer) bool {
	for _, r := range harukiConfig.Cfg.RestoreSuite.EnableRegions {
		if r == string(server) {
			return true
		}
	}
	return false
}

func GetSuiteRestorerLoadStatus() (int, map[string]string) {
	initSuiteRestorers()

	failures := make(map[string]string, len(suiteRestorerLoadFailures))
	for region, message := range suiteRestorerLoadFailures {
		failures[region] = message
	}
	return len(suiteRestorerMap), failures
}
