package web

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceUsesSeparateLivePages(t *testing.T) {
	for _, public := range []bool{false, true} {
		var out bytes.Buffer
		if err := RenderIndex(&out, PageData{IsPublic: public}); err != nil {
			t.Fatal(err)
		}
		for _, control := range []string{`class="workspace-app"`, `id="telegram-title"`, `id="network-title"`, `ws-host-card`, `./static/workspace.css`} {
			if strings.Contains(out.String(), control) == public {
				t.Errorf("public=%v control=%q", public, control)
			}
		}
		if strings.Contains(out.String(), `x-show="alertsModalOpen"`) {
			t.Fatal("Telegram settings must be a page, not the old modal")
		}
	}
}

// Browser acceptance uses the actual Go-rendered template, not a parallel HTML mockup.
func TestCaptureWorkspaceFixture(t *testing.T) {
	dir := os.Getenv("WORKSPACE_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set WORKSPACE_CAPTURE_DIR to capture browser fixtures")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, public := range []bool{false, true} {
		var out bytes.Buffer
		data := PageData{IsPublic: public, ShowServerDetails: !public, CheckMethod: "urltest", Timeout: 4, Version: "ui-test"}
		for i, name := range []string{"🇩🇪 Германия (T1)", "🇯🇵 Япония", "🇦🇱 Албания", "🇹🇷 Турция", "🇩🇪 Германия (S24t)", "🇫🇮 Финляндия"} {
			data.Endpoints = append(data.Endpoints, EndpointInfo{Name: name, SubName: []string{"MASTER", "LETO", "220"}[i/2], StableID: "fixture-" + string(rune('a'+i)), NodeID: "node-" + string(rune('a'+i)), Protocol: "vless", ServerInfo: "192.0.2.1:443", MonitorState: "unknown", Index: i})
		}
		if err := RenderIndex(&out, data); err != nil {
			t.Fatal(err)
		}
		name := "index.html"
		if public {
			name = "public.html"
		}
		if err := os.WriteFile(filepath.Join(dir, name), out.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
