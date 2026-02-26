package api

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"runtime/debug"

	harukiAPIHelper "haruki-suite/utils/api"
	harukiLogger "haruki-suite/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func RegisterDebugRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	logger := harukiLogger.NewLogger("Debug", "DEBUG", nil)

	go func() {
		logger.Infof("Starting pprof server on :6060")
		if err := http.ListenAndServe(":6060", nil); err != nil {
			logger.Errorf("pprof server failed: %v", err)
		}
	}()

	apiHelper.Router.Get("/api/debug/memstats", func(c fiber.Ctx) error {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return c.JSON(fiber.Map{
			"alloc_mb":         fmt.Sprintf("%.1f", float64(m.Alloc)/1024/1024),
			"total_alloc_mb":   fmt.Sprintf("%.1f", float64(m.TotalAlloc)/1024/1024),
			"sys_mb":           fmt.Sprintf("%.1f", float64(m.Sys)/1024/1024),
			"heap_alloc_mb":    fmt.Sprintf("%.1f", float64(m.HeapAlloc)/1024/1024),
			"heap_sys_mb":      fmt.Sprintf("%.1f", float64(m.HeapSys)/1024/1024),
			"heap_idle_mb":     fmt.Sprintf("%.1f", float64(m.HeapIdle)/1024/1024),
			"heap_inuse_mb":    fmt.Sprintf("%.1f", float64(m.HeapInuse)/1024/1024),
			"heap_released_mb": fmt.Sprintf("%.1f", float64(m.HeapReleased)/1024/1024),
			"heap_objects":     m.HeapObjects,
			"goroutines":       runtime.NumGoroutine(),
			"num_gc":           m.NumGC,
			"gc_cpu_fraction":  fmt.Sprintf("%.4f", m.GCCPUFraction),
		})
	})

	apiHelper.Router.Post("/api/debug/freemem", func(c fiber.Ctx) error {
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		runtime.GC()
		debug.FreeOSMemory()

		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		return c.JSON(fiber.Map{
			"before_heap_mb":   fmt.Sprintf("%.1f", float64(before.HeapAlloc)/1024/1024),
			"after_heap_mb":    fmt.Sprintf("%.1f", float64(after.HeapAlloc)/1024/1024),
			"freed_mb":         fmt.Sprintf("%.1f", float64(before.HeapAlloc-after.HeapAlloc)/1024/1024),
			"heap_released_mb": fmt.Sprintf("%.1f", float64(after.HeapReleased)/1024/1024),
		})
	})
}
