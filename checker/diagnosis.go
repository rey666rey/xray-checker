package checker

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"xray-checker/logger"
	"xray-checker/models"
)

const (
	diagnosisSnapshotVersion = 1
	diagnosisAttempts        = 3
	diagnosisHistoryLimit    = 10
)

var ErrDiagnosisBusy = errors.New("another node diagnosis is already running")
var ErrDiagnosisPriority = errors.New("background checks deferred for manual diagnosis")

type DiagnosisState string

const (
	DiagnosisQueued    DiagnosisState = "queued"
	DiagnosisRunning   DiagnosisState = "running"
	DiagnosisCompleted DiagnosisState = "completed"
)

type DiagnosisVerdict string

const (
	DiagnosisHealthy         DiagnosisVerdict = "healthy"
	DiagnosisDegraded        DiagnosisVerdict = "degraded"
	DiagnosisNetUnreachable  DiagnosisVerdict = "net_unreachable"
	DiagnosisHandshakeFailed DiagnosisVerdict = "handshake_failed"
	DiagnosisTunnelFailed    DiagnosisVerdict = "tunnel_failed"
	DiagnosisInconclusive    DiagnosisVerdict = "inconclusive"
)

type ControlDiagnosis struct {
	URL        string `json:"url,omitempty"`
	Online     bool   `json:"online"`
	StatusCode int    `json:"statusCode,omitempty"`
	LatencyMs  int64  `json:"latencyMs,omitempty"`
	ObservedIP string `json:"observedIp,omitempty"`
	Error      string `json:"error,omitempty"`
}

