package checker

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	xproxy "golang.org/x/net/proxy"

	"xray-checker/logger"
	"xray-checker/models"
)

const (
	accessSnapshotVersion = 1
	accessHistoryLimit    = 20
	accessDirectAttempts  = 3
	accessVPNAttempts     = 2
	accessVPNRouteLimit   = 3
	accessAttemptTimeout  = 7 * time.Second
	accessMaxBody         = 64 * 1024
)

var ErrAccessCheckBusy = errors.New("another access check is already running")

type AccessMethod string

const (
	AccessMethodTCP   AccessMethod = "tcp"
	AccessMethodSSH   AccessMethod = "ssh"
	AccessMethodTLS   AccessMethod = "tls"
	AccessMethodHTTP  AccessMethod = "http"
	AccessMethodHTTPS AccessMethod = "https"
)

type AccessCheckState string

const (
	AccessCheckQueued    AccessCheckState = "queued"
	AccessCheckRunning   AccessCheckState = "running"
	AccessCheckCompleted AccessCheckState = "completed"
)

type AccessVerdict string

const (
	AccessVerdictAvailable         AccessVerdict = "available"
	AccessVerdictAvailableUnstable AccessVerdict = "available_unstable"
	AccessVerdictBlocked           AccessVerdict = "blocked"
	AccessVerdictUnavailable       AccessVerdict = "unavailable"
	AccessVerdictInconclusive      AccessVerdict = "inconclusive"
	AccessVerdictInterrupted       AccessVerdict = "interrupted"
)

type AccessCheckRequest struct {
	IP             string       `json:"ip"`
	Port           int          `json:"port"`
	Method         AccessMethod `json:"method"`
	ServerName     string       `json:"serverName,omitempty"`
	HTTPHost       string       `json:"httpHost,omitempty"`
	Path           string       `json:"path,omitempty"`
	ExpectedStatus int          `json:"expectedStatus,omitempty"`
	ExpectedText   string       `json:"expectedText,omitempty"`
}

type AccessRouteResult struct {
	Route           string `json:"route"`
	NodeID          string `json:"nodeId,omitempty"`
	Name            string `json:"name"`
	ExitIP          string `json:"exitIp,omitempty"`
	Attempts        int    `json:"attempts"`
	Successes       int    `json:"successes"`
	TCPConnected    bool   `json:"tcpConnected"`
	ProtocolMatched bool   `json:"protocolMatched"`
	BestLatencyMs   int64  `json:"bestLatencyMs,omitempty"`
	Banner          string `json:"banner,omitempty"`
	TLSVersion      string `json:"tlsVersion,omitempty"`
	PeerName        string `json:"peerName,omitempty"`
	StatusCode      int    `json:"statusCode,omitempty"`
	LastError       string `json:"lastError,omitempty"`
}

type AccessCheck struct {
	RunID          string              `json:"runId"`
	IP             string              `json:"ip"`
	Port           int                 `json:"port"`
	Method         AccessMethod        `json:"method"`
	ServerName     string              `json:"serverName,omitempty"`
	HTTPHost       string              `json:"httpHost,omitempty"`
	Path           string              `json:"path,omitempty"`
	ExpectedStatus int                 `json:"expectedStatus,omitempty"`
	ExpectedText   string              `json:"expectedText,omitempty"`
	State          AccessCheckState    `json:"state"`
	Stage          string              `json:"stage"`
	Verdict        AccessVerdict       `json:"verdict,omitempty"`
	Summary        string              `json:"summary"`
	SourceIP       string              `json:"sourceIp,omitempty"`
	Interface      string              `json:"interface,omitempty"`
	StartedAt      int64               `json:"startedAt"`
	CompletedAt    int64               `json:"completedAt,omitempty"`
	Direct         AccessRouteResult   `json:"direct"`
	VPN            []AccessRouteResult `json:"vpn,omitempty"`
}

type accessSnapshot struct {
	Version int           `json:"version"`
	Checks  []AccessCheck `json:"checks"`
}

type accessRoute struct {
	kind      string
	nodeID    string
	name      string
	exitIP    string
	proxyPort int
	latency   time.Duration
	lastCheck time.Time
}

type accessDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type accessDialerFactory func(accessRoute) (accessDialer, error)

type directAccessDialer struct{ net.Dialer }

