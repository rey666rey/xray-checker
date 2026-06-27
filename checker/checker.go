package checker

import (
	"context"
	"fmt"
	"io"
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
	proxies         []*models.ProxyConfig
	startPort       int
	ipCheck         string
	currentIP       string
	httpClient      *http.Client
	currentMetrics  sync.Map
	latencyMetrics  sync.Map
	ipInitialized   bool
	ipCheckTimeout  int
	genMethodURL    string
	downloadURL     string
	downloadTimeout int
	downloadMinSize int64
	checkMethod     string
	mu              sync.RWMutex
	generation      uint64
}

func NewProxyChecker(proxies []*models.ProxyConfig, startPort int, ipCheckURL string, ipCheckTimeout int, genMethodURL string, downloadURL string, downloadTimeout int, downloadMinSize int64, checkMethod string) *ProxyChecker {
	return &ProxyChecker{
		proxies:   proxies,
		startPort: startPort,
		ipCheck:   ipCheckURL,
		httpClient: &http.Client{
			Timeout: time.Second * time.Duration(ipCheckTimeout),
		},
		ipCheckTimeout:  ipCheckTimeout,
		genMethodURL:    genMethodURL,
		downloadURL:     downloadURL,
		downloadTimeout: downloadTimeout,
		downloadMinSize: downloadMinSize,
		checkMethod:     checkMethod,
	}
}

func (pc *ProxyChecker) GetCurrentIP() (string, error) {
	if pc.ipInitialized && pc.currentIP != "" {
		return pc.currentIP, nil
	}

	resp, err := pc.httpClient.Get(pc.ipCheck)
	if err != nil {
		return "", fmt.Errorf("error getting current IP: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %v", err)
	}

	pc.currentIP = string(body)
	pc.ipInitialized = true
	return pc.currentIP, nil
}

func (pc *ProxyChecker) CheckProxy(proxy *models.ProxyConfig) {
	pc.checkProxyInternal(proxy, 0, false)
}

// proxyMetricKey builds the in-memory map key for a proxy. The first four
// "|"-separated fields (protocol, address, name, sub_name) match the Prometheus
// label set, so ClearMetrics/ReconcileMetrics can derive the series labels from it.
func proxyMetricKey(proxy *models.ProxyConfig) string {
	return fmt.Sprintf("%s|%s:%d|%s|%s|%s",
		proxy.Protocol,
		proxy.Server,
		proxy.Port,
		proxy.Name,
		proxy.SubName,
		proxy.StableID,
	)
}

func (pc *ProxyChecker) checkProxyInternal(proxy *models.ProxyConfig, expectedGeneration uint64, checkGeneration bool) {
	if proxy.StableID == "" {
		proxy.StableID = proxy.GenerateStableID()
	}

	metricKey := proxyMetricKey(proxy)

	isGenerationValid := func() bool {
		if !checkGeneration {
			return true
		}
		return atomic.LoadUint64(&pc.generation) == expectedGeneration
	}

	setFailedStatus := func() {
		if !isGenerationValid() {
			logger.Debug("%s | Skipping metric update: generation changed", proxy.Name)
			return
		}
		metrics.RecordProxyStatus(
			proxy.Protocol,
			fmt.Sprintf("%s:%d", proxy.Server, proxy.Port),
			proxy.Name,
			proxy.SubName,
			0,
		)
		pc.currentMetrics.Store(metricKey, false)
	}

	setFailedLatency := func() {
		if !isGenerationValid() {
			return
		}
		metrics.RecordProxyLatency(
			proxy.Protocol,
			fmt.Sprintf("%s:%d", proxy.Server, proxy.Port),
			proxy.Name,
			proxy.SubName,
			time.Duration(0),
		)
		pc.latencyMetrics.Store(metricKey, time.Duration(0))
	}

	proxyURL := fmt.Sprintf("socks5://127.0.0.1:%d", pc.startPort+proxy.Index)
	proxyURLParsed, err := url.Parse(proxyURL)
	if err != nil {
		logger.Error("Error parsing proxy URL %s: %v", proxyURL, err)
		setFailedStatus()
		setFailedLatency()

		return
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURLParsed),
			DisableKeepAlives: true,
		},
		Timeout: time.Second * time.Duration(pc.ipCheckTimeout),
	}

	var checkSuccess bool
	var checkErr error
	var logMessage string
	var latency time.Duration

	if pc.checkMethod == "ip" {
		checkSuccess, logMessage, latency, checkErr = pc.checkByIP(client)
	} else if pc.checkMethod == "status" {
		checkSuccess, logMessage, latency, checkErr = pc.checkByGen(client)
	} else if pc.checkMethod == "download" {
		checkSuccess, logMessage, latency, checkErr = pc.checkByDownload(client)
	} else {
		logger.Error("Invalid check method: %s", pc.checkMethod)
		return
	}

	if checkErr != nil {
		logger.Error("%s | %v", proxy.Name, checkErr)
		setFailedStatus()
		setFailedLatency()

		return
	}

	if !checkSuccess {
		logger.Error("%s | Failed | %s | Latency: %s", proxy.Name, logMessage, latency)
		setFailedStatus()
		setFailedLatency()
	} else {
		logger.Result("%s | Success | %s | Latency: %s", proxy.Name, logMessage, latency)
		if !isGenerationValid() {
			logger.Debug("%s | Skipping metric update: generation changed", proxy.Name)
			return
		}
		metrics.RecordProxyStatus(
			proxy.Protocol,
			fmt.Sprintf("%s:%d", proxy.Server, proxy.Port),
			proxy.Name,
			proxy.SubName,
			1,
		)
		metrics.RecordProxyLatency(
			proxy.Protocol,
			fmt.Sprintf("%s:%d", proxy.Server, proxy.Port),
			proxy.Name,
			proxy.SubName,
			latency,
		)

		pc.latencyMetrics.Store(metricKey, latency)
		pc.currentMetrics.Store(metricKey, true)
	}
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
	logMessage := fmt.Sprintf("Source IP: %s | Proxy IP: %s", pc.currentIP, proxyIP)
	return proxyIP != pc.currentIP, logMessage, ttfb, nil
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

