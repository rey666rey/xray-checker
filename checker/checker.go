package checker

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"xray-checker/logger"
	"xray-checker/metrics"
	"xray-checker/models"
)

type ProxyChecker struct {
	proxies             []*models.ProxyConfig
	startPort           int
	ipCheck             string
	currentIP           string
	httpClient          *http.Client
	results             sync.Map // proxyMetricLabels -> proxyResult
	ipInitialized       bool
	ipMu                sync.RWMutex
	ipCheckTimeout      int
	genMethodURL        string
	downloadURL         string
	downloadTimeout     int
	downloadMinSize     int64
	checkMethod         string
	checkConcurrency    int // max proxies checked in parallel per cycle; 0 = unlimited
	urlTestURL          string
	urlTestExpected     string
	urlTestAttempts     int
	retryTimeout        int
	retryConcurrency    int
	networkStatusFile   string
	networkStatusMaxAge time.Duration
	networkLogMu        sync.Mutex
	networkWaiting      bool
	resultsFile         string
	persistSignal       chan struct{}
	resultsComplete     atomic.Bool
	resumeFromSnapshot  atomic.Bool
	resumeWasComplete   atomic.Bool
	persistMu           sync.Mutex
	mu                  sync.RWMutex
}

// proxyResult is the latest check outcome for one proxy. Metrics are rendered from
// these at scrape time (a pull model), so there is no separate metric state to keep
// in sync and no series to delete — the metrics collector simply reflects whatever
// results exist for the current proxy set.
type proxyResult struct {
	status    bool
	unstable  bool
	latency   time.Duration
	lastCheck time.Time
}

func NewProxyChecker(proxies []*models.ProxyConfig, startPort int, ipCheckURL string, ipCheckTimeout int, genMethodURL string, downloadURL string, downloadTimeout int, downloadMinSize int64, checkMethod string, checkConcurrency int) *ProxyChecker {
	return &ProxyChecker{
		proxies:   proxies,
		startPort: startPort,
		ipCheck:   ipCheckURL,
		httpClient: &http.Client{
			Timeout: time.Second * time.Duration(ipCheckTimeout),
		},
		ipCheckTimeout:   ipCheckTimeout,
		genMethodURL:     genMethodURL,
		downloadURL:      downloadURL,
		downloadTimeout:  downloadTimeout,
		downloadMinSize:  downloadMinSize,
		checkMethod:      checkMethod,
		checkConcurrency: checkConcurrency,
	}
}

// SetURLTestOptions configures the fast app-style URL test and the slower retry
// pass used only for nodes that failed the first sweep.
func (pc *ProxyChecker) SetURLTestOptions(testURL, expected string, attempts, retryTimeout, retryConcurrency int) {
	pc.urlTestURL = strings.TrimSpace(testURL)
	pc.urlTestExpected = expected
	if attempts < 1 {
		attempts = 1
	}
	pc.urlTestAttempts = attempts
	if retryTimeout <= 0 {
		retryTimeout = pc.ipCheckTimeout
	}
	pc.retryTimeout = retryTimeout
	pc.retryConcurrency = retryConcurrency
}

func (pc *ProxyChecker) GetCurrentIP() (string, error) {
	status := pc.GetNetworkStatus()
	if status.Ready && status.PublicIP != "" {
		pc.setCurrentIP(status.PublicIP)
		return status.PublicIP, nil
	}

	pc.ipMu.RLock()
	if pc.ipInitialized && pc.currentIP != "" {
		currentIP := pc.currentIP
		pc.ipMu.RUnlock()
		return currentIP, nil
	}
	pc.ipMu.RUnlock()

	resp, err := pc.httpClient.Get(pc.ipCheck)
	if err != nil {
		return "", fmt.Errorf("error getting current IP: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %v", err)
	}

	currentIP := string(body)
	pc.setCurrentIP(currentIP)
	return currentIP, nil
}

func (pc *ProxyChecker) CheckProxy(proxy *models.ProxyConfig) {
	pc.checkProxyInternal(proxy)
}

// proxyMetricLabels is the full Prometheus label set for a proxy. It doubles as the
// in-memory map key, so series can be deleted exactly from their labels without
// parsing a packed string (proxy names may legitimately contain any character).
type proxyMetricLabels struct {
	protocol  string
	address   string
	name      string
	subName   string
	stableID  string
	groupName string
}