func (d *directAccessDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.Dialer.DialContext(ctx, network, address)
}

type socksAccessDialer struct{ dialer xproxy.ContextDialer }

func (d *socksAccessDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.dialer.DialContext(ctx, network, address)
}

func defaultAccessDialerFactory(route accessRoute) (accessDialer, error) {
	base := &net.Dialer{Timeout: accessAttemptTimeout, KeepAlive: -1}
	if route.kind == "direct" {
		return &directAccessDialer{Dialer: *base}, nil
	}
	if route.proxyPort <= 0 {
		return nil, errors.New("VPN route has no SOCKS port")
	}
	dialer, err := xproxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(route.proxyPort)), nil, base)
	if err != nil {
		return nil, fmt.Errorf("create SOCKS route: %w", err)
	}
	contextDialer, ok := dialer.(xproxy.ContextDialer)
	if !ok {
		return nil, errors.New("SOCKS route does not support cancellation")
	}
	return &socksAccessDialer{dialer: contextDialer}, nil
}

func normalizeAccessRequest(input AccessCheckRequest) (AccessCheckRequest, error) {
	request := input
	request.IP = strings.TrimSpace(request.IP)
	parsed, err := netip.ParseAddr(request.IP)
	if err != nil {
		return AccessCheckRequest{}, errors.New("a valid IP address is required")
	}
	parsed = parsed.Unmap()
	if !parsed.IsGlobalUnicast() || parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() || parsed.IsMulticast() || parsed.IsUnspecified() {
		return AccessCheckRequest{}, errors.New("only public unicast IP addresses can be checked")
	}
	request.IP = parsed.String()
	request.Method = AccessMethod(strings.ToLower(strings.TrimSpace(string(request.Method))))
	switch request.Method {
	case AccessMethodSSH:
		if request.Port == 0 {
			request.Port = 22
		}
	case AccessMethodTLS, AccessMethodHTTPS:
		if request.Port == 0 {
			request.Port = 443
		}
	case AccessMethodHTTP:
		if request.Port == 0 {
			request.Port = 80
		}
	case AccessMethodTCP:
		// TCP has no safe universal default.
	default:
		return AccessCheckRequest{}, errors.New("method must be tcp, ssh, tls, http, or https")
	}
	if request.Port < 1 || request.Port > 65535 {
		return AccessCheckRequest{}, errors.New("port must be between 1 and 65535")
	}
	request.ServerName = strings.TrimSpace(request.ServerName)
	request.HTTPHost = strings.TrimSpace(request.HTTPHost)
	request.Path = strings.TrimSpace(request.Path)
	request.ExpectedText = strings.TrimSpace(request.ExpectedText)
	if request.Path == "" {
		request.Path = "/"
	}
	if !strings.HasPrefix(request.Path, "/") || len(request.Path) > 2048 || containsHeaderBreak(request.Path) {
		return AccessCheckRequest{}, errors.New("HTTP path must start with / and contain no line breaks")
	}
	if !validAccessHost(request.ServerName) || !validAccessHost(request.HTTPHost) {
		return AccessCheckRequest{}, errors.New("SNI and HTTP Host must contain no spaces or line breaks")
	}
	if request.ExpectedStatus != 0 && (request.ExpectedStatus < 100 || request.ExpectedStatus > 599) {
		return AccessCheckRequest{}, errors.New("expected HTTP status must be between 100 and 599")
	}
	if len(request.ExpectedText) > 1024 || containsHeaderBreak(request.ExpectedText) {
		return AccessCheckRequest{}, errors.New("expected text is too long or contains a line break")
	}
	if request.Method != AccessMethodHTTP && request.Method != AccessMethodHTTPS {
		request.HTTPHost = ""
		request.Path = ""
		request.ExpectedStatus = 0
		request.ExpectedText = ""
	}
	if request.Method != AccessMethodTLS && request.Method != AccessMethodHTTPS {
		request.ServerName = ""
	}
	return request, nil
}

func containsHeaderBreak(value string) bool {
	return strings.ContainsAny(value, "\r\n\x00")
}

func validAccessHost(value string) bool {
	if value == "" {
		return true
	}
	return len(value) <= 255 && !containsHeaderBreak(value) && !strings.ContainsAny(value, " \t/\\")
}