type TLSProbeDiagnosis struct {
	Port          int    `json:"port"`
	ServerName    string `json:"serverName"`
	Attempts      int    `json:"attempts"`
	Successes     int    `json:"successes"`
	BestLatencyMs int64  `json:"bestLatencyMs,omitempty"`
	PeerName      string `json:"peerName,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

type PortDiagnosis struct {
	Port          int    `json:"port"`
	Network       string `json:"network"`
	Attempts      int    `json:"attempts"`
	Successes     int    `json:"successes"`
	BestLatencyMs int64  `json:"bestLatencyMs,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

type BindingDiagnosis struct {
	StableID      string `json:"stableId"`
	HostID        string `json:"hostId"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	Security      string `json:"security,omitempty"`
	Port          int    `json:"port"`
	ServerName    string `json:"serverName,omitempty"`
	Attempts      int    `json:"attempts"`
	Successes     int    `json:"successes"`
	BestLatencyMs int64  `json:"bestLatencyMs,omitempty"`
	FailureStage  string `json:"failureStage,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

// NodeDiagnosis is a manual, point-in-time report. It deliberately does not
// overwrite the normal online/offline result: a probe uplink can fail while the
// node remains healthy from another vantage point.
type NodeDiagnosis struct {
	RunID       string              `json:"runId"`
	NodeID      string              `json:"nodeId"`
	Server      string              `json:"server"`
	Revision    string              `json:"revision"`
	ProbeID     string              `json:"probeId"`
	Interface   string              `json:"interface,omitempty"`
	SourceIP    string              `json:"sourceIp,omitempty"`
	State       DiagnosisState      `json:"state"`
	Stage       string              `json:"stage,omitempty"`
	Verdict     DiagnosisVerdict    `json:"verdict,omitempty"`
	Summary     string              `json:"summary,omitempty"`
	StartedAt   int64               `json:"startedAt"`
	CompletedAt int64               `json:"completedAt,omitempty"`
	Stale       bool                `json:"stale,omitempty"`
	Control     ControlDiagnosis    `json:"control"`
	Ports       []PortDiagnosis     `json:"ports,omitempty"`
	TLS         []TLSProbeDiagnosis `json:"tls,omitempty"`
	Bindings    []BindingDiagnosis  `json:"bindings,omitempty"`
}

type diagnosisSnapshot struct {
	Version int                        `json:"version"`
	Nodes   map[string][]NodeDiagnosis `json:"nodes"`
}

func (pc *ProxyChecker) SetDiagnosisFile(path string) error {
	pc.diagnosisFile = strings.TrimSpace(path)
	if pc.diagnosisFile == "" {
		return nil
	}
	data, err := os.ReadFile(pc.diagnosisFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read diagnosis snapshot: %w", err)
	}
	var snapshot diagnosisSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode diagnosis snapshot: %w", err)
	}
	if snapshot.Version != diagnosisSnapshotVersion {
		return fmt.Errorf("unsupported diagnosis snapshot version %d", snapshot.Version)
	}
	pc.diagnosisMu.Lock()
	defer pc.diagnosisMu.Unlock()
	pc.diagnosisHistory = snapshot.Nodes
	if pc.diagnosisHistory == nil {
		pc.diagnosisHistory = make(map[string][]NodeDiagnosis)
	}
	// A process restart cannot resume an in-flight diagnostic safely.
	for nodeID, history := range pc.diagnosisHistory {
		for i := range history {
			if history[i].State != DiagnosisCompleted {
				history[i].State = DiagnosisCompleted
				history[i].Verdict = DiagnosisInconclusive
				history[i].Summary = "Diagnosis interrupted by checker restart"
				history[i].CompletedAt = time.Now().Unix()
			}
		}
		pc.diagnosisHistory[nodeID] = history
	}
	return nil
}

func (pc *ProxyChecker) StartNodeDiagnosis(nodeID string) (NodeDiagnosis, error) {
	bindings := pc.nodeBindings(nodeID)
	if len(bindings) == 0 {
		return NodeDiagnosis{}, fmt.Errorf("node not found")
	}
	status := pc.GetNetworkStatus()
	if !status.Ready {
		return NodeDiagnosis{}, fmt.Errorf("probe network unavailable: %s", status.Message)
	}

	now := time.Now()
	run := NodeDiagnosis{
		RunID:     fmt.Sprintf("%d", now.UnixNano()),
		NodeID:    nodeID,
		Server:    bindings[0].Server,
		Revision:  nodeDiagnosisRevision(bindings),
		ProbeID:   diagnosisProbeID(status),
		Interface: status.Interface,
		SourceIP:  status.PublicIP,
		State:     DiagnosisQueued,
		Stage:     "queued",
		Summary:   "Waiting for the current background check to finish",
		StartedAt: now.Unix(),
	}

	pc.diagnosisMu.Lock()
	if pc.diagnosisRunning {
		pc.diagnosisMu.Unlock()
		return NodeDiagnosis{}, ErrDiagnosisBusy
	}
	pc.diagnosisRunning = true
	pc.appendDiagnosisLocked(run)
	pc.diagnosisMu.Unlock()
	if err := pc.persistDiagnoses(); err != nil {
		logger.Warn("Could not persist queued node diagnosis: %v", err)
	}

	go pc.executeNodeDiagnosis(run, bindings)
	return run, nil
}

func (pc *ProxyChecker) GetNodeDiagnosis(nodeID string) (NodeDiagnosis, bool) {
	bindings := pc.nodeBindings(nodeID)
	pc.diagnosisMu.RLock()
	history := pc.diagnosisHistory[nodeID]
	if len(history) == 0 {
		pc.diagnosisMu.RUnlock()
		return NodeDiagnosis{}, false
	}
	result := cloneNodeDiagnosis(history[len(history)-1])
	pc.diagnosisMu.RUnlock()
	if len(bindings) > 0 {
		result.Stale = result.Revision != nodeDiagnosisRevision(bindings)
	}
	return result, true
}

func (pc *ProxyChecker) GetNodeDiagnosisHistory(nodeID string) []NodeDiagnosis {
	bindings := pc.nodeBindings(nodeID)
	currentRevision := nodeDiagnosisRevision(bindings)
	pc.diagnosisMu.RLock()
	history := pc.diagnosisHistory[nodeID]
	result := make([]NodeDiagnosis, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		item := cloneNodeDiagnosis(history[i])
		item.Stale = currentRevision != "" && item.Revision != currentRevision
		result = append(result, item)
	}
	pc.diagnosisMu.RUnlock()
	return result
}

func (pc *ProxyChecker) executeNodeDiagnosis(run NodeDiagnosis, bindings []*models.ProxyConfig) {
	defer func() {
		pc.diagnosisMu.Lock()
		pc.diagnosisRunning = false
		pc.diagnosisMu.Unlock()
	}()

	pc.checkCycleMu.Lock()
	defer pc.checkCycleMu.Unlock()

	// Subscription refreshes and Xray reloads use the same lock. Refresh the
	// binding snapshot after acquiring it so a queued diagnosis never probes a
	// configuration that was replaced while it was waiting.
	bindings = pc.nodeBindings(run.NodeID)
	if len(bindings) == 0 {
		run.Verdict = DiagnosisInconclusive
		run.Summary = "Node disappeared from subscriptions before diagnosis started"
		pc.completeDiagnosis(run)
		return
	}
	run.Server = bindings[0].Server
	run.Revision = nodeDiagnosisRevision(bindings)
	run.State = DiagnosisRunning
	run.Stage = "network_control"
	run.Summary = "Checking the iPhone route and control endpoint"
	pc.replaceDiagnosis(run)

	status := pc.GetNetworkStatus()
	if !status.Ready {
		run.Verdict = DiagnosisInconclusive
		run.Summary = "Probe network became unavailable before diagnosis started"
		pc.completeDiagnosis(run)
		return
	}
	run.Interface = status.Interface
	run.SourceIP = status.PublicIP
	run.ProbeID = diagnosisProbeID(status)

	controlTimeout := time.Duration(pc.ipCheckTimeout) * time.Second
	if controlTimeout <= 0 {
		controlTimeout = 5 * time.Second
	}
	run.Control = pc.runDiagnosisControl(controlTimeout, status.PublicIP)
	if run.Control.ObservedIP != "" {
		run.SourceIP = run.Control.ObservedIP
	}
	if !run.Control.Online {
		run.Verdict = DiagnosisInconclusive
		run.Summary = "Control request failed on the probe network"
		pc.completeDiagnosis(run)
		return
	}

	probeTimeout := controlTimeout
	if probeTimeout > 3*time.Second {
		probeTimeout = 3 * time.Second
	}
	run.Stage = "direct_endpoint"
	run.Summary = "Testing endpoint ports and configured TLS/Reality handshakes"
	pc.replaceDiagnosis(run)
	run.Ports, run.TLS = runDirectDiagnostics(bindings, probeTimeout)
	if allTCPPortsUnreachable(run.Ports) {
		run.Verdict = DiagnosisNetUnreachable
		run.Summary = "Control network works, but every TCP endpoint attempt failed"
		pc.completeDiagnosis(run)
		return
	}

	run.Stage = "xray_tunnels"
	run.Summary = fmt.Sprintf("Testing %d configured Xray binding(s)", len(bindings))
	pc.replaceDiagnosis(run)
	run.Bindings = pc.runBindingDiagnostics(bindings, probeTimeout)
	if status = pc.GetNetworkStatus(); !status.Ready {
		run.Verdict = DiagnosisInconclusive
		run.Summary = "Probe network was interrupted during diagnosis"
		pc.completeDiagnosis(run)
		return
	}
	run.Verdict, run.Summary = classifyNodeDiagnosis(run)
	pc.completeDiagnosis(run)
}

func allTCPPortsUnreachable(ports []PortDiagnosis) bool {
	tcpPorts := 0
	for _, port := range ports {
		if port.Network != "tcp" || port.Attempts == 0 {
			continue
		}
		tcpPorts++
		if port.Successes > 0 {
			return false
		}
	}
	return tcpPorts > 0
}

func (pc *ProxyChecker) runDiagnosisControl(timeout time.Duration, expectedSourceIP string) ControlDiagnosis {
	target := pc.urlTestURL
	if target == "" {
		target = pc.ipCheck
	}
	result := ControlDiagnosis{URL: target}
	if target == "" {
		result.Error = "no control URL configured"
		return result
	}
	client := &http.Client{Timeout: timeout}
	status, body, latency, err := timedProxyGET(client, target, 64*1024)
	result.StatusCode = status
	result.LatencyMs = latency.Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Online = status >= 200 && status < 300
	if pc.urlTestExpected != "" && target == pc.urlTestURL {
		result.Online = result.Online && strings.Contains(body, pc.urlTestExpected)
	}
	if !result.Online {
		result.Error = fmt.Sprintf("unexpected control response (status %d)", status)
		return result
	}

	// A host-network monitor can watch a specific interface while the checker
	// container accidentally follows another default route. Verify the egress
	// address before attributing endpoint failures to the named probe network.
	if pc.ipCheck != "" {
		ipStatus, ipBody, _, ipErr := timedProxyGET(client, pc.ipCheck, 256)
		if ipErr != nil {
			result.Online = false
			result.Error = "could not verify probe egress: " + ipErr.Error()
			return result
		}
		observed := strings.TrimSpace(ipBody)
		if ipStatus < 200 || ipStatus >= 300 || net.ParseIP(observed) == nil {
			result.Online = false
			result.Error = "probe egress service returned an invalid address"
			return result
		}
		result.ObservedIP = observed
		if expected := strings.TrimSpace(expectedSourceIP); expected != "" && observed != expected {
			result.Online = false
			result.Error = fmt.Sprintf("probe route mismatch: monitor=%s checker=%s", expected, observed)
		}
	}
	return result
}

type directTarget struct {
	serverNames map[string]bool
	port        int
	network     string
}

func runDirectDiagnostics(bindings []*models.ProxyConfig, timeout time.Duration) ([]PortDiagnosis, []TLSProbeDiagnosis) {
	targets := make(map[string]*directTarget)
	for _, binding := range bindings {
		network := "tcp"
		if isUDPBinding(binding) {
			network = "udp"
		}
		key := fmt.Sprintf("%s:%d", network, binding.Port)
		target := targets[key]
		if target == nil {
			target = &directTarget{port: binding.Port, network: network, serverNames: make(map[string]bool)}
			targets[key] = target
		}
		if network == "tcp" && usesTLS(binding) && strings.TrimSpace(binding.SNI) != "" {
			target.serverNames[strings.TrimSpace(binding.SNI)] = true
		}
	}

	server := ""
	if len(bindings) > 0 {
		server = bindings[0].Server
	}
	ports := make([]PortDiagnosis, 0, len(targets))
	tlsProbes := make([]TLSProbeDiagnosis, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			portResult := probeDirectPort(server, target.network, target.port, timeout)
			localTLS := make([]TLSProbeDiagnosis, 0, len(target.serverNames))
			if target.network == "tcp" && portResult.Successes > 0 {
				names := make([]string, 0, len(target.serverNames))
				for name := range target.serverNames {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					localTLS = append(localTLS, probeDirectTLS(server, target.port, name, timeout))
				}
			}
			mu.Lock()
			ports = append(ports, portResult)
			tlsProbes = append(tlsProbes, localTLS...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(ports, func(i, j int) bool {
		if ports[i].Port != ports[j].Port {
			return ports[i].Port < ports[j].Port
		}
		return ports[i].Network < ports[j].Network
	})
	sort.Slice(tlsProbes, func(i, j int) bool {
		if tlsProbes[i].Port != tlsProbes[j].Port {
			return tlsProbes[i].Port < tlsProbes[j].Port
		}
		return tlsProbes[i].ServerName < tlsProbes[j].ServerName
	})
	return ports, tlsProbes
}

func probeDirectPort(server, network string, port int, timeout time.Duration) PortDiagnosis {
	result := PortDiagnosis{Port: port, Network: network, Attempts: diagnosisAttempts}
	if network == "udp" {
		result.Attempts = 0
		result.LastError = "UDP reachability requires a protocol handshake"
		return result
	}
	address := net.JoinHostPort(server, fmt.Sprintf("%d", port))
	for i := 0; i < diagnosisAttempts; i++ {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", address, timeout)
		if err != nil {
			result.LastError = normalizeProbeError(err)
			continue
		}
		latency := time.Since(start).Milliseconds()
		_ = conn.Close()
		result.Successes++
		if result.BestLatencyMs == 0 || latency < result.BestLatencyMs {
			result.BestLatencyMs = latency
		}
	}
	return result
}

func probeDirectTLS(server string, port int, serverName string, timeout time.Duration) TLSProbeDiagnosis {
	result := TLSProbeDiagnosis{Port: port, ServerName: serverName, Attempts: diagnosisAttempts}
	address := net.JoinHostPort(server, fmt.Sprintf("%d", port))
	for i := 0; i < diagnosisAttempts; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		start := time.Now()
		raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if err != nil {
			cancel()
			result.LastError = normalizeProbeError(err)
			continue
		}
		// Certificate validation is deliberately disabled for this transport-only
		// probe. Receiving and completing TLS is the signal; the real Xray check
		// below validates the configured Reality/TLS connection end to end.
		conn := tls.Client(raw, &tls.Config{ServerName: serverName, InsecureSkipVerify: true}) //nolint:gosec
		err = conn.HandshakeContext(ctx)
		latency := time.Since(start).Milliseconds()
		if err == nil {
			result.Successes++
			if result.BestLatencyMs == 0 || latency < result.BestLatencyMs {
				result.BestLatencyMs = latency
			}
			state := conn.ConnectionState()
			if len(state.PeerCertificates) > 0 {
				result.PeerName = state.PeerCertificates[0].Subject.CommonName
			}
		} else {
			result.LastError = normalizeProbeError(err)
		}
		_ = conn.Close()
		cancel()
	}
	return result
}

func (pc *ProxyChecker) runBindingDiagnostics(bindings []*models.ProxyConfig, timeout time.Duration) []BindingDiagnosis {
	results := make([]BindingDiagnosis, len(bindings))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, binding := range bindings {
		i, binding := i, binding
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = pc.probeBinding(binding, timeout)
		}()
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool {
		if results[i].Name != results[j].Name {
			return results[i].Name < results[j].Name
		}
		return results[i].Port < results[j].Port
	})
	return results
}

func (pc *ProxyChecker) probeBinding(binding *models.ProxyConfig, timeout time.Duration) BindingDiagnosis {
	result := BindingDiagnosis{
		StableID: binding.StableID, HostID: binding.HostID, Name: binding.Name,
		Protocol: binding.Protocol, Security: binding.Security, Port: binding.Port,
		ServerName: binding.SNI, Attempts: diagnosisAttempts,
	}
	proxyURL, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", pc.startPort+binding.Index))
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true},
		Timeout:   timeout,
	}
	target := pc.urlTestURL
	expected := pc.urlTestExpected
	if target == "" {
		target = pc.ipCheck
		expected = ""
	}
	if target == "" {
		result.FailureStage = "configuration"
		result.LastError = "no URL test endpoint configured"
		return result
	}
	for i := 0; i < diagnosisAttempts; i++ {
		status, body, latency, err := timedProxyGET(client, target, 64*1024)
		if err != nil {
			result.FailureStage = "tunnel"
			result.LastError = normalizeProbeError(err)
			continue
		}
		valid := status >= 200 && status < 300
		if expected != "" {
			valid = valid && strings.Contains(body, expected)
		}
		if !valid {
			result.FailureStage = "response"
			result.LastError = fmt.Sprintf("unexpected response status/content (%d)", status)
			continue
		}
		result.Successes++
		if result.BestLatencyMs == 0 || latency.Milliseconds() < result.BestLatencyMs {
			result.BestLatencyMs = latency.Milliseconds()
		}
	}
	if result.Successes > 0 {
		result.FailureStage = ""
		result.LastError = ""
	}
	return result
}

