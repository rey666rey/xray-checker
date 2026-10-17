package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPanelFinishingControls(t *testing.T) {
	for _, public := range []bool{false, true} {
		var out bytes.Buffer
		if err := RenderIndex(&out, PageData{IsPublic: public, ShowServerDetails: !public, Timeout: 4}); err != nil {
			t.Fatal(err)
		}
		html := out.String()
		for _, want := range []string{`class="dashboard-footer"`, `таймаут 4 с`, `UTC`, `toggleMonitorPanel(panel.name)`, `:aria-expanded="!isMonitorPanelCollapsed(panel.name)"`, `monitorCollapsedPanels`, `class="monitor-panel-body"`, `this.lastSyncedAt = Date.now()`} {
			if !strings.Contains(html, want) {
				t.Fatalf("public=%v missing %q", public, want)
			}
		}
		if strings.Contains(html, `class="monitor-copy-ip"`) == public {
			t.Fatalf("public=%v quick-copy visibility", public)
		}
		footer := html[strings.Index(html, `class="dashboard-footer"`):]
		footer = footer[:strings.Index(footer, `</footer>`)]
		if strings.Contains(footer, "GitHub") || strings.Contains(footer, "Telegram Chat") || strings.Contains(footer, "Made with") {
			t.Fatal("legacy footer links remain")
		}
	}
}

func TestPanelFinishingCapture(t *testing.T) {
	dir := os.Getenv("FINISH_CAPTURE_DIR")
	if dir == "" {
		t.Skip("capture fixture not requested")
	}
	for _, public := range []bool{false, true} {
		var nodes []EndpointInfo
		for gi, g := range []string{"MASTER", "220V", "LETO", "DOG"} {
			for i := 0; i < 6; i++ {
				id := fmt.Sprintf("finish-%d-%d", gi, i)
				node := EndpointInfo{Name: fmt.Sprintf("🇩🇪 Узел %d", i+1), SubName: g, StableID: id, NodeID: id, Index: gi*6 + i, Protocol: "vless", ServerInfo: fmt.Sprintf("192.0.2.%d:443", i+1), Status: true, MonitorState: "healthy", Latency: 142 * time.Millisecond, LastCheck: 1700000000}
				if i == 0 {
					node.Status = false
					node.MonitorState = "suspected"
					node.Failures = 1
					node.Latency = 0
				}
				if i == 1 {
					node.ServerInfo = "[2001:db8::2]:443"
				}
				nodes = append(nodes, node)
			}
		}
		var out bytes.Buffer
		if err := RenderIndex(&out, PageData{IsPublic: public, ShowServerDetails: !public, Timeout: 4, CheckInterval: 600, Endpoints: nodes}); err != nil {
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