func proxyMetricKey(proxy *models.ProxyConfig) proxyMetricLabels {
	return proxyMetricLabels{
		protocol:  proxy.Protocol,
		address:   fmt.Sprintf("%s:%d", proxy.Server, proxy.Port),
		name:      proxy.Name,
		subName:   proxy.SubName,
		stableID:  proxy.StableID,
		groupName: proxy.GroupName,
	}
}

func (pc *ProxyChecker) checkProxyInternal(proxy *models.ProxyConfig) {
	pc.checkProxyInternalWithOptions(proxy, pc.ipCheckTimeout, false)
}

func (pc *ProxyChecker) checkProxyInternalWithOptions(proxy *models.ProxyConfig, timeout int, retryPhase bool) {
	for {
		pc.waitForNetwork()
		if !pc.checkProxyAttempt(proxy, timeout, retryPhase) {
			return
		}
	}
}

// checkProxyAttempt performs one proxy request. It returns true only when the
// attempt failed during a confirmed mobile-network outage and must be repeated
// after waitForNetwork observes recovery. Ordinary proxy failures are retained.
func (pc *ProxyChecker) checkProxyAttempt(proxy *models.ProxyConfig, timeout int, retryPhase bool) bool {
	if proxy.StableID == "" {
		proxy.StableID = proxy.GenerateStableID()
	}

	metricKey := proxyMetricKey(proxy)

	storeResult := func(status bool, unstable bool, latency time.Duration) {
		pc.storeResult(metricKey, proxyResult{
			status:    status,
			unstable:  unstable,
			latency:   latency,
			lastCheck: time.Now(),
		})
	}

	setFailed := func() {
		storeResult(false, false, 0)
	}

	proxyURL := fmt.Sprintf("socks5://127.0.0.1:%d", pc.startPort+proxy.Index)
	proxyURLParsed, err := url.Parse(proxyURL)
	if err != nil {
		logger.Error("Error parsing proxy URL %s: %v", proxyURL, err)
		setFailed()

		return false
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURLParsed),
			DisableKeepAlives: true,
		},
		Timeout: time.Second * time.Duration(timeout),
	}

	var checkSuccess bool
	var checkErr error
	var logMessage string
	var latency time.Duration
	var unstable bool

	if pc.checkMethod == "ip" {
		checkSuccess, logMessage, latency, checkErr = pc.checkByIP(client)
	} else if pc.checkMethod == "urltest" {
		checkSuccess, logMessage, latency, unstable, checkErr = pc.checkByURLTest(client)
	} else if pc.checkMethod == "status" {
		checkSuccess, logMessage, latency, checkErr = pc.checkByGen(client)
	} else if pc.checkMethod == "download" {
		checkSuccess, logMessage, latency, checkErr = pc.checkByDownload(client)
	} else {
		logger.Error("Invalid check method: %s", pc.checkMethod)
		return false
	}

	if checkErr != nil {
		if pc.retryAfterNetworkOutage() {
			logger.Warn("%s | Mobile connection was interrupted; retrying after recovery", proxy.Name)
			return true
		}
		logger.Error("%s | %v", proxy.Name, checkErr)
		setFailed()

		return false
	}

	if !checkSuccess {
		logger.Error("%s | Failed | %s | Latency: %s", proxy.Name, logMessage, latency)
		setFailed()
	} else {
		unstable = unstable || retryPhase
		logger.Result("%s | Success | %s | Latency: %s", proxy.Name, logMessage, latency)
		storeResult(true, unstable, latency)
	}

	return false
}

func (pc *ProxyChecker) checkByURLTest(client *http.Client) (bool, string, time.Duration, bool, error) {
	if pc.urlTestURL == "" {
		return false, "", 0, false, fmt.Errorf("URL test endpoint is not configured")
	}

	attempts := pc.urlTestAttempts
	if attempts < 1 {
		attempts = 1
	}
	var best time.Duration
	successes := 0
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		status, body, latency, err := timedProxyGET(client, pc.urlTestURL, 64*1024)
		if err != nil {
			lastErr = err
			continue
		}
		valid := status >= 200 && status < 300
		if pc.urlTestExpected != "" {
			valid = valid && strings.Contains(body, pc.urlTestExpected)
		}
		if !valid {
			lastErr = fmt.Errorf("URL test returned status %d or unexpected content", status)
			continue
		}
		successes++
		if best == 0 || latency < best {
			best = latency
		}
	}
	if successes > 0 {
		return true, fmt.Sprintf("URL test: %d/%d successful", successes, attempts), best, successes < attempts, nil
	}

	// ipify is deliberately a fallback: it is slower than the tiny Apple page,
	// but independently confirms that traffic really exited through the proxy.
	status, body, latency, err := timedProxyGET(client, pc.ipCheck, 256)
	if err != nil {
		if lastErr != nil {
			return false, "", 0, false, fmt.Errorf("URL test failed (%v); IP fallback failed (%w)", lastErr, err)
		}
		return false, "", 0, false, fmt.Errorf("IP fallback failed: %w", err)
	}
	proxyIP := strings.TrimSpace(body)
	if status < 200 || status >= 300 || net.ParseIP(proxyIP) == nil {
		return false, fmt.Sprintf("IP fallback returned invalid response (status %d)", status), latency, false, nil
	}
	currentIP := pc.getCurrentIP()
	if proxyIP == currentIP {
		return false, fmt.Sprintf("IP fallback used source IP %s", currentIP), latency, false, nil
	}
	return true, fmt.Sprintf("IP fallback succeeded: %s", proxyIP), latency, true, nil
}

