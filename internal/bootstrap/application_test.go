package bootstrap

import (
	"context"
	"crypto/tls"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"

	"github.com/gofiber/fiber/v3"
)

func TestApplicationLifecycleOrderAndIdempotentClose(t *testing.T) {
	events := make([]string, 0, 8)
	listenStarted := make(chan struct{})
	stopListener := make(chan struct{})
	releaseTask := make(chan struct{})

	application := &Application{
		serverType:      "HTTP",
		shutdownTimeout: time.Second,
		listenFn: func() error {
			close(listenStarted)
			<-stopListener
			return nil
		},
		listenReady: listenStarted,
		shutdownFn: func(context.Context) error {
			events = append(events, "shutdown server")
			close(stopListener)
			close(releaseTask)
			return nil
		},
		stopWorkers: func() {
			events = append(events, "stop workers")
		},
		backgroundTasks: harukiBackground.NewTaskGroup(nil),
	}
	if !application.backgroundTasks.Go("request-derived", func() {
		<-releaseTask
		events = append(events, "drain request task")
	}) {
		t.Fatal("background task was rejected")
	}

	for _, name := range []string{"main log", "mongo", "redis", "toolbox db", "access log", "bot db"} {
		name := name
		application.addResourceCloser(name, func() error {
			events = append(events, "close "+name)
			return nil
		})
	}

	serveCtx, cancelServe := context.WithCancel(context.Background())
	go func() {
		<-listenStarted
		cancelServe()
	}()

	if err := application.Serve(serveCtx); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if err := application.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := application.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}

	want := []string{
		"stop workers",
		"shutdown server",
		"drain request task",
		"close bot db",
		"close access log",
		"close toolbox db",
		"close redis",
		"close mongo",
		"close main log",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle events = %#v, want %#v", events, want)
	}
}

func TestApplicationTaskDrainTimeoutKeepsResourcesOpen(t *testing.T) {
	t.Parallel()

	listenStarted := make(chan struct{})
	stopListener := make(chan struct{})
	releaseTask := make(chan struct{})
	resourceClosed := false
	application := &Application{
		serverType:      "HTTP",
		shutdownTimeout: 20 * time.Millisecond,
		listenFn: func() error {
			close(listenStarted)
			<-stopListener
			return nil
		},
		listenReady: listenStarted,
		shutdownFn: func(context.Context) error {
			close(stopListener)
			return nil
		},
		backgroundTasks: harukiBackground.NewTaskGroup(nil),
	}
	application.addResourceCloser("database", func() error {
		resourceClosed = true
		return nil
	})
	if !application.backgroundTasks.Go("blocked", func() { <-releaseTask }) {
		t.Fatal("background task was rejected")
	}
	t.Cleanup(func() {
		close(releaseTask)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := application.backgroundTasks.Shutdown(ctx); err != nil {
			t.Errorf("cleanup task drain returned error: %v", err)
		}
	})

	serveCtx, cancelServe := context.WithCancel(context.Background())
	go func() {
		<-listenStarted
		cancelServe()
	}()
	err := application.Serve(serveCtx)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "drain background tasks") {
		t.Fatalf("Serve error = %v, want background task drain deadline", err)
	}
	if application.backgroundTasks.Go("late", func() {}) {
		t.Fatal("task group accepted work after drain began")
	}
	if closeErr := application.Close(); !errors.Is(closeErr, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want cached drain deadline", closeErr)
	}
	if resourceClosed {
		t.Fatal("resources were closed while a background task could still be running")
	}
}

func TestApplicationCloseOnlyPathReportsTaskDrainFailure(t *testing.T) {
	t.Parallel()

	releaseTask := make(chan struct{})
	listenReady := make(chan struct{})
	resourceClosed := false
	application := &Application{
		serverType:      "HTTP",
		shutdownTimeout: 20 * time.Millisecond,
		listenFn:        func() error { return nil },
		listenReady:     listenReady,
		shutdownFn:      func(context.Context) error { return nil },
		backgroundTasks: harukiBackground.NewTaskGroup(nil),
	}
	application.addResourceCloser("database", func() error {
		resourceClosed = true
		return nil
	})
	if !application.backgroundTasks.Go("blocked", func() { <-releaseTask }) {
		t.Fatal("background task was rejected")
	}
	t.Cleanup(func() {
		close(releaseTask)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := application.backgroundTasks.Shutdown(ctx); err != nil {
			t.Errorf("cleanup task drain returned error: %v", err)
		}
	})

	// A listener that exits by itself leaves final cleanup to Close. Close must
	// surface the drain failure so main can return a non-zero status.
	if err := application.Serve(context.Background()); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if err := application.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want background task drain deadline", err)
	}
	if resourceClosed {
		t.Fatal("resources were closed after Close-only task drain failure")
	}
}

func TestApplicationServePreservesListenError(t *testing.T) {
	listenErr := errors.New("listen failed")
	listenReady := make(chan struct{})
	application := &Application{
		serverType:      "HTTPS",
		shutdownTimeout: time.Second,
		listenFn: func() error {
			return listenErr
		},
		listenReady: listenReady,
		shutdownFn: func(context.Context) error {
			return nil
		},
	}

	err := application.Serve(context.Background())
	if !errors.Is(err, listenErr) {
		t.Fatalf("Serve error = %v, want wrapped listen error", err)
	}
	if !strings.Contains(err.Error(), "start HTTPS server") {
		t.Fatalf("Serve error = %v, want start HTTPS server context", err)
	}
	if closeErr := application.Close(); closeErr != nil {
		t.Fatalf("Close returned error: %v", closeErr)
	}
}

