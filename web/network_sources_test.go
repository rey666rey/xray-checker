package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xray-checker/networksource"
)

func sourceFixture(t *testing.T) (string, networksource.Snapshot) {
	t.Helper()
	dir := t.TempDir()
	s := networksource.Snapshot{Version: 1, Enabled: true, ActiveSourceID: "iphone", Epoch: strings.Repeat("a", 32), UpdatedAt: time.Now().Unix(), Job: networksource.Job{State: "idle"}, Interfaces: []networksource.Interface{{Name: "en7", Label: "iPhone USB", Address: "172.20.10.2"}, {Name: "en9", Label: "Ethernet", Address: "192.168.8.2"}}, Profiles: []networksource.Profile{{ID: "iphone", Label: "iPhone USB", Interface: "en7"}, {ID: "router", Label: "LTE router"}}}
	data, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(dir, networksource.StatusName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, s
}

func sourcePOST(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/network/sources", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Xray-Action", "network-source")
	r.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func TestSourceActionsAreTypedAndDurable(t *testing.T) {
	dir, s := sourceFixture(t)
	handler := APINetworkSourcesHandler(dir, "", "")
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"action":"activate","sourceId":"router","interface":"en9","expectedEpoch":"old"}`, 409},
		{`{"action":"execute","sourceId":"router","interface":"en9"}`, 400},
		{`{"action":"activate","sourceId":"router","interface":"en9;id"}`, 400},
		{`{"action":"activate","sourceId":"router","interface":"en9","command":"id"}`, 400},
		{`{} {}`, 400},
		{`{"action":"activate","sourceId":"router","interface":"en88","expectedEpoch":"` + s.Epoch + `"}`, 400},
		{`{"action":"configure","sourceId":"iphone","interface":"en7","expectedEpoch":"` + s.Epoch + `"}`, 409},
	} {
		w := sourcePOST(handler, tc.body)
		if w.Code != tc.status {
			t.Fatalf("%s: status=%d body=%s", tc.body, w.Code, w.Body.String())
		}
	}
	body := `{"action":"activate","sourceId":"router","interface":"en9","expectedEpoch":"` + s.Epoch + `"}`
	if w := sourcePOST(handler, body); w.Code != 202 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var request networksource.Request
	data, err := os.ReadFile(filepath.Join(dir, networksource.RequestName))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request.SourceID != "router" || request.Interface != "en9" || request.ExpectedEpoch != s.Epoch {
		t.Fatalf("bad request: %#v", request)
	}
	info, _ := os.Stat(filepath.Join(dir, networksource.RequestName))
	if info.Mode().Perm() != 0o600 {
		t.Fatal("request permissions are not private")
	}
	if w := sourcePOST(handler, body); w.Code != 409 {
		t.Fatalf("duplicate status=%d", w.Code)
	}
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest(http.MethodGet, "/api/v1/network/sources", nil))
	if !strings.Contains(w.Body.String(), `"state":"queued"`) {
		t.Fatalf("durable queued state not visible: %s", w.Body.String())
	}
}

func TestSourceAdmissionSerializesConcurrentRequests(t *testing.T) {
	dir, s := sourceFixture(t)
	handler := APINetworkSourcesHandler(dir, "", "")
	body := `{"action":"validate","sourceId":"router","interface":"en9","expectedEpoch":"` + s.Epoch + `"}`
	var wg sync.WaitGroup
	statuses := make(chan int, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); statuses <- sourcePOST(handler, body).Code }()
	}
	wg.Wait()
	close(statuses)
	accepted := 0
	for code := range statuses {
		if code == 202 {
			accepted++
		} else if code != 409 {
			t.Fatalf("unexpected status %d", code)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d requests", accepted)
	}
}

func TestSourceAPIDisabledStaleAndCrossOrigin(t *testing.T) {
	if w := sourcePOST(APINetworkSourcesHandler("", "", ""), `{}`); w.Code != 503 {
		t.Fatal(w.Code)
	}
	dir, s := sourceFixture(t)
	handler := APINetworkSourcesHandler(dir, "", "")
	r := httptest.NewRequest("POST", "http://example.com/api/v1/network/sources", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Xray-Action", "network-source")
	r.Header.Set("Origin", "http://other.example")
	w := httptest.NewRecorder()
	handler(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin status %d", w.Code)
	}
	s.UpdatedAt = time.Now().Add(-time.Minute).Unix()
	data, _ := json.Marshal(s)
	_ = os.WriteFile(filepath.Join(dir, networksource.StatusName), data, 0o600)
	if w := sourcePOST(handler, `{}`); w.Code != 503 {
		t.Fatalf("stale status %d", w.Code)
	}
	w = httptest.NewRecorder()
	handler(w, httptest.NewRequest("GET", "/api/v1/network/sources", nil))
	if !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatal(w.Body.String())
	}
}

func TestRouterDisablesLegacyPhoneReconnect(t *testing.T) {
	dir, s := sourceFixture(t)
	s.ActiveSourceID = "router"
	data, _ := json.Marshal(s)
	_ = os.WriteFile(filepath.Join(dir, networksource.StatusName), data, 0o600)
	r := httptest.NewRequest("POST", "http://example.com/api/v1/network/reconnect", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Xray-Action", "reconnect-iphone")
	w := httptest.NewRecorder()
	APINetworkRecoveryHandler(filepath.Join(t.TempDir(), "request"), filepath.Join(t.TempDir(), "status"), dir)(w, r)
	if w.Code != 409 {
		t.Fatalf("router unexpectedly accepted phone reconnect: %d", w.Code)
	}
}

func TestNetworkSourceControlsArePrivateAndRender(t *testing.T) {
	for _, public := range []bool{false, true} {
		var out bytes.Buffer
		if err := RenderIndex(&out, PageData{IsPublic: public, Endpoints: []EndpointInfo{{Name: "Test master", SubName: "MASTER", StableID: "test-master", MonitorState: "unknown"}, {Name: "Test leto", SubName: "LETO", StableID: "test-leto", MonitorState: "unknown"}, {Name: "Test 220", SubName: "220", StableID: "test-220", MonitorState: "unknown"}}}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "Использовать эту сеть") == public {
			t.Fatalf("public=%v source controls exposure", public)
		}
		if dir := os.Getenv("UI_CAPTURE_DIR"); dir != "" && !public {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "index.html"), out.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
