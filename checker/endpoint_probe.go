package checker

import (
	"context"
	"errors"
	"hash/fnv"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"xray-checker/logger"
	"xray-checker/models"
)

type endpointProbeConfig struct {
	interval     time.Duration
	timeout      time.Duration
	confirmDelay time.Duration
	concurrency  int
}

type endpointProbeDialFunc func(context.Context, string, string, time.Duration) error

type endpointProbeTarget struct {
	key     string
	address string
	proxies []*models.ProxyConfig
}

// SetEndpointProbeOptions configures a cheap first-stage liveness check. A TCP
// failure never changes monitor state by itself: only the existing end-to-end
// proxy check is allowed to record a node failure.
func (pc *ProxyChecker) SetEndpointProbeOptions(interval, timeout, confirmDelay time.Duration, concurrency int) {
	pc.endpointProbeMu.Lock()
	defer pc.endpointProbeMu.Unlock()
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if confirmDelay < 0 {
		confirmDelay = 0
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	pc.endpointProbeConfig = endpointProbeConfig{
		interval: interval, timeout: timeout, confirmDelay: confirmDelay, concurrency: concurrency,
	}
	if pc.endpointProbeNext == nil {
		pc.endpointProbeNext = make(map[string]time.Time)
	}
	if pc.endpointProbeDial == nil {
		pc.endpointProbeDial = dialEndpointThroughInterface
	}
}

// StartEndpointProbeScheduler staggers unique server:port probes across the
// configured interval. Suspected nodes are left to the normal confirmation
// scheduler, preventing the fast path from duplicating full proxy checks.
func (pc *ProxyChecker) StartEndpointProbeScheduler(tick time.Duration) {
	pc.endpointProbeMu.Lock()
	config := pc.endpointProbeConfig
	pc.endpointProbeMu.Unlock()
	if config.interval <= 0 {
		return
	}
	if tick <= 0 {
		tick = 5 * time.Second
	}
	logger.Info("TCP endpoint probes enabled: interval=%s timeout=%s confirm=%s concurrency=%d",
		config.interval, config.timeout, config.confirmDelay, config.concurrency)
	go func() {
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for now := range ticker.C {
			pc.checkDueEndpointProbes(now)
		}
	}()
}

func (pc *ProxyChecker) checkDueEndpointProbes(now time.Time) {
	status := pc.GetNetworkStatus()
	if !status.Ready {
		return
	}
	targets := pc.endpointProbeTargets()
	if len(targets) == 0 {
		return
	}

	pc.endpointProbeMu.Lock()
	config := pc.endpointProbeConfig
	if config.interval <= 0 {
		pc.endpointProbeMu.Unlock()
		return
	}
	seen := make(map[string]bool, len(targets))
	due := make([]endpointProbeTarget, 0)
	for _, target := range targets {
		seen[target.key] = true
		next, exists := pc.endpointProbeNext[target.key]
		if !exists {
			next = now.Add(endpointProbeInitialDelay(target.key, config.interval))
			pc.endpointProbeNext[target.key] = next
		}
		if !next.After(now) {
			due = append(due, target)
			pc.endpointProbeNext[target.key] = now.Add(config.interval)
		}
	}
	for key := range pc.endpointProbeNext {
		if !seen[key] {
			delete(pc.endpointProbeNext, key)
		}
	}
	dial := pc.endpointProbeDial
	pc.endpointProbeMu.Unlock()

	if len(due) == 0 {
		return
	}
	failed := pc.confirmFailedEndpointProbes(due, status.Interface, config, dial)
	if len(failed) == 0 || !pc.GetNetworkStatus().Ready {
		return
	}
	failed = pc.currentEndpointProbeProxies(failed)
	if len(failed) == 0 {
		return
	}
	logger.Info("TCP endpoint probe confirmed %d unreachable binding(s); running end-to-end validation", len(failed))
	if err := pc.checkProxySet(failed, CheckReasonTCPProbe); err != nil && !errors.Is(err, ErrDiagnosisPriority) {
		logger.Warn("TCP-triggered proxy validation skipped: %v", err)
	}
}

func (pc *ProxyChecker) currentEndpointProbeProxies(proxies []*models.ProxyConfig) []*models.ProxyConfig {
	wanted := make(map[string]bool, len(proxies))
	for _, proxy := range proxies {
		key := proxy.StableID
		if key == "" {
			key = proxy.GenerateStableID()
		}
		wanted[key] = true
	}
	pc.mu.RLock()
	candidates := make([]*models.ProxyConfig, 0, len(proxies))
	for _, proxy := range pc.proxies {
		key := proxy.StableID
		if key == "" {
			key = proxy.GenerateStableID()
		}
		if wanted[key] {
			candidates = append(candidates, proxy)
		}
	}
	pc.mu.RUnlock()

	result := make([]*models.ProxyConfig, 0, len(candidates))
	pc.monitorMu.RLock()
	for _, proxy := range candidates {
		if node := pc.monitor[proxy.LogicalID]; node != nil && tcpProbeEligibleState(node.State) {
			result = append(result, proxy)
		}
	}
	pc.monitorMu.RUnlock()
	return result
}

func (pc *ProxyChecker) endpointProbeTargets() []endpointProbeTarget {
	pc.mu.RLock()
	proxies := append([]*models.ProxyConfig(nil), pc.proxies...)
	pc.mu.RUnlock()

	groups := make(map[string]*endpointProbeTarget)
	pc.monitorMu.RLock()
	for _, proxy := range proxies {
		if isUDPBinding(proxy) {
			continue
		}
		node := pc.monitor[proxy.LogicalID]
		if node == nil || !tcpProbeEligibleState(node.State) {
			continue
		}
		address := net.JoinHostPort(strings.Trim(strings.TrimSpace(proxy.Server), "[]"), strconv.Itoa(proxy.Port))
		key := strings.ToLower(address)
		target := groups[key]
		if target == nil {
			target = &endpointProbeTarget{key: key, address: address}
			groups[key] = target
		}
		target.proxies = append(target.proxies, proxy)
	}
	pc.monitorMu.RUnlock()

	result := make([]endpointProbeTarget, 0, len(groups))
	for _, target := range groups {
		result = append(result, *target)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].key < result[j].key })
	return result
}

