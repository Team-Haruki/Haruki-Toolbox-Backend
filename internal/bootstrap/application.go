package bootstrap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

type resourceCloser struct {
	name  string
	close func() error
}

// Application owns the process-level server, long-lived schedulers, tracked
// upload work, and resources assembled by Build. An Application is single-use:
// call Serve once, then Close.
type Application struct {
	fiberApp        *fiber.App
	logger          *harukiLogger.Logger
	address         string
	serverType      string
	listenConfig    fiber.ListenConfig
	shutdownTimeout time.Duration

	// Function fields keep lifecycle behavior independently testable without
	// opening real listeners or external dependencies.
	listenFn    func() error
	listenReady <-chan struct{}
	shutdownFn  func(context.Context) error
	stopWorkers func()
	// backgroundTasks owns finite work derived from upload HTTP requests.
	// Long-lived schedulers use stopWorkers and have a different shutdown phase.
	backgroundTasks *harukiBackground.TaskGroup

	serveMu      sync.Mutex
	serveStarted bool
	serverReady  bool

	stopWorkersOnce sync.Once
	shutdownOnce    sync.Once
	shutdownErr     error
	taskDrainOnce   sync.Once
	taskDrainErr    error

	resourceClosers    []resourceCloser
	closeResourcesOnce sync.Once
	closeResourcesErr  error
}

func (a *Application) addResourceCloser(name string, closer func() error) {
	if a == nil || closer == nil {
		return
	}
	a.resourceClosers = append(a.resourceClosers, resourceCloser{name: name, close: closer})
}

func (a *Application) markServeStarted() error {
	a.serveMu.Lock()
	defer a.serveMu.Unlock()
	if a.serveStarted {
		return fmt.Errorf("application has already been served")
	}
	a.serveStarted = true
	return nil
}

func (a *Application) markServerReady() {
	a.serveMu.Lock()
	a.serverReady = true
	a.serveMu.Unlock()
}

func (a *Application) hasServerReady() bool {
	a.serveMu.Lock()
	defer a.serveMu.Unlock()
	return a.serverReady
}

type readyListener struct {
	net.Listener
	ready     chan struct{}
	tlsConfig *tls.Config
	once      sync.Once
}

func (l *readyListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.ready) })
	return l.Listener.Accept()
}

// TLSConfig keeps Fiber's listener metadata and TLS-aware context behavior
// intact even though Application wraps the listener to expose readiness.
func (l *readyListener) TLSConfig() *tls.Config {
	return l.tlsConfig
}

func (a *Application) listenerTLSConfig() (*tls.Config, error) {
	if a.listenConfig.TLSConfig != nil {
		// Match Fiber: an explicit TLSConfig is authoritative. In particular,
		// CertClientFile and TLSConfigFunc are not applied to this branch.
		return a.listenConfig.TLSConfig.Clone(), nil
	}

	minVersion := a.listenConfig.TLSMinVersion
	if minVersion == 0 {
		minVersion = tls.VersionTLS12
	}
	if minVersion != tls.VersionTLS12 && minVersion != tls.VersionTLS13 {
		return nil, fmt.Errorf("unsupported TLS minimum version %d", minVersion)
	}

	certFile := a.listenConfig.CertFile
	keyFile := a.listenConfig.CertKeyFile
	if a.listenConfig.AutoCertManager != nil && (certFile != "" || keyFile != "") {
		return nil, fiber.ErrAutoCertWithCertFile
	}

	var config *tls.Config
	switch {
	case certFile == "" && keyFile == "" && a.listenConfig.AutoCertManager == nil:
		return nil, nil
	case certFile == "" || keyFile == "":
		return nil, fmt.Errorf("both TLS certificate and key files are required")
	case certFile != "":
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load TLS key pair: %w", err)
		}
		tlsHandler := &fiber.TLSHandler{}
		config = &tls.Config{
			MinVersion:     minVersion,
			Certificates:   []tls.Certificate{certificate},
			GetCertificate: tlsHandler.GetClientInfo,
		}
		if a.fiberApp != nil {
			a.fiberApp.SetTLSHandler(tlsHandler)
		}
	default:
		config = &tls.Config{
			MinVersion:     minVersion,
			GetCertificate: a.listenConfig.AutoCertManager.GetCertificate,
			NextProtos:     []string{"http/1.1", "acme-tls/1"},
		}
	}

	if a.listenConfig.CertClientFile != "" {
		clientCAPEM, err := os.ReadFile(filepath.Clean(a.listenConfig.CertClientFile))
		if err != nil {
			return nil, fmt.Errorf("read client CA file %q: %w", a.listenConfig.CertClientFile, err)
		}
		clientCAs := x509.NewCertPool()
		if !clientCAs.AppendCertsFromPEM(clientCAPEM) {
			return nil, fmt.Errorf("parse client CA certificate from %q", a.listenConfig.CertClientFile)
		}
		config.ClientAuth = tls.RequireAndVerifyClientCert
		config.ClientCAs = clientCAs
	}
	if a.listenConfig.TLSConfigFunc != nil {
		a.listenConfig.TLSConfigFunc(config)
	}
	return config, nil
}