func classifyNodeDiagnosis(run NodeDiagnosis) (DiagnosisVerdict, string) {
	totalBindings := len(run.Bindings)
	workingBindings := 0
	perfectBindings := 0
	for _, binding := range run.Bindings {
		if binding.Successes > 0 {
			workingBindings++
		}
		if binding.Successes == binding.Attempts && binding.Attempts > 0 {
			perfectBindings++
		}
	}
	if totalBindings > 0 && perfectBindings == totalBindings {
		return DiagnosisHealthy, fmt.Sprintf("All %d bindings passed %d/%d tunnel attempts", totalBindings, diagnosisAttempts, diagnosisAttempts)
	}
	if workingBindings > 0 {
		return DiagnosisDegraded, fmt.Sprintf("%d of %d bindings established a working tunnel", workingBindings, totalBindings)
	}

	tcpPorts := 0
	reachablePorts := 0
	for _, port := range run.Ports {
		if port.Network != "tcp" || port.Attempts == 0 {
			continue
		}
		tcpPorts++
		if port.Successes > 0 {
			reachablePorts++
		}
	}
	if tcpPorts > 0 && reachablePorts == 0 {
		return DiagnosisNetUnreachable, "Control network works, but every TCP endpoint attempt failed"
	}

	if len(run.TLS) > 0 {
		tlsWorking := 0
		for _, probe := range run.TLS {
			if probe.Successes > 0 {
				tlsWorking++
			}
		}
		if tlsWorking == 0 {
			return DiagnosisHandshakeFailed, "TCP is reachable, but configured TLS/Reality handshakes did not complete"
		}
	}
	return DiagnosisTunnelFailed, "Endpoint is reachable, but no configured Xray binding carried the control request"
}