func TestApplicationServePreservesGracefulShutdownError(t *testing.T) {
	shutdownErr := errors.New("shutdown failed")
	listenStarted := make(chan struct{})
	stopListener := make(chan struct{})
	resourceClosed := false
	application := &Application{
		serverType:      "HTTP",
		shutdownTimeout: time.Second,
		listenFn: func() error {
			close(listenStarted)
			<-stopListener
			return nil
		},
		listenReady: listenStarted,
		shutdownFn: func(context.Context) error {
			close(stopListener)
			return shutdownErr
		},
	}
	application.addResourceCloser("database", func() error {
		resourceClosed = true
		return nil
	})

	serveCtx, cancelServe := context.WithCancel(context.Background())
	go func() {
		<-listenStarted
		cancelServe()
	}()
	err := application.Serve(serveCtx)
	if !errors.Is(err, shutdownErr) {
		t.Fatalf("Serve error = %v, want wrapped shutdown error", err)
	}
	if !strings.Contains(err.Error(), "graceful shutdown failed") {
		t.Fatalf("Serve error = %v, want graceful shutdown context", err)
	}
	if closeErr := application.Close(); !errors.Is(closeErr, shutdownErr) {
		t.Fatalf("Close error = %v, want shutdown error", closeErr)
	}
	if resourceClosed {
		t.Fatal("Close released resources after shutdown failed")
	}
}

func TestApplicationServeAlreadyCanceledWaitsForListenerReadiness(t *testing.T) {
	t.Parallel()

	listenReady := make(chan struct{})
	listenerDone := make(chan struct{})
	shutdownCalled := make(chan struct{})
	application := &Application{
		serverType:      "HTTP",
		shutdownTimeout: time.Second,
		listenFn: func() error {
			close(listenReady)
			<-listenerDone
			return nil
		},
		listenReady: listenReady,
		shutdownFn: func(context.Context) error {
			select {
			case <-listenReady:
			default:
				t.Error("shutdown ran before listener readiness")
			}
			close(shutdownCalled)
			close(listenerDone)
			return nil
		},
	}

	serveCtx, cancelServe := context.WithCancel(context.Background())
	cancelServe()
	if err := application.Serve(serveCtx); err != nil {
		t.Fatalf("Serve returned error for already-canceled context: %v", err)
	}
	select {
	case <-shutdownCalled:
	default:
		t.Fatal("Serve returned without shutting down the ready listener")
	}
}

func TestApplicationServeCancellationDuringListenerStartup(t *testing.T) {
	t.Parallel()

	listenInvoked := make(chan struct{})
	allowReady := make(chan struct{})
	listenReady := make(chan struct{})
	listenerDone := make(chan struct{})
	application := &Application{
		serverType:      "HTTP",
		shutdownTimeout: time.Second,
		listenFn: func() error {
			close(listenInvoked)
			<-allowReady
			close(listenReady)
			<-listenerDone
			return nil
		},
		listenReady: listenReady,
		shutdownFn: func(context.Context) error {
			select {
			case <-listenReady:
			default:
				t.Error("shutdown ran while listener startup was incomplete")
			}
			close(listenerDone)
			return nil
		},
	}

	serveCtx, cancelServe := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- application.Serve(serveCtx) }()
	<-listenInvoked
	cancelServe()
	close(allowReady)

	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve hung when cancellation raced listener startup")
	}
}

func TestApplicationServeAlreadyCanceledWithRealFiberListener(t *testing.T) {
	t.Parallel()

	application := &Application{
		fiberApp:        fiber.New(),
		address:         "127.0.0.1:0",
		serverType:      "HTTP",
		listenConfig:    fiber.ListenConfig{DisableStartupMessage: true},
		shutdownTimeout: time.Second,
	}
	serveCtx, cancelServe := context.WithCancel(context.Background())
	cancelServe()

	serveResult := make(chan error, 1)
	go func() { serveResult <- application.Serve(serveCtx) }()
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve hung after binding a real listener for an already-canceled context")
	}
	if err := application.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func TestApplicationListenerTLSConfigHonorsMinimumVersion(t *testing.T) {
	t.Parallel()

	application := &Application{
		listenConfig: fiber.ListenConfig{
			TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13},
		},
	}
	config, err := application.listenerTLSConfig()
	if err != nil {
		t.Fatalf("listenerTLSConfig returned error: %v", err)
	}
	if config.MinVersion != tls.VersionTLS13 {
		t.Fatalf("TLS minimum version = %d, want TLS 1.3", config.MinVersion)
	}
	if config == application.listenConfig.TLSConfig {
		t.Fatal("listenerTLSConfig returned the caller-owned TLS config without cloning")
	}
}

func TestApplicationServeRejectsSecondCall(t *testing.T) {
	listenReady := make(chan struct{})
	application := &Application{
		serverType:      "HTTP",
		shutdownTimeout: time.Second,
		listenFn: func() error {
			return nil
		},
		listenReady: listenReady,
		shutdownFn: func(context.Context) error {
			return nil
		},
	}

	if err := application.Serve(context.Background()); err != nil {
		t.Fatalf("first Serve returned error: %v", err)
	}
	if err := application.Serve(context.Background()); err == nil || !strings.Contains(err.Error(), "already been served") {
		t.Fatalf("second Serve error = %v, want already served error", err)
	}
	if err := application.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}