func (pc *ProxyChecker) ClearMetrics() {
	pc.currentMetrics.Range(func(key, _ interface{}) bool {
		metricKey := key.(string)
		parts := strings.Split(metricKey, "|")
		if len(parts) >= 4 {
			metrics.DeleteProxyStatus(parts[0], parts[1], parts[2], parts[3])
			metrics.DeleteProxyLatency(parts[0], parts[1], parts[2], parts[3])
		}
		pc.currentMetrics.Delete(key)
		return true
	})

	pc.latencyMetrics.Range(func(key, _ interface{}) bool {
		pc.latencyMetrics.Delete(key)
		return true
	})
}

// UpdateProxies swaps in a new proxy set and bumps the generation. It deliberately
// does NOT clear metrics: doing so used to leave /metrics empty (every proxy "down")
// for up to PROXY_CHECK_INTERVAL until the next scheduled check (#148). The old series
// are kept so existing proxies never blink; the caller should run an immediate check
// and then ReconcileMetrics to drop series for proxies that no longer exist.
func (pc *ProxyChecker) UpdateProxies(newProxies []*models.ProxyConfig) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	atomic.AddUint64(&pc.generation, 1)
	pc.proxies = newProxies
}

// ReconcileMetrics removes status/latency series and in-memory entries that belong
// to proxies no longer present in the current set. It is meant to run after an
// immediate post-update check has populated metrics for the new set, so surviving
// proxies keep their freshly-refreshed values and only stale ones are pruned.
func (pc *ProxyChecker) ReconcileMetrics() {
	pc.mu.RLock()
	currentKeys := make(map[string]struct{}, len(pc.proxies))
	currentLabels := make(map[string]struct{}, len(pc.proxies))
	for _, proxy := range pc.proxies {
		if proxy.StableID == "" {
			proxy.StableID = proxy.GenerateStableID()
		}
		key := proxyMetricKey(proxy)
		currentKeys[key] = struct{}{}
		// label tuple = protocol|address|name|sub_name (first 4 key fields)
		currentLabels[fmt.Sprintf("%s|%s:%d|%s|%s", proxy.Protocol, proxy.Server, proxy.Port, proxy.Name, proxy.SubName)] = struct{}{}
	}
	pc.mu.RUnlock()

	pc.currentMetrics.Range(func(k, _ interface{}) bool {
		metricKey := k.(string)
		if _, ok := currentKeys[metricKey]; ok {
			return true
		}
		parts := strings.Split(metricKey, "|")
		if len(parts) >= 4 {
			// Only delete the Prometheus series if no surviving proxy shares the
			// same label tuple (guards against same-label collisions removing a
			// still-present proxy's series).
			labelTuple := strings.Join(parts[0:4], "|")
			if _, used := currentLabels[labelTuple]; !used {
				metrics.DeleteProxyStatus(parts[0], parts[1], parts[2], parts[3])
				metrics.DeleteProxyLatency(parts[0], parts[1], parts[2], parts[3])
			}
		}
		pc.currentMetrics.Delete(k)
		pc.latencyMetrics.Delete(k)
		return true
	})
}

func (pc *ProxyChecker) CheckAllProxies() {
	if _, err := pc.GetCurrentIP(); err != nil {
		logger.Warn("Error getting current IP: %v", err)
		return
	}

	pc.mu.RLock()
	proxiesToCheck := make([]*models.ProxyConfig, len(pc.proxies))
	copy(proxiesToCheck, pc.proxies)
	currentGeneration := atomic.LoadUint64(&pc.generation)
	pc.mu.RUnlock()

	var wg sync.WaitGroup
	for _, proxy := range proxiesToCheck {
		wg.Add(1)
		go func(p *models.ProxyConfig, gen uint64) {
			defer wg.Done()
			pc.checkProxyInternal(p, gen, true)
		}(proxy, currentGeneration)
	}
	wg.Wait()
}

func (pc *ProxyChecker) GetProxyStatus(name string) (bool, time.Duration, error) {
	pc.mu.RLock()
	var metricKey string
	for _, proxy := range pc.proxies {
		if proxy.Name == name {
			if proxy.StableID == "" {
				proxy.StableID = proxy.GenerateStableID()
			}

			metricKey = proxyMetricKey(proxy)
			break
		}
	}
	pc.mu.RUnlock()

	if metricKey == "" {
		return false, 0, fmt.Errorf("proxy not found")
	}

	status, ok := pc.currentMetrics.Load(metricKey)
	if !ok {
		return false, 0, fmt.Errorf("metric not found")
	}

	latency, _ := pc.latencyMetrics.Load(metricKey)
	if latency == nil {
		latency = time.Duration(0)
	}

	return status.(bool), latency.(time.Duration), nil
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
