package handler

import (
	harukiConfig "haruki-suite/config"
	"haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/suiterestore"
	"sync"
)

var (
	suiteRestorerOnce         sync.Once
	suiteRestorerMap          map[string]*suiterestore.Restorer
	suiteRestorerLoadFailures map[string]string
)

func initSuiteRestorers() {
	suiteRestorerOnce.Do(func() {
		suiteRestorerMap = make(map[string]*suiterestore.Restorer)
		suiteRestorerLoadFailures = make(map[string]string)
		for region, path := range harukiConfig.Cfg.RestoreSuite.StructuresFile {
			if path == "" {
				continue
			}
			r, err := suiterestore.NewFromFile(path)
			if err != nil {
				harukiLogger.Errorf("failed to load suite structure file for region %s (%s): %v", region, path, err)
				suiteRestorerLoadFailures[region] = err.Error()
				continue
			}
			suiteRestorerMap[region] = r
		}
	})
}

func getSuiteRestorer(server utils.SupportedDataUploadServer) *suiterestore.Restorer {
	initSuiteRestorers()
	return suiteRestorerMap[string(server)]
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
