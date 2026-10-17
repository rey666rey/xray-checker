package web

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPanelTopReferenceLayout(t *testing.T) {
	for _, public := range []bool{false, true} {
		var out bytes.Buffer
		if err := RenderIndex(&out, PageData{IsPublic: public, ShowServerDetails: true, Host: "test-host", Port: "2113"}); err != nil {
			t.Fatal(err)
		}
		html := out.String()
		for _, want := range []string{`data-panel-top`, `Мониторинг узлов`, `Всего узлов`, `x-text="stats.offline"`, `x-text="summaryPanelNote"`, `@click="refreshData()"`, `aria-label="Автообновление"`, `refreshSeconds: 4,`} {
			if !strings.Contains(html, want) {
				t.Fatalf("public=%v missing %q", public, want)
			}
		}
		start := strings.Index(html, `<header x-show="showHeader"`)
		end := strings.Index(html[start:], `</header>`) + start
		header := html[start:end]
		if strings.Contains(header, "test-host") || strings.Contains(header, "toggleDarkMode()") {
			t.Fatalf("public=%v masthead contains legacy extras", public)
		}
		if strings.Contains(header, `class="panel-top-source"`) == public {
			t.Fatalf("public=%v source controls visibility", public)
		}
		if strings.Contains(header, `class="panel-top-nav"`) == public {
			t.Fatalf("public=%v navigation visibility", public)
		}
		if !public && (!strings.Contains(html, `@click="reconnectSelectedNetwork()"`) || !strings.Contains(html, `@click="switchNetworkSource(sourceId)"`)) {
			t.Fatal("network actions were lost")
		}
	}
}

func TestPanelTopCapture(t *testing.T) {
	dir := os.Getenv("TOP_CAPTURE_DIR")
	if dir == "" {
		t.Skip("capture fixture not requested")
	}
	for _, public := range []bool{false, true} {
		endpoints := []EndpointInfo{
			{Name: "Германия", SubName: "MASTER", StableID: "one", NodeID: "one", Status: true, MonitorState: "healthy", Latency: 778 * time.Millisecond, LastCheck: 1700000000},
			{Name: "Швеция", SubName: "LETO", StableID: "two", NodeID: "two", Status: true, MonitorState: "healthy", Latency: 778 * time.Millisecond, LastCheck: 1700000000},
			{Name: "Япония", SubName: "MOBILE", StableID: "three", NodeID: "three", MonitorState: "needs_replacement", Failures: 3, LastCheck: 1700000000},
			{Name: "Канада", SubName: "DOG", StableID: "four", NodeID: "four", MonitorState: "unknown"},
		}
		var out bytes.Buffer
		if err := RenderIndex(&out, PageData{IsPublic: public, ShowServerDetails: !public, Endpoints: endpoints}); err != nil {
			t.Fatal(err)
		}
		name := "private.html"
		if public {
			name = "public.html"
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), out.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