func (pc *ProxyChecker) SetAccessCheckFile(path string) error {
	pc.accessFile = strings.TrimSpace(path)
	if pc.accessFile == "" {
		return nil
	}
	data, err := os.ReadFile(pc.accessFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read access-check snapshot: %w", err)
	}
	var snapshot accessSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode access-check snapshot: %w", err)
	}
	if snapshot.Version != accessSnapshotVersion {
		return fmt.Errorf("unsupported access-check snapshot version %d", snapshot.Version)
	}
	now := time.Now().Unix()
	for i := range snapshot.Checks {
		if snapshot.Checks[i].State != AccessCheckCompleted {
			snapshot.Checks[i].State = AccessCheckCompleted
			snapshot.Checks[i].Stage = "completed"
			snapshot.Checks[i].Verdict = AccessVerdictInterrupted
			snapshot.Checks[i].Summary = "Access check was interrupted by checker restart"
			snapshot.Checks[i].CompletedAt = now
		}
	}
	if len(snapshot.Checks) > accessHistoryLimit {
		snapshot.Checks = snapshot.Checks[len(snapshot.Checks)-accessHistoryLimit:]
	}
	pc.accessMu.Lock()
	pc.accessHistory = cloneAccessChecks(snapshot.Checks)
	pc.accessMu.Unlock()
	if err := pc.persistAccessChecks(); err != nil {
		return fmt.Errorf("persist restored access-check snapshot: %w", err)
	}
	return nil
}

func (pc *ProxyChecker) StartAccessCheck(input AccessCheckRequest) (AccessCheck, error) {
	request, err := normalizeAccessRequest(input)
	if err != nil {
		return AccessCheck{}, err
	}
	status := pc.GetNetworkStatus()
	if !status.Ready {
		return AccessCheck{}, fmt.Errorf("probe network unavailable: %s", status.Message)
	}
	now := time.Now()
	run := AccessCheck{
		RunID: fmt.Sprintf("access-%d", now.UnixNano()), IP: request.IP, Port: request.Port,
		Method: request.Method, ServerName: request.ServerName, HTTPHost: request.HTTPHost, Path: request.Path,
		ExpectedStatus: request.ExpectedStatus, ExpectedText: request.ExpectedText,
		State: AccessCheckQueued, Stage: "queued", Summary: "Waiting to check the direct route",
		SourceIP: status.PublicIP, Interface: status.Interface, StartedAt: now.Unix(),
		Direct: AccessRouteResult{Route: "direct", Name: "iPhone direct"},
	}
	pc.accessMu.Lock()
	if pc.accessRunning {
		pc.accessMu.Unlock()
		return AccessCheck{}, ErrAccessCheckBusy
	}
	pc.accessRunning = true
	pc.accessHistory = append(pc.accessHistory, cloneAccessCheck(run))
	if len(pc.accessHistory) > accessHistoryLimit {
		pc.accessHistory = append([]AccessCheck(nil), pc.accessHistory[len(pc.accessHistory)-accessHistoryLimit:]...)
	}
	pc.accessMu.Unlock()
	if err := pc.persistAccessChecks(); err != nil {
		logger.Warn("Could not persist queued access check: %v", err)
	}
	go pc.executeAccessCheck(run, request)
	return run, nil
}

func (pc *ProxyChecker) GetAccessCheck(runID string) (AccessCheck, bool) {
	pc.accessMu.RLock()
	defer pc.accessMu.RUnlock()
	for i := len(pc.accessHistory) - 1; i >= 0; i-- {
		if pc.accessHistory[i].RunID == runID {
			return cloneAccessCheck(pc.accessHistory[i]), true
		}
	}
	return AccessCheck{}, false
}

func (pc *ProxyChecker) GetAccessCheckHistory() []AccessCheck {
	pc.accessMu.RLock()
	defer pc.accessMu.RUnlock()
	result := make([]AccessCheck, 0, len(pc.accessHistory))
	for i := len(pc.accessHistory) - 1; i >= 0; i-- {
		result = append(result, cloneAccessCheck(pc.accessHistory[i]))
	}
	return result
}

