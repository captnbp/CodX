// Command codx is the entrypoint for the CodX server.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/captnbp/CodX/internal/config"
	"github.com/captnbp/CodX/internal/inactivity"
	"github.com/captnbp/CodX/internal/k8s"
	"github.com/captnbp/CodX/internal/oidc"
	"github.com/captnbp/CodX/internal/session"
	"github.com/captnbp/CodX/internal/web"
	"github.com/go-logr/zerologr"
	"github.com/rs/zerolog"
)

func main() {
	configPath := flag.String("config", "/etc/codx/config.yaml", "path to the CodX configuration file")
	flag.Parse()

	if err := run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "codx: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	// Set up structured logging with zerolog.
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	zl := zerolog.New(os.Stdout).With().Timestamp().Caller().Logger()
	log := zerologr.New(&zl)

	log.Info("starting CodX server", "config", configPath)

	// Load configuration.
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	cfg, err := config.Load(data)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log.Info("configuration loaded",
		"instance", cfg.InstanceName,
		"listen", cfg.HTTP.ListenAddr,
		"oidcIssuer", cfg.OIDC.Issuer,
	)

	// Set up signal handling.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Set up the OIDC authenticator.
	auth, err := oidc.New(ctx, cfg.OIDC)
	if err != nil {
		return fmt.Errorf("init OIDC: %w", err)
	}
	log.Info("OIDC provider discovered", "issuer", cfg.OIDC.Issuer)

	// Set up the session store (Redis in production).
	store := session.NewRedisStore(session.RedisOptions{
		Addr:       cfg.Redis.Host,
		Password:   cfg.Redis.Password,
		DB:         cfg.Redis.DB,
		TLS:        cfg.Redis.TLS,
		CAFilePath: cfg.Redis.CAFilePath,
		KeyPrefix:  cfg.InstanceName,
	})
	defer store.Close()

	// Set up the Kubernetes clientset.
	// TODO: use real client-go clients loaded from in-cluster config.
	clientset := &k8s.Clientset{}

	// Set up the profile store.
	profileStore := k8s.NewProfileStore()
	_ = profileStore // TODO: load profiles via informer

	// Set up the workspace manager.
	wm := k8s.NewWorkspaceManager(clientset, cfg)

	// Set up the inactivity watcher.
	checkInterval, _ := time.ParseDuration(cfg.Inactivity.CheckInterval)
	if checkInterval == 0 {
		checkInterval = 60 * time.Second
	}
	activitySource := inactivity.NewLogTailActivity()
	watcher := inactivity.NewWatcher(activitySource, wm.StopWorkspace, checkInterval, log)
	go watcher.Run(ctx)

	// Set up the web server.
	webServer := web.New(cfg, auth, store, profileStore, wm, nil)

	server := &http.Server{
		Addr:         cfg.HTTP.ListenAddr,
		Handler:      webServer.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // SSE needs no write timeout
		IdleTimeout:  120 * time.Second,
	}

	// Start the HTTPS server (TLS is mandatory).
	errCh := make(chan error, 1)
	go func() {
		log.Info("HTTPS server starting", "addr", cfg.HTTP.ListenAddr)
		if err := server.ListenAndServeTLS(cfg.HTTP.TLSCertFile, cfg.HTTP.TLSKeyFile); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("HTTPS server: %w", err)
		}
	}()

	// Wait for termination signal or server error.
	select {
	case <-ctx.Done():
		log.Info("received termination signal, shutting down")
	case err := <-errCh:
		log.Error(err, "server error")
		return err
	}

	// Graceful shutdown.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error(err, "graceful shutdown failed")
	}

	log.Info("CodX server stopped")
	return nil
}
