package checker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xray-checker/networksource"
)

func TestSourceGatePausesOnlyTransitionsAndRejectsWrongSession(t *testing.T) {
	dir := t.TempDir()
	epoch := strings.Repeat("a", 32)
	pc := NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	statusFile := filepath.Join(t.TempDir(), "network.json")
	pc.SetNetworkStatusFile(statusFile, time.Minute)
	pc.SetNetworkSource(dir, "iphone", epoch)
	write := func(path string, v any) {
		t.Helper()
		data, _ := json.Marshal(v)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	status := NetworkStatus{SourceID: "iphone", Epoch: epoch, State: "connected", Interface: "col0", UpdatedAt: time.Now().Unix()}
	write(statusFile, status)
	snapshot := networksource.Snapshot{Version: 1, Enabled: true, ActiveSourceID: "iphone", Epoch: epoch, UpdatedAt: time.Now().Unix()}
	write(filepath.Join(dir, networksource.StatusName), snapshot)
	if !pc.GetNetworkStatus().Ready {
		t.Fatal("valid source was rejected")
	}
	write(filepath.Join(dir, networksource.RequestName), networksource.Request{Action: "validate"})
	if !pc.GetNetworkStatus().Ready {
		t.Fatal("read-only preflight paused monitoring")
	}
	write(filepath.Join(dir, networksource.RequestName), networksource.Request{Action: "activate"})
	if s := pc.GetNetworkStatus(); s.Ready || s.State != "switching" {
		t.Fatalf("activation gate not closed: %#v", s)
	}
	_ = os.Remove(filepath.Join(dir, networksource.RequestName))
	snapshot.Epoch = strings.Repeat("b", 32)
	write(filepath.Join(dir, networksource.StatusName), snapshot)
	if pc.GetNetworkStatus().Ready {
		t.Fatal("old runtime accepted new source epoch")
	}
	snapshot.Epoch = epoch
	snapshot.UpdatedAt = time.Now().Add(time.Minute).Unix()
	write(filepath.Join(dir, networksource.StatusName), snapshot)
	if pc.GetNetworkStatus().Ready {
		t.Fatal("future heartbeat accepted")
	}
}

func TestManagedSourceIPNeverFallsBackToDefaultNetwork(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; _, _ = w.Write([]byte("203.0.113.9")) }))
	defer server.Close()
	pc := NewProxyChecker(nil, 10000, server.URL, 1, "", "", 1, 1, "urltest", 1)
	pc.SetNetworkStatusFile(filepath.Join(t.TempDir(), "missing"), time.Minute)
	pc.setCurrentIP("old-ip")
	if _, err := pc.GetCurrentIP(); err == nil {
		t.Fatal("managed route returned a stale/default IP")
	}
	if called {
		t.Fatal("managed source leaked an IP request to default transport")
	}
}
