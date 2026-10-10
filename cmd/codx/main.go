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
	"github.com/captnbp/CodX/internal/leader"
	"github.com/captnbp/CodX/internal/metrics"
	"github.com/captnbp/CodX/internal/oidc"
	"github.com/captnbp/CodX/internal/session"
	"github.com/captnbp/CodX/internal/tracing"
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
	// #nosec G304 -- configPath is the -config flag value, operator controlled.
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

	// Set up OpenTelemetry tracing (optional, controlled by the tracing
	// section of the configuration).
	tracingShutdown, err := tracing.Setup(ctx, cfg.Tracing)
	if err != nil {
		return fmt.Errorf("init tracing: %w", err)
	}
	if cfg.Tracing.Enabled {
		log.Info("OpenTelemetry tracing enabled",
			"otlpEndpoint", cfg.Tracing.OTLPEndpoint,
			"serviceName", cfg.Tracing.ServiceName,
		)
	}

	// Set up the OIDC authenticator.
	auth, err := oidc.New(ctx, cfg.OIDC)
	if err != nil {
		return fmt.Errorf("init OIDC: %w", err)
	}
	log.Info("OIDC provider discovered", "issuer", cfg.OIDC.Issuer)

	// Set up the session store (Redis in production). The store key TTL
	// follows the configured session lifetime.
	sessionTTL, err := cfg.Session.ParseTTL()
	if err != nil || sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}
	store := session.NewRedisStore(session.RedisOptions{
		Addr:       cfg.Redis.Host,
		Password:   cfg.Redis.Password,
		DB:         cfg.Redis.DB,
		TLS:        cfg.Redis.TLS,
		CAFilePath: cfg.Redis.CAFilePath,
		KeyPrefix:  cfg.InstanceName,
		TTL:        sessionTTL,
	})
	defer store.Close()

	// Set up the Kubernetes clientset from the in-cluster service account.
	clientset, err := k8s.NewInClusterClientset()
	if err != nil {
		return fmt.Errorf("init kubernetes clients: %w", err)
	}
	log.Info("kubernetes clients initialized",
		"namespace", cfg.Namespace,
	)

	// Set up the profile store and start watching Profile CRs.
	profileStore := k8s.NewProfileStore().WithLogger(log)
	go profileStore.Run(ctx, clientset.Profile, cfg.Namespace, 0)
	log.Info("profile watcher started", "namespace", cfg.Namespace)

	// Set up the workspace manager.
	wm := k8s.NewWorkspaceManager(clientset, cfg)

	// Set up the inactivity watcher.
	checkInterval, _ := time.ParseDuration(cfg.Inactivity.CheckInterval)
	if checkInterval == 0 {
		checkInterval = 60 * time.Second
	}

	// The connection-count source polls the Envoy admin /stats endpoint of
	// every workspace pod for active HTTPS connections.
	activitySource := inactivity.NewConnectionCountActivity(workspacePodLister{wm: wm}, log, nil)
	watcher := inactivity.NewWatcher(activitySource, wm.StopWorkspace, checkInterval, log).WithAuditLogger(log)
	reconciler := inactivity.NewRegistrationReconciler(watcher, workspaceDelayLister{wm: wm, profiles: profileStore}, log)

	// startInactivityLoops starts the three inactivity loops; stop cancels
	// them. With leader election the loops run only on the lease holder.
	startInactivityLoops := func(ctx context.Context) {
		go activitySource.Run(ctx, checkInterval)
		log.Info("inactivity activity source: envoy connection-count",
			"adminPort", inactivity.DefaultEnvoyAdminPort,
			"stat", inactivity.DefaultConnectionStatName,
		)
		go watcher.Run(ctx)
		// Keep the watcher's registrations in sync with the running
		// workspaces and their profile's inactivity stop delay.
		go reconciler.Run(ctx, checkInterval)
	}

	if cfg.Inactivity.LeaderElection.Enabled {
		leaseDuration, _ := time.ParseDuration(cfg.Inactivity.LeaderElection.LeaseDuration)
		renewDeadline, _ := time.ParseDuration(cfg.Inactivity.LeaderElection.RenewDeadline)
		retryPeriod, _ := time.ParseDuration(cfg.Inactivity.LeaderElection.RetryPeriod)
		releaseOnCancel := cfg.Inactivity.LeaderElection.ReleaseOnCancel == nil || *cfg.Inactivity.LeaderElection.ReleaseOnCancel
		leaseNamespace := leader.EnsureLeaseNamespaceDefault(cfg.Inactivity.LeaderElection.LeaseNamespace)

		election := leader.NewElection(
			leaseNamespace,
			cfg.Inactivity.LeaderElection.LeaseName,
			leader.PodIdentity(),
			leaseDuration, renewDeadline, retryPeriod,
			releaseOnCancel,
			log,
		)

		// The inactivity loops run only while this replica holds the
		// lease: onStartedLeading starts them with a context cancelled as
		// soon as leadership is lost.
		go func() {
			if err := election.Run(ctx, clientset.LeaderElectionClient(), clientset.LeaderElectionRecorder(), startInactivityLoops, func() {}); err != nil {
				log.Error(err, "inactivity leader election failed")
			}
		}()
	} else {
		startInactivityLoops(ctx)
	}

	// Start the metrics server (optional dedicated /metrics endpoint).
	var metricsServer *http.Server
	if cfg.Metrics.Enabled {
		m := metrics.New()

		mux := http.NewServeMux()
		mux.Handle("/metrics", m.Handler())

		metricsServer = &http.Server{
			Addr:        cfg.Metrics.ListenAddr,
			Handler:     mux,
			ReadTimeout: 10 * time.Second,
		}

		go func() {
			log.Info("metrics server starting",
				"addr", cfg.Metrics.ListenAddr,
				"tls", cfg.Metrics.TLS,
			)
			var err error
			if cfg.Metrics.TLS {
				// Reuse the CodX server certificate.
				err = metricsServer.ListenAndServeTLS(cfg.HTTP.TLSCertFile, cfg.HTTP.TLSKeyFile)
			} else {
				err = metricsServer.ListenAndServe()
			}
			if err != nil && err != http.ErrServerClosed {
				log.Error(err, "metrics server error")
			}
		}()

		// Periodically refresh the workspace and health gauges.
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()

			refresh := func() {
				counts, err := wm.CountWorkspaces(ctx)
				if err != nil {
					log.Error(err, "failed to refresh workspace metrics")
					m.SetHealthy(false)
					return
				}
				m.SetWorkspaceCounts(counts.Running, counts.Pending)
				m.SetHealthy(true)
			}

			refresh()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					refresh()
				}
			}
		}()
	}

	// Set up the web server (handler wrapped with the otelhttp middleware
	// for server spans and W3C trace context extraction).
	webServer := web.New(cfg, auth, store, profileStore, wm, nil).WithLogger(log)

	server := &http.Server{
		Addr:         cfg.HTTP.ListenAddr,
		Handler:      tracing.Handler("codx", webServer.Handler()),
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
	if metricsServer != nil {
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			log.Error(err, "metrics server graceful shutdown failed")
		}
	}

	// Flush remaining spans to the collector.
	if err := tracing.Shutdown(tracingShutdown); err != nil {
		log.Error(err, "tracing shutdown failed")
	}

	log.Info("CodX server stopped")
	return nil
}