func (pc *ProxyChecker) completeDiagnosis(run NodeDiagnosis) {
	run.State = DiagnosisCompleted
	run.Stage = "completed"
	run.CompletedAt = time.Now().Unix()
	pc.replaceDiagnosis(run)
	logger.Info("Node diagnosis completed: node=%s verdict=%s", run.NodeID, run.Verdict)
}

func (pc *ProxyChecker) manualDiagnosisPending() bool {
	pc.diagnosisMu.RLock()
	pending := pc.diagnosisRunning
	pc.diagnosisMu.RUnlock()
	return pending
}

func (pc *ProxyChecker) replaceDiagnosis(run NodeDiagnosis) {
	pc.diagnosisMu.Lock()
	history := pc.diagnosisHistory[run.NodeID]
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].RunID == run.RunID {
			history[i] = cloneNodeDiagnosis(run)
			pc.diagnosisHistory[run.NodeID] = history
			break
		}
	}
	pc.diagnosisMu.Unlock()
	if err := pc.persistDiagnoses(); err != nil {
		logger.Warn("Could not persist node diagnosis: %v", err)
	}
}

func (pc *ProxyChecker) appendDiagnosisLocked(run NodeDiagnosis) {
	history := append(pc.diagnosisHistory[run.NodeID], cloneNodeDiagnosis(run))
	if len(history) > diagnosisHistoryLimit {
		history = append([]NodeDiagnosis(nil), history[len(history)-diagnosisHistoryLimit:]...)
	}
	pc.diagnosisHistory[run.NodeID] = history
}

