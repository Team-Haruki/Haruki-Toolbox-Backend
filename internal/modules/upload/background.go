package upload

import (
	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// startBackgroundTask preserves detached behavior for standalone/test upload
// helpers that have no application runner. Production always injects a runner;
// rejection there is logged because silently dropping persisted follow-up work
// would hide a lifecycle wiring error.
func startBackgroundTask(runner harukiBackground.Runner, logger *harukiLogger.Logger, name string, task func()) bool {
	if task == nil {
		return false
	}
	if runner == nil {
		go task()
		return true
	}
	if runner.Go(name, task) {
		return true
	}
	if logger != nil {
		logger.Warnf("Background task %q rejected because application shutdown is draining uploads", name)
	} else {
		harukiLogger.Warnf("Background task %q rejected because application shutdown is draining uploads", name)
	}
	return false
}
