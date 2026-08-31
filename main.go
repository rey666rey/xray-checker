package main

import (
	"context"
	"net/http"
	"strings"
	"time"
	"xray-checker/alerts"
	"xray-checker/checker"
	"xray-checker/config"
	"xray-checker/logger"
	"xray-checker/metrics"
	"xray-checker/models"
	"xray-checker/subscription"
	"xray-checker/web"
	"xray-checker/xray"

	"github.com/go-co-op/gocron"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	version   = "unknown"
	startTime = time.Now()
)

func main() {
	config.Parse(version)

	logLevel := logger.ParseLevel(config.CLIConfig.LogLevel)
	logger.SetLevel(logLevel)

	logger.Startup("Xray Checker %s", version)
	if logLevel == logger.LevelNone {
		logger.Startup("Log level: none (silent mode)")
	}

	if err := web.InitAssetLoader(config.CLIConfig.Web.CustomAssetsPath); err != nil {
		logger.Fatal("Failed to initialize custom assets: %v", err)
	}

	geoManager := xray.NewGeoFileManager("")
	if err := geoManager.EnsureGeoFiles(); err != nil {
		logger.Fatal("Failed to ensure geo files: %v", err)
	}

	configFile := "xray_config.json"
	proxyConfigs, err := subscription.InitializeConfiguration(configFile, version)
	if err != nil {
		logger.Fatal("Error initializing configuration: %v", err)
	}

	logger.Info("Loaded %d proxy configurations", len(*proxyConfigs))
	endpointPool := checker.NewEndpointPool(*proxyConfigs)

	if config.CLIConfig.Web.Public {
		if name := subscription.GetSubscriptionName(); name != "" {
			logger.Info("Subscription name for public status page: %s", name)
		}
	} else {
		subNames := web.CollectSubscriptionNames(*proxyConfigs)
		if len(subNames) > 0 {
			logger.Info("Subscriptions: %s", strings.Join(subNames, ", "))
		}
	}

	if logLevel == logger.LevelDebug {
		logger.Debug("=== Parsed Proxy Configurations ===")
		for _, pc := range *proxyConfigs {
			logger.Debug("%s", pc.DebugString())
		}
	}

	xrayRunner := xray.NewRunner(configFile)
	if err := xrayRunner.Start(); err != nil {
		logger.Fatal("Error starting Xray: %v", err)
	}

	defer func() {
		if err := xrayRunner.Stop(); err != nil {
			logger.Error("Error stopping Xray: %v", err)
		}
	}()

	proxyChecker := checker.NewProxyChecker(
		*proxyConfigs,
		config.CLIConfig.Xray.StartPort,
		config.CLIConfig.Proxy.IpCheckUrl,
		config.CLIConfig.Proxy.Timeout,
		config.CLIConfig.Proxy.StatusCheckUrl,
		config.CLIConfig.Proxy.DownloadUrl,
		config.CLIConfig.Proxy.DownloadTimeout,
		config.CLIConfig.Proxy.DownloadMinSize,
		config.CLIConfig.Proxy.CheckMethod,
		config.CLIConfig.Proxy.CheckConcurrency,
	)
	proxyChecker.SetEndpointPool(endpointPool)
	proxyChecker.SetNetworkStatusFile(
		config.CLIConfig.NetworkStatusFile,
		time.Duration(config.CLIConfig.NetworkStatusMaxAge)*time.Second,
	)
	proxyChecker.SetURLTestOptions(
		config.CLIConfig.Proxy.URLTestUrl,
		config.CLIConfig.Proxy.URLTestExpected,
		config.CLIConfig.Proxy.URLTestAttempts,
		config.CLIConfig.Proxy.RetryTimeout,
		config.CLIConfig.Proxy.RetryConcurrency,
	)
	if err := proxyChecker.SetResultsFile(config.CLIConfig.ResultsFile); err != nil {
		logger.Warn("Could not restore saved proxy results: %v", err)
	}
	if err := proxyChecker.SetMonitorFile(config.CLIConfig.NodeHistoryFile); err != nil {
		logger.Warn("Could not restore node repair history: %v", err)
	}
	if err := proxyChecker.SetDiagnosisFile(config.CLIConfig.NodeDiagnosisFile); err != nil {
		logger.Warn("Could not restore node diagnosis history: %v", err)
	}
	if err := proxyChecker.SetAccessCheckFile(config.CLIConfig.AccessCheckFile); err != nil {
		logger.Warn("Could not restore access-check history: %v", err)
	}
	alertManager, err := alerts.NewManager(
		proxyChecker,
		config.CLIConfig.Xray.StartPort,
		config.CLIConfig.Alerts.SettingsFile,
		config.CLIConfig.Alerts.TelegramToken,
		config.CLIConfig.Alerts.TelegramChat,
		config.CLIConfig.Alerts.TelegramProxy,
	)
	if err != nil {
		logger.Fatal("Could not initialize Telegram alerts: %v", err)
	}
	alertManager.Start(context.Background())

	// The collector renders metrics from the checker's current proxy snapshot on
	// each scrape, so custom metricsLabels (#124) can change across subscription
	// updates without resetting other series.
	registry := prometheus.NewRegistry()
	registry.MustRegister(metrics.NewCollector(config.CLIConfig.Metrics.Instance, proxyChecker))

	runCheckIteration := func() {
		logger.Info("Starting proxy check iteration")
		start := time.Now()
		proxyChecker.CheckAllProxies()
		elapsed := time.Since(start)

		// Warn if a cycle overruns the interval: with PROXY_CHECK_CONCURRENCY set,
		// a large/slow proxy set can take longer than PROXY_CHECK_INTERVAL, so checks
		// (and metrics) effectively run less often than configured.
		if interval := config.CLIConfig.Proxy.CheckInterval; interval > 0 && elapsed > time.Duration(interval)*time.Second {
			// When a concurrency cap is set, raising it (or the interval) helps. When
			// unlimited (0), the cycle is already as parallel as it gets, so the only
			// useful lever is a longer interval.
			if config.CLIConfig.Proxy.CheckConcurrency > 0 {
				logger.Warn("Check cycle took %s, longer than PROXY_CHECK_INTERVAL=%ds — raise PROXY_CHECK_CONCURRENCY or PROXY_CHECK_INTERVAL", elapsed.Round(time.Second), interval)
			} else {
				logger.Warn("Check cycle took %s, longer than PROXY_CHECK_INTERVAL=%ds — raise PROXY_CHECK_INTERVAL", elapsed.Round(time.Second), interval)
			}
		}

		if config.CLIConfig.Metrics.PushURL != "" {
			pushConfig, err := metrics.ParseURL(config.CLIConfig.Metrics.PushURL)
			if err != nil {
				logger.Error("Error parsing push URL: %v", err)
				return
			}

			if pushConfig != nil {
				if err := metrics.PushMetrics(pushConfig, registry); err != nil {
					logger.Error("Error pushing metrics: %v", err)
				}
			}
		}
	}

	if config.CLIConfig.RunOnce {
		runCheckIteration()
		logger.Info("Check completed")
		return
	}

	if config.CLIConfig.Proxy.InitialCheckOnly {
		// Keep serving the in-memory result after the initial batched sweep. Running
		// in a goroutine makes the dashboard available while that first sweep fills in.
		if proxyChecker.HasCompleteResults() {
			logger.Info("Keeping the completed proxy result snapshot; initial full check skipped")
		} else {
			go runCheckIteration()
		}
	} else {
		checkScheduler := gocron.NewScheduler(time.UTC)
		// SingletonMode: if a check cycle overruns the interval, the next tick is skipped
		// instead of starting a second concurrent cycle. Without a concurrency limit a
		// cycle is bounded by PROXY_TIMEOUT so this rarely triggers, but with
		// PROXY_CHECK_CONCURRENCY a slow cycle degrades to "runs less often" rather than
		// piling up overlapping runs.
		checkScheduler.Every(config.CLIConfig.Proxy.CheckInterval).Seconds().SingletonMode().Do(func() {
			runCheckIteration()
		})
		checkScheduler.StartAsync()
	}
	// Targeted monitoring continues even in initial-check-only mode: healthy nodes
	// are staggered, while suspected/changed nodes follow short confirmation rounds.
	proxyChecker.StartMonitorScheduler(10 * time.Second)

	if config.CLIConfig.Subscription.Update {
		var pendingMassFingerprint string
		var pendingMassConfirmations int
		updateScheduler := gocron.NewScheduler(time.UTC)
		updateScheduler.Every(config.CLIConfig.Subscription.UpdateInterval).Seconds().WaitForSchedule().SingletonMode().Do(func() {
			logger.Info("Checking subscriptions for updates...")
			newConfigs, err := subscription.ReadFromMultipleSources(config.CLIConfig.Subscription.URLs)
			if err != nil {
				logger.Error("Error fetching subscriptions: %v", err)
				return
			}

			if config.CLIConfig.Proxy.ResolveDomains {
				resolved, err := subscription.ResolveDomainsForConfigs(newConfigs)
				if err != nil {
					logger.Error("Error resolving domains: %v", err)
				} else {
					newConfigs = resolved
				}
			}

			// A panel response is a sample of a many-to-many topology: one host may
			// select a different node on every request. Merge several immediate samples
			// into the long-lived endpoint pool instead of waiting for one Server to
			// repeat five times and discarding the other legitimate members.
			reads := [][]*models.ProxyConfig{newConfigs}
			sampleCount := config.CLIConfig.Subscription.PoolSamples
			if sampleCount < 1 {
				sampleCount = 1
			}
			for sample := 1; sample < sampleCount; sample++ {
				nextSample, sampleErr := subscription.ReadFromMultipleSources(config.CLIConfig.Subscription.URLs)
				if sampleErr != nil {
					logger.Warn("Subscription pool sample %d/%d failed; continuing: %v", sample+1, sampleCount, sampleErr)
					continue
				}
				if config.CLIConfig.Proxy.ResolveDomains {
					resolved, err := subscription.ResolveDomainsForConfigs(nextSample)
					if err != nil {
						logger.Warn("Subscription pool sample %d/%d DNS resolution failed; continuing: %v", sample+1, sampleCount, err)
						continue
					}
					nextSample = resolved
				}
				reads = append(reads, nextSample)
			}
			poolStats := checker.EndpointPoolStats{}
			newConfigs, poolStats = endpointPool.Observe(reads...)
			if poolStats.Added > 0 || poolStats.Updated > 0 || poolStats.Detached > 0 {
				logger.Info("Endpoint pool: %d bindings, %d added, %d updated, %d detached, %d temporarily missing",
					poolStats.Bindings, poolStats.Added, poolStats.Updated, poolStats.Detached, poolStats.Missing)
			}

			if !xray.IsConfigsEqual(*proxyConfigs, newConfigs) {
				preflight := checker.PlanProxyUpdate(*proxyConfigs, newConfigs)
				changedCount := preflight.Count(checker.ProxyChanged)
				for _, change := range preflight.Changes {
					if change.Kind == checker.ProxyChanged {
						logger.Info("Subscription change sample fields: %s", strings.Join(change.ChangedFields, ", "))
						break
					}
				}
				massChange := changedCount >= 100 &&
					preflight.Count(checker.ProxyAdded) == 0 && preflight.Count(checker.ProxyRemoved) == 0
				if massChange {
					fingerprint := checker.ProxySetRevisionFingerprint(newConfigs)
					if fingerprint == pendingMassFingerprint {
						pendingMassConfirmations++
					} else {
						pendingMassFingerprint = fingerprint
						pendingMassConfirmations = 1
					}
					for _, change := range preflight.Changes {
						if change.Kind == checker.ProxyChanged {
							logger.Warn("Large subscription diff sample changed fields: %s", strings.Join(change.ChangedFields, ", "))
							break
						}
					}
					if pendingMassConfirmations < 2 {
						logger.Warn("Deferring large subscription diff (%d changed nodes) until the same revision is returned twice", changedCount)
						return
					}
					logger.Warn("Large subscription diff confirmed twice; applying %d changed nodes in bounded batches", changedCount)
				} else {
					pendingMassFingerprint = ""
					pendingMassConfirmations = 0
				}
				plan, err := updateConfiguration(newConfigs, proxyConfigs, xrayRunner, proxyChecker)
				if err != nil {
					logger.Error("Error updating configuration: %v", err)
				} else {
					logger.Info("Subscription diff: %d added, %d changed, %d renamed, %d removed",
						plan.Count(checker.ProxyAdded), plan.Count(checker.ProxyChanged),
						plan.Count(checker.ProxyRenamed), plan.Count(checker.ProxyRemoved))
					if err := proxyChecker.CheckUpdatedProxies(plan.ProxiesToCheck()); err != nil {
						logger.Warn("Could not verify updated nodes immediately: %v", err)
					}
					proxyChecker.PruneStaleResults()
				}
			} else {
				logger.Info("Subscriptions checked, no changes")
			}
			if changed := proxyChecker.RefreshResolvedIPs(); len(changed) > 0 {
				if err := proxyChecker.CheckUpdatedProxies(changed); err != nil {
					logger.Warn("Could not verify nodes after DNS change: %v", err)
				}
			}
		})
		updateScheduler.StartAsync()
	}

	mux, err := web.NewPrefixServeMux(config.CLIConfig.Metrics.BasePath)
	if err != nil {
		logger.Fatal("Error creating web server: %v", err)
	}
	mux.Handle("/health", web.HealthHandler())
	mux.Handle("/static/", web.StaticHandler())
	mux.Handle("/api/v1/public/proxies", web.APIPublicProxiesHandler(proxyChecker))

	web.RegisterConfigEndpoints(*proxyConfigs, proxyChecker, config.CLIConfig.Xray.StartPort)

	protectedHandler := http.NewServeMux()
	protectedHandler.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	protectedHandler.Handle("/config/", web.ConfigStatusHandler(proxyChecker))
	protectedHandler.Handle("/api/v1/proxies/", web.APIProxyHandler(proxyChecker, config.CLIConfig.Xray.StartPort))
	protectedHandler.Handle("/api/v1/proxies", web.APIProxiesHandler(proxyChecker, config.CLIConfig.Xray.StartPort))
	protectedHandler.Handle("/api/v1/nodes/", web.APINodesHandler(proxyChecker, config.CLIConfig.Xray.StartPort))
	protectedHandler.Handle("/api/v1/nodes", web.APINodesHandler(proxyChecker, config.CLIConfig.Xray.StartPort))
	protectedHandler.Handle("/api/v1/access-checks", web.APIAccessChecksHandler(proxyChecker))
	protectedHandler.Handle("/api/v1/access-checks/", web.APIAccessChecksHandler(proxyChecker))
	protectedHandler.Handle("/api/v1/config", web.APIConfigHandler(proxyChecker))
	protectedHandler.Handle("/api/v1/status", web.APIStatusHandler(proxyChecker))
	protectedHandler.Handle("/api/v1/system/info", web.APISystemInfoHandler(version, startTime))
	protectedHandler.Handle("/api/v1/system/ip", web.APISystemIPHandler(proxyChecker))
	protectedHandler.Handle("/api/v1/network", web.APINetworkStatusHandler(proxyChecker))
	protectedHandler.Handle("/api/v1/alerts/telegram", web.TelegramAlertsHandler(alertManager))
	protectedHandler.Handle("/api/v1/alerts/telegram/", web.TelegramAlertsHandler(alertManager))
	protectedHandler.Handle("/api/v1/docs", web.APIDocsHandler())
	protectedHandler.Handle("/api/v1/openapi.yaml", web.APIOpenAPIHandler())

	if config.CLIConfig.Web.Public {
		mux.Handle("/", web.IndexHandler(version, proxyChecker))
		mux.Handle("/config/", web.ConfigStatusHandler(proxyChecker))
		middlewareHandler := web.BasicAuthMiddleware(
			config.CLIConfig.Metrics.Username,
			config.CLIConfig.Metrics.Password,
		)(protectedHandler)
		mux.Handle("/metrics", middlewareHandler)
		mux.Handle("/api/", middlewareHandler)
	} else if config.CLIConfig.Metrics.Protected {
		protectedHandler.Handle("/", web.IndexHandler(version, proxyChecker))
		middlewareHandler := web.BasicAuthMiddleware(
			config.CLIConfig.Metrics.Username,
			config.CLIConfig.Metrics.Password,
		)(protectedHandler)
		mux.Handle("/", middlewareHandler)
	} else {
		protectedHandler.Handle("/", web.IndexHandler(version, proxyChecker))
		mux.Handle("/", protectedHandler)
	}

	if !config.CLIConfig.RunOnce {
		logger.Info("Server listening on %s:%s%s",
			config.CLIConfig.Metrics.Host,
			config.CLIConfig.Metrics.Port,
			config.CLIConfig.Metrics.BasePath,
		)
		if err := http.ListenAndServe(config.CLIConfig.Metrics.Host+":"+config.CLIConfig.Metrics.Port, mux); err != nil {
			logger.Fatal("Error starting server: %v", err)
		}
	}
}

