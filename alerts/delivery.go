package alerts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"xray-checker/checker"
)

const (
	maxAutomaticRoutes = 3
	routeTimeout       = 8 * time.Second
	routeCooldown      = 5 * time.Minute
)

type deliveryCandidate struct {
	id      string
	label   string
	client  *TelegramClient
	auto    bool
	latency time.Duration
}

func normalizeDeliveryMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case DeliveryDirect:
		return DeliveryDirect
	case DeliveryCustom:
		return DeliveryCustom
	default:
		return DeliveryAuto
	}
}

func (m *Manager) verifyThroughRoute(ctx context.Context, token, mode, customProxyURL string) (BotInfo, error) {
	var bot BotInfo
	err := m.tryDelivery(ctx, mode, customProxyURL, nil, func(client *TelegramClient) error {
		var verifyErr error
		bot, verifyErr = client.Verify(ctx, token)
		return verifyErr
	})
	return bot, err
}

// tryDelivery is deliberately isolated from proxy checking. It only reads the
// latest checker snapshot and opens an ordinary HTTPS request through an
// already-running local Xray SOCKS inbound. It never schedules a check, mutates a
// result, or holds a checker lock while waiting for Telegram.
func (m *Manager) tryDelivery(
	ctx context.Context,
	mode string,
	customProxyURL string,
	excludedNodeIDs map[string]bool,
	operation func(*TelegramClient) error,
) error {
	m.deliveryMu.Lock()
	defer m.deliveryMu.Unlock()

	candidates, err := m.deliveryCandidates(mode, customProxyURL, excludedNodeIDs)
	if err != nil {
		return err
	}
	var lastErr error
	attempted := 0
	for _, candidate := range candidates {
		attempted++
		err := operation(candidate.client)
		candidate.client.httpClient.CloseIdleConnections()
		if err == nil {
			m.recordRouteSuccess(candidate)
			return nil
		}
		lastErr = err
		retryable := telegramErrorRetryable(err)
		if candidate.auto && retryable {
			m.recordRouteFailure(candidate.id)
		}
		if !retryable {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no Telegram delivery route is available")
	}
	return fmt.Errorf("Telegram delivery failed through %d route(s): %w", attempted, lastErr)
}

func (m *Manager) deliveryCandidates(
	mode string,
	customProxyURL string,
	excludedNodeIDs map[string]bool,
) ([]deliveryCandidate, error) {
	if strings.TrimSpace(mode) == "" {
		m.mu.RLock()
		mode = m.settings.DeliveryMode
		m.mu.RUnlock()
	}
	mode = normalizeDeliveryMode(mode)
	switch mode {
	case DeliveryDirect:
		return []deliveryCandidate{{
			id: "direct", label: "Direct mobile connection",
			client: m.client.withHTTPClient(&http.Client{Timeout: routeTimeout}),
		}}, nil
	case DeliveryCustom:
		customProxyURL = strings.TrimSpace(customProxyURL)
		if customProxyURL == "" {
			m.mu.RLock()
			customProxyURL = m.customProxy
			m.mu.RUnlock()
		}
		client, err := telegramProxyClient(customProxyURL)
		if err != nil {
			return nil, err
		}
		return []deliveryCandidate{{
			id: "custom", label: "Custom proxy", client: m.client.withHTTPClient(client),
		}}, nil
	default:
		return m.automaticCandidates(excludedNodeIDs)
	}
}

func (m *Manager) automaticCandidates(excludedNodeIDs map[string]bool) ([]deliveryCandidate, error) {
	type route struct {
		nodeID  string
		label   string
		port    int
		latency time.Duration
	}
	m.mu.RLock()
	lastRouteID := m.state.LastRouteID
	failures := make(map[string]int64, len(m.state.RouteFailures))
	for routeID, until := range m.state.RouteFailures {
		failures[routeID] = until
	}
	m.mu.RUnlock()

	now := time.Now().Unix()
	byNode := make(map[string]route)
	cooledByNode := make(map[string]route)
	for _, proxy := range m.checker.GetProxies() {
		nodeID := proxy.NodeID
		if nodeID == "" {
			nodeID = proxy.GenerateNodeID()
		}
		if excludedNodeIDs[nodeID] {
			continue
		}
		online, unstable, latency, _, found := m.checker.GetProxyResultDetailsByStableID(proxy.StableID)
		if !found || !online || unstable {
			continue
		}
		monitor, found := m.checker.GetNodeMonitorByStableID(proxy.StableID)
		if !found || (monitor.State != checker.NodeHealthy && monitor.State != checker.NodeFixed) {
			continue
		}
		name := strings.TrimSpace(proxy.Name)
		server := displayServer(proxy.Server)
		label := server
		if name != "" && !strings.EqualFold(name, server) {
			label = name + " · " + server
		}
		candidate := route{
			nodeID:  nodeID,
			label:   label,
			port:    m.startPort + proxy.Index,
			latency: latency,
		}
		target := byNode
		if until := failures[nodeID]; until > now {
			target = cooledByNode
		}
		if existing, exists := target[nodeID]; !exists || routeLess(candidate, existing, lastRouteID) {
			target[nodeID] = candidate
		}
	}
	// Cooldown keeps a repeatedly failing route out of the normal rotation, but
	// it must not turn a transient Telegram outage into a five-minute blackout.
	// If every healthy node is cooling down, retry the best of those nodes.
	if len(byNode) == 0 {
		byNode = cooledByNode
	}
	routes := make([]route, 0, len(byNode))
	for _, route := range byNode {
		routes = append(routes, route)
	}
	sort.Slice(routes, func(i, j int) bool { return routeLess(routes[i], routes[j], lastRouteID) })
	if len(routes) > maxAutomaticRoutes {
		routes = routes[:maxAutomaticRoutes]
	}
	if len(routes) == 0 {
		return nil, errors.New("no healthy Xray nodes are available for Telegram delivery")
	}

	result := make([]deliveryCandidate, 0, len(routes))
	for _, route := range routes {
		proxyURL := fmt.Sprintf("socks5h://127.0.0.1:%d", route.port)
		httpClient, err := telegramProxyClient(proxyURL)
		if err != nil {
			return nil, err
		}
		result = append(result, deliveryCandidate{
			id: route.nodeID, label: route.label,
			client: m.client.withHTTPClient(httpClient), auto: true, latency: route.latency,
		})
	}
	return result, nil
}

func routeLess(left, right struct {
	nodeID  string
	label   string
	port    int
	latency time.Duration
}, lastRouteID string) bool {
	if (left.nodeID == lastRouteID) != (right.nodeID == lastRouteID) {
		return left.nodeID == lastRouteID
	}
	if (left.latency == 0) != (right.latency == 0) {
		return left.latency != 0
	}
	if left.latency != right.latency {
		return left.latency < right.latency
	}
	return left.label < right.label
}

func telegramProxyClient(rawURL string) (*http.Client, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("Custom Telegram proxy URL is required")
	}
	proxyURL, err := url.Parse(rawURL)
	if err != nil || proxyURL.Host == "" {
		return nil, errors.New("Telegram proxy URL is invalid")
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("Telegram proxy must use http, https, socks5, or socks5h")
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(proxyURL),
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   routeTimeout,
		ResponseHeaderTimeout: routeTimeout,
	}
	return &http.Client{Transport: transport, Timeout: routeTimeout}, nil
}

func (m *Manager) recordRouteSuccess(candidate deliveryCandidate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.LastRoute = candidate.label
	m.state.LastRouteID = candidate.id
	delete(m.state.RouteFailures, candidate.id)
	_ = m.persistStateLocked()
}

func (m *Manager) recordRouteFailure(routeID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.RouteFailures == nil {
		m.state.RouteFailures = make(map[string]int64)
	}
	m.state.RouteFailures[routeID] = time.Now().Add(routeCooldown).Unix()
	_ = m.persistStateLocked()
}

func (m *Manager) healthyRouteCount() int {
	nodes := make(map[string]bool)
	for _, proxy := range m.checker.GetProxies() {
		online, unstable, _, _, found := m.checker.GetProxyResultDetailsByStableID(proxy.StableID)
		if !found || !online || unstable {
			continue
		}
		monitor, found := m.checker.GetNodeMonitorByStableID(proxy.StableID)
		if !found || (monitor.State != checker.NodeHealthy && monitor.State != checker.NodeFixed) {
			continue
		}
		nodeID := proxy.NodeID
		if nodeID == "" {
			nodeID = proxy.GenerateNodeID()
		}
		nodes[nodeID] = true
	}
	return len(nodes)
}