func (pc *ProxyChecker) executeAccessCheck(run AccessCheck, request AccessCheckRequest) {
	defer func() {
		pc.accessMu.Lock()
		pc.accessRunning = false
		pc.accessMu.Unlock()
	}()

	status := pc.GetNetworkStatus()
	if !status.Ready {
		pc.completeAccessCheck(run, AccessVerdictInterrupted, "The iPhone route became unavailable before the check started")
		return
	}
	run.State = AccessCheckRunning
	run.Stage = "direct"
	run.SourceIP = status.PublicIP
	run.Interface = status.Interface
	run.Summary = "Checking the target directly through the iPhone route"
	pc.replaceAccessCheck(run)

	direct := accessRoute{kind: "direct", name: "iPhone direct", exitIP: status.PublicIP}
	run.Direct = pc.runAccessRoute(request, direct, accessDirectAttempts)
	pc.replaceAccessCheck(run)
	if status = pc.GetNetworkStatus(); !status.Ready {
		pc.completeAccessCheck(run, AccessVerdictInterrupted, "The iPhone route was interrupted during the direct check")
		return
	}
	if run.Direct.Successes > 0 {
		verdict := AccessVerdictAvailable
		summary := fmt.Sprintf("%s is available directly through the iPhone route", accessMethodLabel(request.Method))
		if run.Direct.Successes < run.Direct.Attempts {
			verdict = AccessVerdictAvailableUnstable
			summary = fmt.Sprintf("%s is available directly, but the result is unstable", accessMethodLabel(request.Method))
		}
		pc.completeAccessCheck(run, verdict, summary)
		return
	}

	run.Stage = "vpn"
	run.Summary = "Direct access failed; checking the same target through healthy VPN routes"
	pc.replaceAccessCheck(run)
	pc.runtimeMu.RLock()
	routes := pc.healthyAccessRoutes(accessVPNRouteLimit)
	if len(routes) == 0 {
		pc.runtimeMu.RUnlock()
		pc.completeAccessCheck(run, AccessVerdictInconclusive, "Direct access failed, but no healthy VPN routes are available for comparison")
		return
	}

	results := make([]AccessRouteResult, len(routes))
	var wg sync.WaitGroup
	for i, route := range routes {
		i, route := i, route
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = pc.runAccessRoute(request, route, accessVPNAttempts)
		}()
	}
	wg.Wait()
	pc.runtimeMu.RUnlock()
	run.VPN = results
	if status = pc.GetNetworkStatus(); !status.Ready {
		pc.completeAccessCheck(run, AccessVerdictInterrupted, "The iPhone route was interrupted during the VPN comparison")
		return
	}
	vpnSuccesses := 0
	for _, result := range results {
		if result.Successes > 0 {
			vpnSuccesses++
		}
	}
	if vpnSuccesses > 0 {
		pc.completeAccessCheck(run, AccessVerdictBlocked,
			fmt.Sprintf("%s is unavailable directly through iPhone but available through %d VPN route(s)", accessMethodLabel(request.Method), vpnSuccesses))
		return
	}
	pc.completeAccessCheck(run, AccessVerdictUnavailable,
		fmt.Sprintf("%s is unavailable both directly and through all healthy VPN routes", accessMethodLabel(request.Method)))
}

func (pc *ProxyChecker) runAccessRoute(request AccessCheckRequest, route accessRoute, attempts int) AccessRouteResult {
	result := AccessRouteResult{Route: route.kind, NodeID: route.nodeID, Name: route.name, ExitIP: route.exitIP}
	dialer, err := pc.accessDialerFactory(route)
	if err != nil {
		result.Attempts = attempts
		result.LastError = normalizeProbeError(err)
		return result
	}
	for attempt := 0; attempt < attempts; attempt++ {
		result.Attempts++
		ctx, cancel := context.WithTimeout(context.Background(), accessAttemptTimeout)
		probe := runAccessProbe(ctx, dialer, request)
		cancel()
		result.TCPConnected = result.TCPConnected || probe.TCPConnected
		result.ProtocolMatched = result.ProtocolMatched || probe.ProtocolMatched
		result.StatusCode = probe.StatusCode
		if probe.Banner != "" {
			result.Banner = probe.Banner
		}
		if probe.TLSVersion != "" {
			result.TLSVersion = probe.TLSVersion
		}
		if probe.PeerName != "" {
			result.PeerName = probe.PeerName
		}
		if probe.Success {
			result.Successes++
			if result.BestLatencyMs == 0 || probe.LatencyMs < result.BestLatencyMs {
				result.BestLatencyMs = probe.LatencyMs
			}
			result.LastError = ""
		} else {
			result.LastError = probe.Error
		}
	}
	return result
}