// workspacePodLister adapts the k8s WorkspaceManager to the inactivity
// package's WorkspacePodLister interface.
type workspacePodLister struct {
	wm *k8s.WorkspaceManager
}

// ListWorkspacePods returns the running workspace pods with their slug and IP,
// as expected by the connection-count inactivity source.
func (l workspacePodLister) ListWorkspacePods(ctx context.Context) ([]inactivity.WorkspacePod, error) {
	pods, err := l.wm.ListWorkspacePods(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]inactivity.WorkspacePod, 0, len(pods))
	for _, pod := range pods {
		slug := pod.Labels[k8s.LabelInstance]
		if slug == "" {
			continue
		}
		out = append(out, inactivity.WorkspacePod{Slug: slug, PodIP: pod.Status.PodIP})
	}
	return out, nil
}

// workspaceDelayLister resolves each running workspace's inactivity stop
// delay from the profile recorded on its pod, as expected by the watcher
// registration reconciler.
type workspaceDelayLister struct {
	wm       *k8s.WorkspaceManager
	profiles *k8s.ProfileStore
}

// ListWorkspaceDelays returns the running workspaces mapped to the
// InactivityStopDelaySeconds of their profile. A zero delay means the
// workspace is never stopped. Pods without a resolvable profile are skipped.
func (l workspaceDelayLister) ListWorkspaceDelays(ctx context.Context) (map[string]time.Duration, error) {
	pods, err := l.wm.ListWorkspacePods(ctx)
	if err != nil {
		return nil, err
	}
	delays := make(map[string]time.Duration, len(pods))
	for _, pod := range pods {
		slug := pod.Labels[k8s.LabelInstance]
		profileName := pod.Labels[k8s.LabelProfile]
		if slug == "" || profileName == "" {
			continue
		}
		profile := l.profiles.Get(profileName)
		if profile == nil {
			// The profile CR is not (yet) in the cache; retry on the next
			// reconcile once the profile store reloads.
			continue
		}
		delays[slug] = time.Duration(profile.Spec.InactivityStopDelaySeconds) * time.Second
	}
	return delays, nil
}