func timedProxyGET(client *http.Client, target string, maxBody int64) (int, string, time.Duration, error) {
	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return 0, "", 0, err
	}
	var ttfb time.Duration
	start := time.Now()
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { ttfb = time.Since(start) }}
	req = req.WithContext(httptrace.WithClientTrace(context.Background(), trace))
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if ttfb == 0 {
		ttfb = time.Since(start)
	}
	if err != nil {
		return resp.StatusCode, "", ttfb, err
	}
	return resp.StatusCode, string(body), ttfb, nil
}

func (pc *ProxyChecker) checkByIP(client *http.Client) (bool, string, time.Duration, error) {
	req, err := http.NewRequest("GET", pc.ipCheck, nil)
	if err != nil {
		return false, "", 0, err
	}

	var ttfb time.Duration
	start := time.Now()
	trace := &httptrace.ClientTrace{
		GotFirstResponseByte: func() {
			ttfb = time.Since(start)
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(context.Background(), trace))

	resp, err := client.Do(req)
	if err != nil {
		return false, "", 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", ttfb, err
	}

	proxyIP := string(body)
	currentIP := pc.getCurrentIP()
	logMessage := fmt.Sprintf("Source IP: %s | Proxy IP: %s", currentIP, proxyIP)
	return proxyIP != currentIP, logMessage, ttfb, nil
}

func (pc *ProxyChecker) checkByGen(client *http.Client) (bool, string, time.Duration, error) {
	req, err := http.NewRequest("GET", pc.genMethodURL, nil)
	if err != nil {
		return false, "", 0, err
	}

	var ttfb time.Duration
	start := time.Now()
	trace := &httptrace.ClientTrace{
		GotFirstResponseByte: func() {
			ttfb = time.Since(start)
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(context.Background(), trace))

	resp, err := client.Do(req)
	if err != nil {
		return false, "", 0, err
	}
	defer resp.Body.Close()

	logMessage := fmt.Sprintf("Status: %d", resp.StatusCode)
	return resp.StatusCode >= 200 && resp.StatusCode < 300, logMessage, ttfb, nil
}

func (pc *ProxyChecker) checkByDownload(client *http.Client) (bool, string, time.Duration, error) {
	if pc.downloadURL == "" {
		return false, "Download URL not configured", 0, fmt.Errorf("download URL not configured")
	}

	req, err := http.NewRequest("GET", pc.downloadURL, nil)
	if err != nil {
		return false, "", 0, err
	}

	var ttfb time.Duration
	start := time.Now()
	trace := &httptrace.ClientTrace{
		GotFirstResponseByte: func() {
			ttfb = time.Since(start)
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(context.Background(), trace))

	downloadClient := &http.Client{
		Transport: client.Transport,
		Timeout:   time.Second * time.Duration(pc.downloadTimeout),
	}

	resp, err := downloadClient.Do(req)
	if err != nil {
		return false, "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Sprintf("HTTP status: %d", resp.StatusCode), ttfb, nil
	}

	totalBytes := int64(0)
	buffer := make([]byte, 8192)

	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			totalBytes += int64(n)
		}

		if totalBytes >= pc.downloadMinSize {
			break
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return false, fmt.Sprintf("Download error after %d bytes: %v", totalBytes, err), ttfb, nil
		}
	}

	success := totalBytes >= pc.downloadMinSize
	logMessage := fmt.Sprintf("Downloaded: %d bytes (min: %d)", totalBytes, pc.downloadMinSize)

	return success, logMessage, ttfb, nil
}

// UpdateProxies swaps in a new proxy set. Metrics are rendered from the current
// set at scrape time, so surviving proxies keep their last result (no blink to 0,
// the #148 regression) and removed proxies simply stop being emitted on the next
// scrape — no metric deletion needed. The caller should run an immediate check to
// populate the new proxies and may call PruneStaleResults to drop cached results
// for removed proxies.
func (pc *ProxyChecker) UpdateProxies(newProxies []*models.ProxyConfig) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.proxies = newProxies
}