type accessProbeResult struct {
	Success         bool
	TCPConnected    bool
	ProtocolMatched bool
	LatencyMs       int64
	Banner          string
	TLSVersion      string
	PeerName        string
	StatusCode      int
	Error           string
}

func runAccessProbe(ctx context.Context, dialer accessDialer, request AccessCheckRequest) accessProbeResult {
	started := time.Now()
	address := net.JoinHostPort(request.IP, strconv.Itoa(request.Port))
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return accessProbeResult{Error: normalizeProbeError(err)}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(accessAttemptTimeout))
	result := accessProbeResult{TCPConnected: true, LatencyMs: time.Since(started).Milliseconds()}
	switch request.Method {
	case AccessMethodTCP:
		result.Success = true
		result.ProtocolMatched = true
	case AccessMethodSSH:
		return probeSSH(conn, started, result)
	case AccessMethodTLS:
		return probeTLS(ctx, conn, request, started, result, false)
	case AccessMethodHTTP:
		return probeHTTP(conn, request, started, result)
	case AccessMethodHTTPS:
		return probeTLS(ctx, conn, request, started, result, true)
	default:
		result.Error = "unsupported access method"
	}
	return result
}

func probeSSH(conn net.Conn, started time.Time, result accessProbeResult) accessProbeResult {
	if _, err := io.WriteString(conn, "SSH-2.0-xray-checker\r\n"); err != nil {
		result.Error = normalizeProbeError(err)
		return result
	}
	reader := bufio.NewReader(io.LimitReader(conn, 4096))
	for lines := 0; lines < 8; lines++ {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SSH-2.0-") || strings.HasPrefix(line, "SSH-1.99-") {
			result.Success = true
			result.ProtocolMatched = true
			result.Banner = truncateAccessValue(line, 255)
			result.LatencyMs = time.Since(started).Milliseconds()
			return result
		}
		if err != nil {
			result.Error = normalizeProbeError(err)
			return result
		}
	}
	result.Error = "connected, but no SSH banner was received"
	return result
}

func probeTLS(ctx context.Context, raw net.Conn, request AccessCheckRequest, started time.Time, result accessProbeResult, withHTTP bool) accessProbeResult {
	serverName := request.ServerName
	if serverName == "" {
		serverName = request.IP
	}
	conn := tls.Client(raw, &tls.Config{ServerName: serverName, InsecureSkipVerify: true}) //nolint:gosec -- transport reachability probe
	if err := conn.HandshakeContext(ctx); err != nil {
		result.Error = normalizeProbeError(err)
		return result
	}
	state := conn.ConnectionState()
	result.ProtocolMatched = true
	result.TLSVersion = tlsVersionName(state.Version)
	if len(state.PeerCertificates) > 0 {
		result.PeerName = state.PeerCertificates[0].Subject.CommonName
	}
	result.LatencyMs = time.Since(started).Milliseconds()
	if !withHTTP {
		result.Success = true
		return result
	}
	return probeHTTP(conn, request, started, result)
}

func probeHTTP(conn net.Conn, request AccessCheckRequest, started time.Time, result accessProbeResult) accessProbeResult {
	host := request.HTTPHost
	if host == "" {
		host = request.ServerName
	}
	if host == "" {
		host = request.IP
	}
	path := request.Path
	if path == "" {
		path = "/"
	}
	urlHost := host
	if parsed := net.ParseIP(host); parsed != nil && strings.Contains(host, ":") {
		urlHost = "[" + host + "]"
	}
	httpRequest, err := http.NewRequest(http.MethodGet, "http://"+urlHost+path, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	httpRequest.Host = host
	httpRequest.Header.Set("User-Agent", "Xray-Checker-Access-Probe")
	httpRequest.Header.Set("Accept", "*/*")
	httpRequest.Close = true
	if err := httpRequest.Write(conn); err != nil {
		result.Error = normalizeProbeError(err)
		return result
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), httpRequest)
	if err != nil {
		result.Error = normalizeProbeError(err)
		return result
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, accessMaxBody))
	result.StatusCode = response.StatusCode
	result.ProtocolMatched = true
	result.LatencyMs = time.Since(started).Milliseconds()
	if readErr != nil {
		result.Error = normalizeProbeError(readErr)
		return result
	}
	if request.ExpectedStatus != 0 && response.StatusCode != request.ExpectedStatus {
		result.Error = fmt.Sprintf("HTTP status %d, expected %d", response.StatusCode, request.ExpectedStatus)
		return result
	}
	if request.ExpectedText != "" && !strings.Contains(string(body), request.ExpectedText) {
		result.Error = "HTTP response did not contain the expected text"
		return result
	}
	result.Success = true
	return result
}