func updateConfiguration(newConfigs []*models.ProxyConfig, currentConfigs *[]*models.ProxyConfig,
	xrayRunner *xray.Runner, proxyChecker *checker.ProxyChecker) (checker.ProxyUpdatePlan, error) {

	logger.Info("Subscription changed, updating configuration...")
	// First pass assigns carried logical IDs before Xray preparation. The final
	// plan is rebuilt after validation in case Xray excludes an invalid node.
	checker.PlanProxyUpdate(*currentConfigs, newConfigs)

	xray.PrepareProxyConfigs(newConfigs)

	configFile := "xray_config.json"
	configGenerator := xray.NewConfigGenerator()
	validProxies, err := configGenerator.GenerateValidatedConfig(
		newConfigs,
		config.CLIConfig.Xray.StartPort,
		configFile,
		config.CLIConfig.Xray.LogLevel,
	)
	if err != nil {
		return checker.ProxyUpdatePlan{}, err
	}
	newConfigs = validProxies
	plan := checker.PlanProxyUpdate(*currentConfigs, newConfigs)

	if err := proxyChecker.WithChecksPaused(func() error {
		if err := xrayRunner.Stop(); err != nil {
			return err
		}
		if err := xrayRunner.Start(); err != nil {
			return err
		}
		proxyChecker.ApplyProxyUpdate(newConfigs, plan)
		return nil
	}); err != nil {
		return checker.ProxyUpdatePlan{}, err
	}

	*currentConfigs = newConfigs

	web.RegisterConfigEndpoints(newConfigs, proxyChecker, config.CLIConfig.Xray.StartPort)

	logger.Info("Configuration updated: %d proxies", len(newConfigs))
	return plan, nil
}
