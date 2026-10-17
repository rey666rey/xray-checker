package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"xray-checker/checker"
)

func TestMonitoringAPIReportsReadOnlyQueueStatus(t *testing.T) {
	pc := checker.NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 30)
	handler := APIMonitoringHandler(pc)
	response := httptest.NewRecorder()
	handler(response, httptest.NewRequest(http.MethodGet, "/api/v1/monitoring", nil))
	var body struct {
		Success bool                     `json:"success"`
		Data    checker.MonitoringStatus `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !body.Success || body.Data.Mode != "normal" || body.Data.WindowSeconds != 900 {
		t.Fatalf("unexpected monitoring response: %s", response.Body.String())
	}
	response = httptest.NewRecorder()
	handler(response, httptest.NewRequest(http.MethodPost, "/api/v1/monitoring", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatal("monitoring endpoint accepted a mutation")
	}
}

func TestMonitoringBarIsPrivate(t *testing.T) {
	for _, public := range []bool{false, true} {
		var output bytes.Buffer
		if err := RenderIndex(&output, PageData{IsPublic: public, Endpoints: []EndpointInfo{{Name: "Test binding"}}}); err != nil {
			t.Fatal(err)
		}
		contains := strings.Contains(output.String(), "Pending checks:")
		if contains == public {
			t.Fatalf("monitoring bar public=%v, present=%v", public, contains)
		}
	}
}