func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("TLS 0x%x", version)
	}
}

func truncateAccessValue(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func (pc *ProxyChecker) healthyAccessRoutes(limit int) []accessRoute {
	pc.mu.RLock()
	proxies := append([]*models.ProxyConfig(nil), pc.proxies...)
	pc.mu.RUnlock()
	seenNodes := make(map[string]bool)
	routes := make([]accessRoute, 0)
	for _, proxy := range proxies {
		stableID := proxy.StableID
		if stableID == "" {
			stableID = proxy.GenerateStableID()
		}
		value, ok := pc.results.Load(proxyMetricKey(proxy))
		if !ok {
			continue
		}
		result := value.(proxyResult)
		if !result.status || result.unstable {
			continue
		}
		nodeID := proxy.NodeID
		if nodeID == "" {
			nodeID = stableID
		}
		if seenNodes[nodeID] {
			continue
		}
		seenNodes[nodeID] = true
		routes = append(routes, accessRoute{
			kind: "vpn", nodeID: nodeID, name: proxy.Name, exitIP: result.exitIP,
			proxyPort: pc.startPort + proxy.Index, latency: result.latency, lastCheck: result.lastCheck,
		})
	}
	sort.SliceStable(routes, func(i, j int) bool {
		if !routes[i].lastCheck.Equal(routes[j].lastCheck) {
			return routes[i].lastCheck.After(routes[j].lastCheck)
		}
		return routes[i].latency < routes[j].latency
	})
	if limit > 0 && len(routes) > limit {
		routes = routes[:limit]
	}
	return routes
}

func accessMethodLabel(method AccessMethod) string {
	return strings.ToUpper(string(method))
}

func (pc *ProxyChecker) completeAccessCheck(run AccessCheck, verdict AccessVerdict, summary string) {
	run.State = AccessCheckCompleted
	run.Stage = "completed"
	run.Verdict = verdict
	run.Summary = summary
	run.CompletedAt = time.Now().Unix()
	pc.replaceAccessCheck(run)
	logger.Info("Access check completed: target=%s:%d method=%s verdict=%s", run.IP, run.Port, run.Method, run.Verdict)
}

func (pc *ProxyChecker) replaceAccessCheck(run AccessCheck) {
	pc.accessMu.Lock()
	for i := len(pc.accessHistory) - 1; i >= 0; i-- {
		if pc.accessHistory[i].RunID == run.RunID {
			pc.accessHistory[i] = cloneAccessCheck(run)
			break
		}
	}
	pc.accessMu.Unlock()
	if err := pc.persistAccessChecks(); err != nil {
		logger.Warn("Could not persist access check: %v", err)
	}
}

func (pc *ProxyChecker) persistAccessChecks() error {
	if pc.accessFile == "" {
		return nil
	}
	pc.accessPersistMu.Lock()
	defer pc.accessPersistMu.Unlock()
	pc.accessMu.RLock()
	snapshot := accessSnapshot{Version: accessSnapshotVersion, Checks: cloneAccessChecks(pc.accessHistory)}
	pc.accessMu.RUnlock()
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode access-check snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(pc.accessFile), 0o755); err != nil {
		return fmt.Errorf("create access-check directory: %w", err)
	}
	temporary := pc.accessFile + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write access-check snapshot: %w", err)
	}
	if err := os.Rename(temporary, pc.accessFile); err != nil {
		return fmt.Errorf("replace access-check snapshot: %w", err)
	}
	return nil
}

func cloneAccessCheck(run AccessCheck) AccessCheck {
	run.VPN = append([]AccessRouteResult(nil), run.VPN...)
	return run
}

func cloneAccessChecks(checks []AccessCheck) []AccessCheck {
	result := make([]AccessCheck, len(checks))
	for i := range checks {
		result[i] = cloneAccessCheck(checks[i])
	}
	return result
}