func tcpProbeEligibleState(state NodeState) bool {
	return state == NodeHealthy || state == NodeFixed || state == NodeUnstable
}

func endpointProbeInitialDelay(key string, interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return time.Duration(uint64(h.Sum32()) % uint64(interval))
}

func (pc *ProxyChecker) confirmFailedEndpointProbes(targets []endpointProbeTarget, interfaceName string, config endpointProbeConfig, dial endpointProbeDialFunc) []*models.ProxyConfig {
	if dial == nil {
		dial = dialEndpointThroughInterface
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, config.concurrency)
	confirmed := make(chan endpointProbeTarget, len(targets))
	for _, target := range targets {
		wg.Add(1)
		go func(target endpointProbeTarget) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if dial(context.Background(), interfaceName, target.address, config.timeout) == nil {
				return
			}
			if config.confirmDelay > 0 {
				time.Sleep(config.confirmDelay)
			}
			if !pc.GetNetworkStatus().Ready {
				return
			}
			if dial(context.Background(), interfaceName, target.address, config.timeout) != nil {
				confirmed <- target
			}
		}(target)
	}
	wg.Wait()
	close(confirmed)

	unique := make(map[string]*models.ProxyConfig)
	for target := range confirmed {
		for _, proxy := range target.proxies {
			key := proxy.StableID
			if key == "" {
				key = proxy.GenerateStableID()
			}
			unique[key] = proxy
		}
	}
	result := make([]*models.ProxyConfig, 0, len(unique))
	for _, proxy := range unique {
		result = append(result, proxy)
	}
	sort.Slice(result, func(i, j int) bool {
		return proxyAddress(result[i]) < proxyAddress(result[j])
	})
	return result
}

func dialEndpointThroughInterface(parent context.Context, interfaceName, address string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	connection, err := newInterfaceDialer(interfaceName, timeout).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	_ = connection.Close()
	return nil
}
