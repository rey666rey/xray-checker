package main

import (
	"fmt"
	"strings"
	"sync"

	"xray-checker/checker"
	"xray-checker/config"
	"xray-checker/logger"
	"xray-checker/models"
	"xray-checker/subscription"
	"xray-checker/xray"
)

// subscriptionUpdater serializes scheduled and user-triggered refreshes. A
// replacement verification can therefore force the exact same sampling and
// Xray reload path as the background scheduler without racing it.
type subscriptionUpdater struct {
	mu                       sync.Mutex
	configs                  *[]*models.ProxyConfig
	endpointPool             *checker.EndpointPool
	xrayRunner               *xray.Runner
	proxyChecker             *checker.ProxyChecker
	pendingMassFingerprint   string
	pendingMassConfirmations int
}

func (updater *subscriptionUpdater) Refresh() (checker.ProxyUpdatePlan, error) {
	updater.mu.Lock()
	defer updater.mu.Unlock()

	logger.Info("Checking subscriptions for updates...")
	newConfigs, err := subscription.ReadFromMultipleSources(config.CLIConfig.Subscription.URLs)
	if err != nil {
		return checker.ProxyUpdatePlan{}, fmt.Errorf("fetch subscriptions: %w", err)
	}

	newConfigs, _ = updater.resolveDomains(newConfigs, 1)
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
		var resolved bool
		nextSample, resolved = updater.resolveDomains(nextSample, sample+1)
		if !resolved && config.CLIConfig.Proxy.ResolveDomains {
			continue
		}
		reads = append(reads, nextSample)
	}

	poolStats := checker.EndpointPoolStats{}
	newConfigs, poolStats = updater.endpointPool.Observe(reads...)
	if poolStats.Added > 0 || poolStats.Updated > 0 || poolStats.Detached > 0 {
		logger.Info("Endpoint pool: %d bindings, %d added, %d updated, %d detached, %d temporarily missing",
			poolStats.Bindings, poolStats.Added, poolStats.Updated, poolStats.Detached, poolStats.Missing)
	}

	plan := checker.ProxyUpdatePlan{}
	if !xray.IsConfigsEqual(*updater.configs, newConfigs) {
		preflight := checker.PlanProxyUpdate(*updater.configs, newConfigs)
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
			if fingerprint == updater.pendingMassFingerprint {
				updater.pendingMassConfirmations++
			} else {
				updater.pendingMassFingerprint = fingerprint
				updater.pendingMassConfirmations = 1
			}
			for _, change := range preflight.Changes {
				if change.Kind == checker.ProxyChanged {
					logger.Warn("Large subscription diff sample changed fields: %s", strings.Join(change.ChangedFields, ", "))
					break
				}
			}
			if updater.pendingMassConfirmations < 2 {
				logger.Warn("Deferring large subscription diff (%d changed nodes) until the same revision is returned twice", changedCount)
				return checker.ProxyUpdatePlan{}, nil
			}
			logger.Warn("Large subscription diff confirmed twice; applying %d changed nodes in bounded batches", changedCount)
		} else {
			updater.pendingMassFingerprint = ""
			updater.pendingMassConfirmations = 0
		}

		plan, err = updateConfiguration(newConfigs, updater.configs, updater.xrayRunner, updater.proxyChecker)
		if err != nil {
			return checker.ProxyUpdatePlan{}, err
		}
		logger.Info("Subscription diff: %d added, %d changed, %d renamed, %d removed",
			plan.Count(checker.ProxyAdded), plan.Count(checker.ProxyChanged),
			plan.Count(checker.ProxyRenamed), plan.Count(checker.ProxyRemoved))
		if err := updater.proxyChecker.CheckUpdatedProxies(plan.ProxiesToCheck()); err != nil {
			logger.Warn("Could not verify updated nodes immediately: %v", err)
		}
		updater.proxyChecker.PruneStaleResults()
	} else {
		logger.Info("Subscriptions checked, no changes")
	}

	if changed := updater.proxyChecker.RefreshResolvedIPs(); len(changed) > 0 {
		if err := updater.proxyChecker.CheckUpdatedProxies(changed); err != nil {
			logger.Warn("Could not verify nodes after DNS change: %v", err)
		}
	}
	return plan, nil
}

func (updater *subscriptionUpdater) resolveDomains(proxies []*models.ProxyConfig, sample int) ([]*models.ProxyConfig, bool) {
	if !config.CLIConfig.Proxy.ResolveDomains {
		return proxies, true
	}
	resolved, err := subscription.ResolveDomainsForConfigs(proxies)
	if err != nil {
		if sample <= 1 {
			logger.Error("Error resolving domains: %v", err)
		} else {
			logger.Warn("Subscription pool sample %d DNS resolution failed; continuing: %v", sample, err)
		}
		return proxies, false
	}
	return resolved, true
}
