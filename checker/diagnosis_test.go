package checker

import (
	"os"
	"path/filepath"
	"testing"
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
