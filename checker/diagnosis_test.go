package checker

import (
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
