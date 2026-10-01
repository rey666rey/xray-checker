package checker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"xray-checker/models"
)

func TestClassifyNodeDiagnosis(t *testing.T) {
	tests := []struct {
		name string
		run  NodeDiagnosis
		want DiagnosisVerdict
	}{
		{
			name: "healthy when every binding passes every attempt",
			run: NodeDiagnosis{Bindings: []BindingDiagnosis{
				{Attempts: 3, Successes: 3}, {Attempts: 3, Successes: 3},
			}},
			want: DiagnosisHealthy,
		},
		{
			name: "degraded when only one binding works",
			run: NodeDiagnosis{Bindings: []BindingDiagnosis{
				{Attempts: 3, Successes: 1}, {Attempts: 3, Successes: 0},
			}},
			want: DiagnosisDegraded,
		},
		{
			name: "network unreachable when tcp never connects",
			run: NodeDiagnosis{
				Ports:    []PortDiagnosis{{Network: "tcp", Attempts: 3, Successes: 0}},
				Bindings: []BindingDiagnosis{{Attempts: 3, Successes: 0}},
			},
			want: DiagnosisNetUnreachable,
		},
		{
			name: "handshake failure after tcp connects",
			run: NodeDiagnosis{
				Ports:    []PortDiagnosis{{Network: "tcp", Attempts: 3, Successes: 3}},
				TLS:      []TLSProbeDiagnosis{{Attempts: 3, Successes: 0}},
				Bindings: []BindingDiagnosis{{Attempts: 3, Successes: 0}},
			},
			want: DiagnosisHandshakeFailed,
		},
		{
			name: "tunnel failure when endpoint layers respond",
			run: NodeDiagnosis{
				Ports:    []PortDiagnosis{{Network: "tcp", Attempts: 3, Successes: 3}},
				TLS:      []TLSProbeDiagnosis{{Attempts: 3, Successes: 3}},
				Bindings: []BindingDiagnosis{{Attempts: 3, Successes: 0}},
			},
			want: DiagnosisTunnelFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := classifyNodeDiagnosis(tt.run)
			if got != tt.want {
				t.Fatalf("classifyNodeDiagnosis()=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestAllTCPPortsUnreachable(t *testing.T) {
	tests := []struct {
		name  string
		ports []PortDiagnosis
		want  bool
	}{
		{name: "all tcp attempts failed", ports: []PortDiagnosis{{Network: "tcp", Attempts: 3}}, want: true},
		{name: "one tcp endpoint worked", ports: []PortDiagnosis{{Network: "tcp", Attempts: 3}, {Network: "tcp", Attempts: 3, Successes: 1}}, want: false},
		{name: "udp only is inconclusive", ports: []PortDiagnosis{{Network: "udp", Attempts: 0}}, want: false},
		{name: "no endpoints", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allTCPPortsUnreachable(tt.ports); got != tt.want {
				t.Fatalf("allTCPPortsUnreachable()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetDiagnosisFileMarksInterruptedRunInconclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnoses.json")
	data := `{"version":1,"nodes":{"node-1":[{"runId":"run-1","nodeId":"node-1","state":"running","startedAt":1}]}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	checker := NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	if err := checker.SetDiagnosisFile(path); err != nil {
		t.Fatal(err)
	}
	history := checker.GetNodeDiagnosisHistory("node-1")
	if len(history) != 1 {
		t.Fatalf("history len=%d, want 1", len(history))
	}
	if history[0].State != DiagnosisCompleted || history[0].Verdict != DiagnosisInconclusive {
		t.Fatalf("interrupted diagnosis=%#v", history[0])
	}
}

func TestDiagnosisIsStale(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	tests := []struct {
		name string
		run  NodeDiagnosis
		rev  string
		want bool
	}{
		{name: "fresh completed result", run: NodeDiagnosis{Revision: "r1", State: DiagnosisCompleted, Verdict: DiagnosisHandshakeFailed, CompletedAt: now.Add(-time.Minute).Unix()}, rev: "r1"},
		{name: "changed configuration", run: NodeDiagnosis{Revision: "r1", State: DiagnosisCompleted, Verdict: DiagnosisHandshakeFailed, CompletedAt: now.Unix()}, rev: "r2", want: true},
		{name: "expired completed result", run: NodeDiagnosis{Revision: "r1", State: DiagnosisCompleted, Verdict: DiagnosisHandshakeFailed, CompletedAt: now.Add(-diagnosisFreshDuration - time.Second).Unix()}, rev: "r1", want: true},
		{name: "inconclusive expires quickly", run: NodeDiagnosis{Revision: "r1", State: DiagnosisCompleted, Verdict: DiagnosisInconclusive, CompletedAt: now.Add(-diagnosisInconclusiveFreshFor - time.Second).Unix()}, rev: "r1", want: true},
		{name: "running result remains current", run: NodeDiagnosis{Revision: "r1", State: DiagnosisRunning, StartedAt: now.Add(-time.Hour).Unix()}, rev: "r1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diagnosisIsStale(tt.run, tt.rev, now); got != tt.want {
				t.Fatalf("diagnosisIsStale()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeDiagnosisBecomesStaleAfterNewFailedCheck(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	proxy := &models.ProxyConfig{
		StableID: "binding-1", LogicalID: "logical-1", HostID: "host-1", NodeID: "node-1",
		Name: "Node", Protocol: "vless", Security: "tls", Server: "192.0.2.10", Port: 443,
	}
	pc := NewProxyChecker([]*models.ProxyConfig{proxy}, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	pc.diagnosisHistory["node-1"] = []NodeDiagnosis{{
		NodeID: "node-1", Revision: nodeDiagnosisRevision([]*models.ProxyConfig{proxy}),
		State: DiagnosisCompleted, Verdict: DiagnosisHealthy, CompletedAt: now.Unix(),
	}}
	pc.results.Store(proxyMetricKey(proxy), proxyResult{status: false, lastCheck: now.Add(time.Second)})

	run, ok := pc.GetNodeDiagnosis("node-1")
	if !ok || !run.Stale {
		t.Fatalf("diagnosis=%#v, found=%v; want stale after newer failed check", run, ok)
	}
}

func TestApplyDiagnosisEvidencePromotesOnlyWorkingBindings(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	working := &models.ProxyConfig{
		StableID: "working", LogicalID: "working-logical", HostID: "working-host", NodeID: "shared-node",
		Name: "Working", Protocol: "vless", Server: "192.0.2.10", Port: 443,
	}
	failing := &models.ProxyConfig{
		StableID: "failing", LogicalID: "failing-logical", HostID: "failing-host", NodeID: "shared-node",
		Name: "Failing", Protocol: "trojan", Server: "192.0.2.10", Port: 8443,
	}
	bindings := []*models.ProxyConfig{working, failing}
	pc := NewProxyChecker(bindings, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	pc.results.Store(proxyMetricKey(working), proxyResult{status: false, lastCheck: now.Add(-time.Minute), exitIP: "198.51.100.7"})
	pc.results.Store(proxyMetricKey(failing), proxyResult{status: false, lastCheck: now.Add(-time.Minute)})

	pc.applyDiagnosisEvidence(NodeDiagnosis{
		NodeID: "shared-node", Revision: nodeDiagnosisRevision(bindings), State: DiagnosisCompleted,
		CompletedAt: now.Unix(), Bindings: []BindingDiagnosis{
			{StableID: "working", Attempts: 3, Successes: 3, BestLatencyMs: 42},
			{StableID: "failing", Attempts: 3, Successes: 0},
		},
	}, now)

	workingResult := pc.resultsForKey(proxyMetricKey(working))
	if !workingResult.status || workingResult.unstable || workingResult.latency != 42*time.Millisecond {
		t.Fatalf("working result = %#v, want stable online at 42ms", workingResult)
	}
	if workingResult.exitIP != "198.51.100.7" {
		t.Fatalf("working exit IP = %q, want preserved value", workingResult.exitIP)
	}
	failingResult := pc.resultsForKey(proxyMetricKey(failing))
	if failingResult.status {
		t.Fatalf("failing result = %#v, want existing offline result unchanged", failingResult)
	}
	monitor, ok := pc.GetNodeMonitorByStableID("working")
	if !ok || monitor.State != NodeHealthy || monitor.ConsecutiveFailures != 0 {
		t.Fatalf("working monitor = %#v, found=%v", monitor, ok)
	}
	if len(monitor.History) == 0 || monitor.History[len(monitor.History)-1].Type != "diagnosis" {
		t.Fatalf("working monitor history = %#v, want diagnosis event", monitor.History)
	}
	metrics := pc.MetricsSnapshot()
	if len(metrics) != 2 || !metrics[0].Online || metrics[1].Online {
		t.Fatalf("metrics snapshot = %#v, want only working binding online", metrics)
	}
}

func TestApplyDiagnosisEvidenceMarksPartialSuccessUnstable(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	proxy := &models.ProxyConfig{
		StableID: "binding-1", LogicalID: "logical-1", HostID: "host-1", NodeID: "node-1",
		Name: "Node", Protocol: "vless", Server: "192.0.2.10", Port: 443,
	}
	pc := NewProxyChecker([]*models.ProxyConfig{proxy}, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	pc.results.Store(proxyMetricKey(proxy), proxyResult{status: false, lastCheck: now.Add(-time.Minute)})

	pc.applyDiagnosisEvidence(NodeDiagnosis{
		NodeID: "node-1", Revision: nodeDiagnosisRevision([]*models.ProxyConfig{proxy}),
		State: DiagnosisCompleted, CompletedAt: now.Unix(),
		Bindings: []BindingDiagnosis{{StableID: "binding-1", Attempts: 3, Successes: 1, BestLatencyMs: 125}},
	}, now)

	result := pc.resultsForKey(proxyMetricKey(proxy))
	if !result.status || !result.unstable || result.latency != 125*time.Millisecond {
		t.Fatalf("partial result = %#v, want unstable online at 125ms", result)
	}
	if result.lastError != "deep diagnosis passed 1/3 tunnel attempts" {
		t.Fatalf("partial error = %q", result.lastError)
	}
	monitor, _ := pc.GetNodeMonitorByStableID("binding-1")
	if monitor.State != NodeUnstable {
		t.Fatalf("partial monitor state = %s, want %s", monitor.State, NodeUnstable)
	}
}

func TestApplyDiagnosisEvidenceDoesNotOverwriteNewerOrChangedResult(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	proxy := &models.ProxyConfig{
		StableID: "binding-1", LogicalID: "logical-1", HostID: "host-1", NodeID: "node-1",
		Name: "Node", Protocol: "vless", Server: "192.0.2.10", Port: 443,
	}
	pc := NewProxyChecker([]*models.ProxyConfig{proxy}, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	newer := proxyResult{status: false, lastCheck: now.Add(time.Second), lastError: "newer failure"}
	pc.results.Store(proxyMetricKey(proxy), newer)
	run := NodeDiagnosis{
		NodeID: "node-1", Revision: nodeDiagnosisRevision([]*models.ProxyConfig{proxy}),
		State: DiagnosisCompleted, CompletedAt: now.Unix(),
		Bindings: []BindingDiagnosis{{StableID: "binding-1", Attempts: 3, Successes: 3}},
	}

	pc.applyDiagnosisEvidence(run, now)
	if got := pc.resultsForKey(proxyMetricKey(proxy)); got.status || got.lastError != newer.lastError {
		t.Fatalf("newer result overwritten: %#v", got)
	}

	pc.results.Store(proxyMetricKey(proxy), proxyResult{status: false, lastCheck: now.Add(-time.Minute)})
	run.Revision = "old-revision"
	pc.applyDiagnosisEvidence(run, now)
	if got := pc.resultsForKey(proxyMetricKey(proxy)); got.status {
		t.Fatalf("revision-mismatched diagnosis promoted result: %#v", got)
	}
}

func TestSetDiagnosisFileReconcilesFreshSavedSuccess(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	proxy := &models.ProxyConfig{
		StableID: "binding-1", LogicalID: "logical-1", HostID: "host-1", NodeID: "node-1",
		Name: "Node", Protocol: "vless", Server: "192.0.2.10", Port: 443,
	}
	pc := NewProxyChecker([]*models.ProxyConfig{proxy}, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	pc.results.Store(proxyMetricKey(proxy), proxyResult{status: false, lastCheck: now.Add(-time.Minute)})
	snapshot := diagnosisSnapshot{Version: diagnosisSnapshotVersion, Nodes: map[string][]NodeDiagnosis{
		"node-1": {{
			RunID: "saved-run", NodeID: "node-1", Revision: nodeDiagnosisRevision([]*models.ProxyConfig{proxy}),
			State: DiagnosisCompleted, Verdict: DiagnosisHealthy, CompletedAt: now.Unix(),
			Bindings: []BindingDiagnosis{{StableID: "binding-1", Attempts: 3, Successes: 3, BestLatencyMs: 37}},
		}},
	}}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "diagnoses.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := pc.SetDiagnosisFile(path); err != nil {
		t.Fatalf("SetDiagnosisFile() error = %v", err)
	}
	result := pc.resultsForKey(proxyMetricKey(proxy))
	if !result.status || result.unstable || result.latency != 37*time.Millisecond {
		t.Fatalf("restored diagnosis result = %#v, want stable online at 37ms", result)
	}
}

func (pc *ProxyChecker) resultsForKey(key proxyMetricLabels) proxyResult {
	value, _ := pc.results.Load(key)
	return value.(proxyResult)
}

func TestGetBindingDiagnosisSeparatesBindingsOnSameNode(t *testing.T) {
	tlsBinding := &models.ProxyConfig{
		StableID: "tls-binding", LogicalID: "tls-logical", HostID: "tls-host", NodeID: "shared-node",
		Name: "TLS", Protocol: "vless", Security: "tls", Server: "192.0.2.10", Port: 443, SNI: "example.com",
	}
	udpBinding := &models.ProxyConfig{
		StableID: "hy2-binding", LogicalID: "hy2-logical", HostID: "hy2-host", NodeID: "shared-node",
		Name: "Hysteria", Protocol: "hysteria2", Server: "192.0.2.10", Port: 8443,
	}
	bindings := []*models.ProxyConfig{tlsBinding, udpBinding}
	pc := NewProxyChecker(bindings, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	pc.diagnosisHistory["shared-node"] = []NodeDiagnosis{{
		NodeID: "shared-node", Revision: nodeDiagnosisRevision(bindings), State: DiagnosisCompleted,
		Verdict: DiagnosisHandshakeFailed, CompletedAt: time.Now().Unix(),
		Ports: []PortDiagnosis{{Port: 443, Network: "tcp", Attempts: 3, Successes: 3}},
		TLS:   []TLSProbeDiagnosis{{Port: 443, ServerName: "example.com", Attempts: 3}},
		Bindings: []BindingDiagnosis{
			{StableID: "tls-binding", Attempts: 3},
			{StableID: "hy2-binding", Attempts: 3},
		},
	}}

	tlsResult, ok := pc.GetBindingDiagnosis("tls-binding")
	if !ok || tlsResult.Verdict != DiagnosisHandshakeFailed {
		t.Fatalf("TLS binding diagnosis=%#v, found=%v", tlsResult, ok)
	}
	udpResult, ok := pc.GetBindingDiagnosis("hy2-binding")
	if !ok || udpResult.Verdict != DiagnosisTunnelFailed {
		t.Fatalf("Hysteria binding diagnosis=%#v, found=%v", udpResult, ok)
	}
}

func TestAutomaticDiagnosisQueuesOnlyAfterRepeatedFailure(t *testing.T) {
	proxy := &models.ProxyConfig{
		StableID: "binding-1", LogicalID: "logical-1", HostID: "host-1", NodeID: "node-1",
		Name: "Node", Protocol: "vless", Security: "tls", Server: "192.0.2.10", Port: 443,
	}
	pc := NewProxyChecker([]*models.ProxyConfig{proxy}, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	pc.monitor = map[string]*NodeMonitorState{
		"logical-1": {LogicalID: "logical-1", ConsecutiveFailures: automaticDiagnosisFailureThreshold - 1, LastCheck: time.Now().Unix()},
	}

	pc.enqueueAutomaticDiagnoses([]*models.ProxyConfig{proxy})
	if len(pc.diagnosisQueue) != 0 {
		t.Fatal("automatic diagnosis queued after only one failure")
	}

	pc.monitor["logical-1"].ConsecutiveFailures = automaticDiagnosisFailureThreshold
	pc.enqueueAutomaticDiagnoses([]*models.ProxyConfig{proxy})
	if len(pc.diagnosisQueue) != 1 {
		t.Fatalf("automatic diagnosis queue len=%d, want 1", len(pc.diagnosisQueue))
	}
	result, ok := pc.GetBindingDiagnosis("binding-1")
	if !ok || result.State != DiagnosisQueued || result.Trigger != "automatic" {
		t.Fatalf("queued binding diagnosis=%#v, found=%v", result, ok)
	}

	pc.enqueueAutomaticDiagnoses([]*models.ProxyConfig{proxy})
	if len(pc.diagnosisQueue) != 1 {
		t.Fatalf("duplicate automatic diagnosis was queued; len=%d", len(pc.diagnosisQueue))
	}
}