// startListening binds synchronously, then starts Fiber on a listener whose
// first Accept call is the definitive "server is inside Serve" handshake.
// Shutdown never runs before that handshake, eliminating shutdown-before-listen
// races even when the serve context is already canceled.
func (a *Application) startListening() (<-chan struct{}, <-chan error, error) {
	listenErrCh := make(chan error, 1)
	if a.listenFn != nil {
		if a.listenReady == nil {
			return nil, nil, fmt.Errorf("test listener readiness signal is not configured")
		}
		go func() { listenErrCh <- a.listenFn() }()
		return a.listenReady, listenErrCh, nil
	}
	if a.fiberApp == nil {
		return nil, nil, fmt.Errorf("fiber app is not initialized")
	}

	tlsConfig, err := a.listenerTLSConfig()
	if err != nil {
		return nil, nil, err
	}
	network := a.listenConfig.ListenerNetwork
	if network == "" {
		network = fiber.NetworkTCP4
	}
	listener, err := net.Listen(network, a.address)
	if err != nil {
		return nil, nil, fmt.Errorf("listen on %s: %w", a.address, err)
	}
	if a.listenConfig.ListenerAddrFunc != nil {
		a.listenConfig.ListenerAddrFunc(listener.Addr())
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}
	ready := make(chan struct{})
	wrapped := &readyListener{Listener: listener, ready: ready, tlsConfig: tlsConfig}
	go func() {
		defer func() { _ = listener.Close() }()
		listenErrCh <- a.fiberApp.Listener(wrapped, a.listenConfig)
	}()
	return ready, listenErrCh, nil
}

func (a *Application) shutdown(ctx context.Context) error {
	if a.shutdownFn != nil {
		return a.shutdownFn(ctx)
	}
	if a.fiberApp == nil {
		return fmt.Errorf("fiber app is not initialized")
	}
	return a.fiberApp.ShutdownWithContext(ctx)
}

func (a *Application) stopAndWaitWorkers() {
	if a == nil {
		return
	}
	a.stopWorkersOnce.Do(func() {
		if a.stopWorkers != nil {
			a.stopWorkers()
		}
	})
}

func (a *Application) shutdownServer() error {
	if a == nil {
		return nil
	}
	a.shutdownOnce.Do(func() {
		timeout := a.shutdownTimeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		a.shutdownErr = a.shutdown(shutdownCtx)
	})
	return a.shutdownErr
}

func (a *Application) drainBackgroundTasks() error {
	if a == nil || a.backgroundTasks == nil {
		return nil
	}
	a.taskDrainOnce.Do(func() {
		timeout := a.shutdownTimeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		drainCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := a.backgroundTasks.Shutdown(drainCtx); err != nil {
			a.taskDrainErr = fmt.Errorf("drain background tasks: %w", err)
		}
	})
	return a.taskDrainErr
}

func (a *Application) closeResources() error {
	if a == nil {
		return nil
	}
	a.closeResourcesOnce.Do(func() {
		errs := make([]error, 0)
		for i := len(a.resourceClosers) - 1; i >= 0; i-- {
			closer := a.resourceClosers[i]
			if err := closer.close(); err != nil {
				errs = append(errs, fmt.Errorf("close %s: %w", closer.name, err))
			}
		}
		a.closeResourcesErr = errors.Join(errs...)
	})
	return a.closeResourcesErr
}

// Serve starts the HTTP server and blocks until it stops or ctx is canceled.
// Cancellation stops long-lived workers, drains Fiber request handlers, and
// only then seals and drains finite upload-derived background work.
func (a *Application) Serve(ctx context.Context) error {
	if a == nil {
		return fmt.Errorf("application is nil")
	}
	if ctx == nil {
		return fmt.Errorf("serve context is nil")
	}
	if err := a.markServeStarted(); err != nil {
		return err
	}

	if a.logger != nil {
		if a.serverType == "HTTPS" {
			a.logger.Infof("SSL enabled, starting HTTPS server at %s", a.address)
		} else {
			a.logger.Infof("Starting HTTP server at %s", a.address)
		}
	}

	listenReady, listenErrCh, err := a.startListening()
	if err != nil {
		return fmt.Errorf("start %s server: %w", a.serverType, err)
	}

	// Do not observe cancellation until the listener goroutine has either
	// entered the server's Accept loop or exited. Calling Fiber.Shutdown before
	// Serve reaches that point can otherwise be a no-op followed by a hung server.
	select {
	case <-listenReady:
		a.markServerReady()
	case err := <-listenErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("start %s server: %w", a.serverType, err)
		}
		return nil
	}

	select {
	case err := <-listenErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("start %s server: %w", a.serverType, err)
		}
		return nil
	case <-ctx.Done():
		if a.logger != nil {
			a.logger.Infof("shutdown signal received, stopping server")
		}
	}

	a.stopAndWaitWorkers()
	if err := a.shutdownServer(); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	listenErr := <-listenErrCh
	taskDrainErr := a.drainBackgroundTasks()
	if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
		return errors.Join(fmt.Errorf("server stopped with error: %w", listenErr), taskDrainErr)
	}
	if taskDrainErr != nil {
		return taskDrainErr
	}
	if a.logger != nil {
		a.logger.Infof("server shutdown completed")
	}
	return nil
}

// Close stops remaining workers and the HTTP server before releasing resources.
// It is idempotent and closes resources in the reverse order they were acquired.
func (a *Application) Close() error {
	if a == nil {
		return nil
	}
	a.stopAndWaitWorkers()

	if a.hasServerReady() {
		if err := a.shutdownServer(); err != nil {
			// A timed-out graceful shutdown may leave request handlers running even
			// though the listener has stopped. Keep their databases and clients
			// alive rather than closing dependencies underneath them. The process
			// entrypoint exits on this error, so the OS will reclaim the resources.
			return err
		}
	}
	if err := a.drainBackgroundTasks(); err != nil {
		// A timed-out task may still be using PostgreSQL, Redis, or MongoDB. Keep
		// every resource alive instead of closing dependencies underneath it.
		return err
	}
	return a.closeResources()
}