// PruneStaleResults drops cached results for proxies no longer in the current set.
// This is memory hygiene only: stale results are never emitted (the snapshot
// iterates the current set), but pruning keeps the results map from growing across
// many subscription changes.
func (pc *ProxyChecker) PruneStaleResults() {
	pc.mu.RLock()
	currentKeys := make(map[proxyMetricLabels]struct{}, len(pc.proxies))
	for _, proxy := range pc.proxies {
		if proxy.StableID == "" {
			proxy.StableID = proxy.GenerateStableID()
		}
		currentKeys[proxyMetricKey(proxy)] = struct{}{}
	}
	pc.mu.RUnlock()

	pc.results.Range(func(key, _ interface{}) bool {
		if _, ok := currentKeys[key.(proxyMetricLabels)]; !ok {
			pc.results.Delete(key)
		}
		return true
	})
	pc.scheduleResultsPersist()
}

// MetricsSnapshot returns one ProxyMetric per current proxy that has a check
// result, attaching its custom metricsLabels. The metrics collector renders these
// at scrape time, so the exported series always match the current proxy set.
func (pc *ProxyChecker) MetricsSnapshot() []metrics.ProxyMetric {
	pc.mu.RLock()
	proxies := make([]*models.ProxyConfig, len(pc.proxies))
	copy(proxies, pc.proxies)
	pc.mu.RUnlock()

	out := make([]metrics.ProxyMetric, 0, len(proxies))
	for _, proxy := range proxies {
		if proxy.StableID == "" {
			proxy.StableID = proxy.GenerateStableID()
		}
		key := proxyMetricKey(proxy)
		v, ok := pc.results.Load(key)
		if !ok {
			// Not checked yet: no series until the first result, matching prior behavior.
			continue
		}
		r := v.(proxyResult)
		out = append(out, metrics.ProxyMetric{
			Protocol:     key.protocol,
			Address:      key.address,
			Name:         key.name,
			SubName:      key.subName,
			StableID:     key.stableID,
			GroupName:    key.groupName,
			CustomLabels: proxy.MetricsLabels,
			Online:       r.status,
			LatencyMs:    float64(r.latency.Milliseconds()),
		})
	}
	return out
}

func (pc *ProxyChecker) CheckAllProxies() {
	pc.markResultsComplete(false)
	pc.waitForNetwork()
	if _, err := pc.GetCurrentIP(); err != nil {
		logger.Warn("Error getting current IP: %v", err)
		return
	}

	pc.mu.RLock()
	proxiesToCheck := make([]*models.ProxyConfig, len(pc.proxies))
	copy(proxiesToCheck, pc.proxies)
	pc.mu.RUnlock()

	firstPass := proxiesToCheck
	resuming := pc.resumeFromSnapshot.Swap(false)
	resumeWasComplete := pc.resumeWasComplete.Swap(false)
	if resuming {
		firstPass = pc.uncheckedProxies(proxiesToCheck)
		logger.Info("Resuming interrupted check: %d unchecked proxies remain", len(firstPass))
	}
	runBoundedChecks(firstPass, pc.checkConcurrency, pc.checkProxyInternal)

	// Mobile links occasionally reset otherwise healthy proxy connections during
	// a large sweep. Match app-style URL tests by retrying only first-pass failures;
	// successes are never needlessly checked twice. A smaller retry batch reduces
	// NAT/radio pressure and any successful attempt becomes the retained result.
	if pc.checkMethod == "ip" || pc.checkMethod == "urltest" {
		retryCandidates := proxiesToCheck
		if resumeWasComplete {
			// A completed snapshot may have only a few unmatched nodes after a
			// subscription rename/rotation. Its old offline results were already
			// retried, so retry only the newly checked nodes.
			retryCandidates = firstPass
		}
		failed := pc.failedProxies(retryCandidates)
		if len(failed) > 0 {
			retryConcurrency := pc.retryConcurrency
			if retryConcurrency <= 0 {
				retryConcurrency = pc.checkConcurrency
				if retryConcurrency > 1 {
					retryConcurrency /= 2
				}
			}
			retryTimeout := pc.retryTimeout
			if retryTimeout <= 0 {
				retryTimeout = pc.ipCheckTimeout
			}
			logger.Info("Retrying %d first-pass failures with concurrency %d and timeout %ds", len(failed), retryConcurrency, retryTimeout)
			runBoundedChecks(failed, retryConcurrency, func(proxy *models.ProxyConfig) {
				pc.checkProxyInternalWithOptions(proxy, retryTimeout, true)
			})
		}
	}
	pc.markResultsComplete(true)
}

