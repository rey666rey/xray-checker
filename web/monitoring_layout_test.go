package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"xray-checker/checker"
)

func TestMonitoringReferenceLayout(t *testing.T) {
	for _, public := range []bool{false, true} {
		var out bytes.Buffer
		if err := RenderIndex(&out, PageData{IsPublic: public, ShowServerDetails: !public}); err != nil {
			t.Fatal(err)
		}
		html := out.String()
		for _, want := range []string{`data-monitor-workspace`, `panelVisibleProxies(panel.name)`, `rows.slice(0, 4)`, `monitorPageSize: 24`, `monitorHostView(proxy)`, `Открыть панель ↗`, `monitor-local-search`, `ui-monospace`, `monitorDetailsRows(panel.name)`, `:aria-pressed="panelState(panel.name).filter`} {
			if !strings.Contains(html, want) {
				t.Fatalf("public=%v missing %q", public, want)
			}
		}
		start := strings.Index(html, `data-monitor-workspace`)
		end := strings.Index(html[start:], `x-show="showServersHeader`) + start
		monitor := html[start:end]
		if strings.Contains(monitor, `@click="diagnoseNode(proxy.nodeId, $event)"`) == public {
			t.Fatalf("public=%v diagnosis actions visibility", public)
		}
		if strings.Contains(monitor, `class="monitor-host-address"`) == public {
			t.Fatalf("public=%v address visibility", public)
		}
	}
}

func TestMonitoringCapture(t *testing.T) {
	dir := os.Getenv("MONITOR_CAPTURE_DIR")
	if dir == "" {
		t.Skip("capture fixture not requested")
	}
	for _, public := range []bool{false, true} {
		var nodes []EndpointInfo
		groups := []string{"MASTER", "LETO", "MASTER · MOBILE", "DOG"}
		names := []string{"🇩🇪 Германия · PR (DE-01 v3)", "🇸🇪 Швеция · Узел 2 (v3)", "🇸🇪 Швеция · Узел 45 (v3)", "🇸🇪 Швеция · Узел 3 (v3)"}
		for gi, group := range groups {
			for i := 0; i < 30; i++ {
				id := fmt.Sprintf("node-%d-%d", gi, i)
				node := EndpointInfo{Name: names[i%4], SubName: group, StableID: id, NodeID: id, Index: gi*30 + i, Protocol: "hysteria", ServerInfo: fmt.Sprintf("192.0.2.%d:2083", i+1), Status: true, MonitorState: "healthy", Latency: time.Duration(98+i*11) * time.Millisecond, LastCheck: 1700000000}
				if i == 0 {
					node.Name = "🇩🇪 Германия · PR (DE-01 v3)"
					node.Status = false
					node.MonitorState = "needs_replacement"
					node.Failures = 3
					node.Latency = 0
					node.Diagnosis = &checker.BindingDiagnosisSummary{State: "completed", Verdict: "tunnel_failed", CompletedAt: 1700000000}
				}
				if i == 2 {
					node.Status = false
					node.MonitorState = "suspected"
					node.Failures = 1
					node.Latency = 0
				}
				if gi == 1 {
					node.Name = "🇦🇱 Албания · Tirana (v2)"
					if i%2 == 1 {
						node.Name = "🇯🇵 Япония · Tokyo (v3)"
					}
					node.Protocol = "vless"
				}
				// An online endpoint with an old transitional state must still read "Работает".
				if i == 5 {
					node.MonitorState = "unknown"
				}
				if gi == 0 && i == 29 {
					node.Name = "🇩🇪 Очень длинное название узла для проверки усечения и адаптивности"
					node.ServerInfo = "[2001:db8:1234:5678::10]:443"
				}
				nodes = append(nodes, node)
			}
		}
		var out bytes.Buffer
		if err := RenderIndex(&out, PageData{IsPublic: public, ShowServerDetails: !public, Endpoints: nodes}); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		name := "private.html"
		if public {
			name = "public.html"
		}
		if err := os.WriteFile(filepath.Join(dir, name), out.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