func (pc *ProxyChecker) persistDiagnoses() error {
	if pc.diagnosisFile == "" {
		return nil
	}
	pc.diagnosisPersistMu.Lock()
	defer pc.diagnosisPersistMu.Unlock()
	pc.diagnosisMu.RLock()
	snapshot := diagnosisSnapshot{Version: diagnosisSnapshotVersion, Nodes: make(map[string][]NodeDiagnosis, len(pc.diagnosisHistory))}
	for nodeID, history := range pc.diagnosisHistory {
		snapshot.Nodes[nodeID] = append([]NodeDiagnosis(nil), history...)
	}
	pc.diagnosisMu.RUnlock()
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode diagnosis snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(pc.diagnosisFile), 0o755); err != nil {
		return fmt.Errorf("create diagnosis directory: %w", err)
	}
	temporary := pc.diagnosisFile + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write diagnosis snapshot: %w", err)
	}
	if err := os.Rename(temporary, pc.diagnosisFile); err != nil {
		return fmt.Errorf("replace diagnosis snapshot: %w", err)
	}
	return nil
}

func (pc *ProxyChecker) nodeBindings(nodeID string) []*models.ProxyConfig {
	proxies := pc.GetProxies()
	bindings := make([]*models.ProxyConfig, 0)
	for _, proxy := range proxies {
		if proxy.NodeID == nodeID {
			bindings = append(bindings, proxy)
		}
	}
	return bindings
}