func (pc *ProxyChecker) uncheckedProxies(proxies []*models.ProxyConfig) []*models.ProxyConfig {
	unchecked := make([]*models.ProxyConfig, 0)
	for _, proxy := range proxies {
		if _, ok := pc.results.Load(proxyMetricKey(proxy)); !ok {
			unchecked = append(unchecked, proxy)
		}
	}
	return unchecked
}

func (pc *ProxyChecker) failedProxies(proxies []*models.ProxyConfig) []*models.ProxyConfig {
	failed := make([]*models.ProxyConfig, 0)
	for _, proxy := range proxies {
		result, ok := pc.results.Load(proxyMetricKey(proxy))
		if !ok || !result.(proxyResult).status {
			failed = append(failed, proxy)
		}
	}
	return failed
}

// runBoundedChecks runs check(p) for every proxy concurrently. concurrency == 0
// keeps the original behavior (all at once); a positive value bounds how many run
// simultaneously via a semaphore, so large subscriptions don't open thousands of
// connections in one burst. Local socks ports are unchanged — each check still
// dials its own fixed port; this only throttles how many run at a time.
func runBoundedChecks(proxies []*models.ProxyConfig, concurrency int, check func(*models.ProxyConfig)) {
	var sem chan struct{}
	if concurrency > 0 {
		sem = make(chan struct{}, concurrency)
	}

	var wg sync.WaitGroup
	for _, proxy := range proxies {
		wg.Add(1)
		go func(p *models.ProxyConfig) {
			defer wg.Done()
			if sem != nil {
				sem <- struct{}{}
				defer func() { <-sem }()
			}
			check(p)
		}(proxy)
	}
	wg.Wait()
}

// GetProxyResultByStableID returns the latest check outcome for the proxy with the
// given stable_id: online status, latency, last-check time as a Unix timestamp in
// seconds (0 if never checked), and whether a result was found.
//
// Lookup is by the unique stable_id (and the full metric key it maps to), the same
// key /metrics uses — so proxies that share a display name are never confused. A
// previous name-based lookup returned the first same-named proxy's result, making
// /config/{id}, the dashboard and the JSON API disagree with /metrics (issue #172).
func (pc *ProxyChecker) GetProxyResultByStableID(stableID string) (bool, time.Duration, int64, bool) {
	online, _, latency, lastCheck, found := pc.GetProxyResultDetailsByStableID(stableID)
	return online, latency, lastCheck, found
}

// GetProxyResultDetailsByStableID also reports whether a node succeeded only
// through a fallback or the slower retry pass.
func (pc *ProxyChecker) GetProxyResultDetailsByStableID(stableID string) (bool, bool, time.Duration, int64, bool) {
	pc.mu.RLock()
	var metricKey proxyMetricLabels
	found := false
	for _, proxy := range pc.proxies {
		if proxy.StableID == "" {
			proxy.StableID = proxy.GenerateStableID()
		}
		if proxy.StableID == stableID {
			metricKey = proxyMetricKey(proxy)
			found = true
			break
		}
	}
	pc.mu.RUnlock()

	if !found {
		return false, false, 0, 0, false
	}

	v, ok := pc.results.Load(metricKey)
	if !ok {
		return false, false, 0, 0, false
	}

	r := v.(proxyResult)
	var lastCheck int64
	if !r.lastCheck.IsZero() {
		lastCheck = r.lastCheck.Unix()
	}
	return r.status, r.unstable, r.latency, lastCheck, true
}

func (pc *ProxyChecker) GetProxyByStableID(stableID string) (*models.ProxyConfig, bool) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	for _, proxy := range pc.proxies {
		if proxy.StableID == "" {
			proxy.StableID = proxy.GenerateStableID()
		}

		if proxy.StableID == stableID {
			return proxy, true
		}
	}
	return nil, false
}

func (pc *ProxyChecker) GetProxies() []*models.ProxyConfig {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	result := make([]*models.ProxyConfig, len(pc.proxies))
	copy(result, pc.proxies)
	return result
}
