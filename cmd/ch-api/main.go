package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	_ "github.com/gokaybaz/go-cloud-hypervisor-service/docs"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/config"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/handler"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/service"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/store"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/api"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/audit"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/auth"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/eventlog"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/lifecycle"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/imagemanager"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/metrics"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/network"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/pprof"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/sshkey"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/preflight"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/ratelimit"
	chtls "github.com/gokaybaz/go-cloud-hypervisor-service/pkg/tls"
	"golang.org/x/crypto/acme/autocert"
)

// @title           Cloud Hypervisor API
// @version         1.0
// @description     REST API for managing Cloud Hypervisor VMs.
// @termsOfService  https://github.com/gokaybaz/go-cloud-hypervisor-service

// @contact.name   API Support
// @contact.url    https://github.com/gokaybaz/go-cloud-hypervisor-service/issues
// @contact.email  support@example.com

// @license.name  MIT
// @license.url   https://opensource.org/licenses/MIT

// @host      localhost:8080
// @BasePath  /

// @externalDocs.description  OpenAPI Specification
// @externalDocs.url          https://swagger.io/resources/open-api/

func main() {
	loader := config.NewLoader()
	cfg, err := loader.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger := logging.New(cfg.Log.Level)
	if cfg.Log.Format == "console" {
		os.Setenv("LOG_FORMAT", "console")
	}

	if os.Getenv("PREFLIGHT") == "1" {
		if !preflight.Verify() {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if cfg.Preflight.Enabled {
		report := preflight.Check()
		if report.HasFailures() {
			for _, res := range report.Failed() {
				logger.Warn("preflight check failed", "name", res.Name, "message", res.Message)
			}
			if cfg.Preflight.StrictMode {
				logger.Error("preflight strict mode enabled, exiting")
				os.Exit(1)
			}
		}
	}

	mr := metrics.New()

	store := store.New(logger, mr)

	// Event log: rotating file + syslog.
	var el eventlog.Writer
	fw, err := eventlog.NewFileWriter("data/events")
	if err != nil {
		logger.Error("failed to create event log file writer", "err", err)
	} else {
		sw, serr := eventlog.NewSyslogWriter("ch-api")
		if serr != nil {
			logger.Warn("syslog unavailable, using file-only event log", "err", serr)
			el = fw
		} else {
			el = eventlog.NewMulti(fw, sw)
		}
		defer el.Close()
	}

	netMgr := network.NewManager(cfg.Network.HostIface)
	if err := netMgr.SetupNAT(cfg.Network.HostIface); err != nil {
		logger.Warn("NAT setup failed (may already be configured)", "err", err)
	}

	imageMgr := imagemanager.NewManager(
		cfg.Images.BasePath,
		cfg.Images.BaseImage,
		cfg.Images.Kernel,
	)

	sshKeyMgr := sshkey.NewManager(cfg.Keys.BasePath)

	svc := service.New(store, logger, el, netMgr, imageMgr, sshKeyMgr)

	auditor, err := audit.New("data/audit")
	if err != nil {
		logger.Error("failed to create auditor", "err", err)
		os.Exit(1)
	}
	defer auditor.Close()

	var authCfg *auth.Config
	if cfg.Auth.Enabled && cfg.Auth.Secret != "" {
		authCfg = &auth.Config{
			Secret:      cfg.Auth.Secret,
			Issuer:      cfg.Auth.Issuer,
			Audience:    cfg.Auth.Audience,
			RBACEnabled: cfg.Auth.RBACEnabled,
		}
	}

	rl := ratelimit.NewLimiter(ratelimit.Config{
		Enabled:       cfg.RateLimit.Enabled,
		Window:        cfg.RateLimit.Window,
		IPLimit:       cfg.RateLimit.IPLimit,
		ViewerLimit:   cfg.RateLimit.ViewerLimit,
		OperatorLimit: cfg.RateLimit.OperatorLimit,
		AdminLimit:    cfg.RateLimit.AdminLimit,
	}, logger)

	router := api.NewRouter(logger, auditor, mr, authCfg, rl)
	handler.Register(router, svc, logger, mr)

	srvCfg := api.ServerConfig{
		Host:         cfg.Server.Host,
		Port:         cfg.Server.Port,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}
	srv := api.NewServer(srvCfg, router.Handler())

	// TLS configuration.
	tlsCfg, err := chtls.NewTLSConfig(chtls.Config{
		Enabled:     cfg.TLS.Enabled,
		CertFile:    cfg.TLS.CertFile,
		KeyFile:     cfg.TLS.KeyFile,
		ACMEEnabled: cfg.TLS.ACMEEnabled,
	})
	if err != nil {
		logger.Error("failed to create TLS config", "err", err)
		os.Exit(1)
	}
	if tlsCfg != nil {
		srv.TLSConfig = tlsCfg
	}

	acmMgr := chtls.NewAutocertManager(chtls.Config{
		Enabled:     cfg.TLS.Enabled,
		ACMEEnabled: cfg.TLS.ACMEEnabled,
		ACMEEmail:   cfg.TLS.ACMEEmail,
		ACMEDomains: cfg.TLS.ACMEDomains,
		ACMECache:   cfg.TLS.ACMECache,
	})
	if acmMgr != nil {
		srv.TLSConfig.GetCertificate = acmMgr.GetCertificate
	}

	mgr := lifecycle.NewManager(logger, lifecycle.WithStopTimeout(cfg.Server.ShutdownTimeout))

	mgr.Register(&configWatcher{loader: loader, logger: logger})
	mgr.Register(&rateLimiter{limiter: rl, logger: logger})
	mgr.Register(&httpServer{srv: srv, acmMgr: acmMgr, logger: logger})

	if cfg.Profile {
		mgr.Register(pprof.New(logger, ""))
		logger.Info("pprof profiling enabled", "addr", "localhost:6060")
	}

	if err := mgr.Run(context.Background()); err != nil {
		logger.Error("lifecycle error", "err", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// configWatcher lifecycle component
// ---------------------------------------------------------------------------

type configWatcher struct {
	loader  *config.Loader
	watcher *config.Watcher
	logger  logging.Logger
}

// Name returns the component name.
func (c *configWatcher) Name() string { return "config-watcher" }

// Start begins watching the configuration file for changes.
func (c *configWatcher) Start(_ context.Context) error {
	c.watcher = c.loader.Watch(func(newCfg *config.Config) {
		c.logger.Info("config reloaded", "level", newCfg.Log.Level)
	})
	return nil
}

// Stop halts the configuration file watcher.
func (c *configWatcher) Stop(_ context.Context) error {
	if c.watcher != nil {
		c.watcher.Stop()
	}
	return nil
}

// ---------------------------------------------------------------------------
// httpServer lifecycle component
// ---------------------------------------------------------------------------

type httpServer struct {
	srv     *http.Server
	acmMgr  *autocert.Manager
	logger  logging.Logger
}

// Name returns the component name.
func (s *httpServer) Name() string { return "http-server" }

// Start begins listening for HTTP connections in a background goroutine.
func (s *httpServer) Start(_ context.Context) error {
	go func() {
		if s.acmMgr != nil {
			s.logger.Info("starting server with ACME TLS", "addr", s.srv.Addr)
			// ACME HTTP-01 challenge handler on port 80.
			go func() {
				s.logger.Info("starting ACME HTTP-01 challenge server", "addr", ":80")
				if err := http.ListenAndServe(":80", s.acmMgr.HTTPHandler(nil)); err != nil && err != http.ErrServerClosed {
					s.logger.Error("ACME HTTP-01 server error", "err", err)
				}
			}()
			if err := s.srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				s.logger.Error("server error", "err", err)
			}
		} else if s.srv.TLSConfig != nil {
			s.logger.Info("starting server with TLS", "addr", s.srv.Addr)
			if err := s.srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				s.logger.Error("server error", "err", err)
			}
		} else {
			s.logger.Info("starting server", "addr", s.srv.Addr)
			if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				s.logger.Error("server error", "err", err)
			}
		}
	}()
	return nil
}

// Stop gracefully shuts down the HTTP server.
func (s *httpServer) Stop(ctx context.Context) error {
	s.logger.Info("shutting down server")
	return s.srv.Shutdown(ctx)
}

// ---------------------------------------------------------------------------
// rateLimiter lifecycle component
// ---------------------------------------------------------------------------

type rateLimiter struct {
	limiter *ratelimit.Limiter
	logger  logging.Logger
}

func (r *rateLimiter) Name() string { return "rate-limiter" }

func (r *rateLimiter) Start(_ context.Context) error { return nil }

func (r *rateLimiter) Stop(_ context.Context) error {
	r.logger.Info("stopping rate limiter")
	r.limiter.Stop()
	return nil
}