func nodeDiagnosisRevision(bindings []*models.ProxyConfig) string {
	if len(bindings) == 0 {
		return ""
	}
	revisions := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		revisions = append(revisions, binding.GenerateRevisionID())
	}
	sort.Strings(revisions)
	sum := sha256.Sum256([]byte(strings.Join(revisions, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}

func diagnosisProbeID(status NetworkStatus) string {
	if strings.TrimSpace(status.Interface) != "" {
		return "local:" + strings.TrimSpace(status.Interface)
	}
	return "local"
}

func usesTLS(proxy *models.ProxyConfig) bool {
	security := strings.ToLower(strings.TrimSpace(proxy.Security))
	return security == "tls" || security == "reality"
}

func isUDPBinding(proxy *models.ProxyConfig) bool {
	protocol := strings.ToLower(strings.TrimSpace(proxy.Protocol))
	return protocol == "hysteria" || protocol == "hysteria2" || protocol == "wireguard"
}

func normalizeProbeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(message, "timeout"):
		return "timeout"
	case strings.Contains(message, "connection reset"):
		return "connection reset"
	case strings.Contains(message, "connection refused"):
		return "connection refused"
	case strings.Contains(message, "no route"):
		return "no route to host"
	case strings.Contains(message, "eof"):
		return "connection closed during handshake"
	default:
		return err.Error()
	}
}

func cloneNodeDiagnosis(input NodeDiagnosis) NodeDiagnosis {
	result := input
	result.Ports = append([]PortDiagnosis(nil), input.Ports...)
	result.TLS = append([]TLSProbeDiagnosis(nil), input.TLS...)
	result.Bindings = append([]BindingDiagnosis(nil), input.Bindings...)
	return result
}
