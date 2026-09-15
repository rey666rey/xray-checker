package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkRecoveryHandlerQueuesReconnect(t *testing.T) {
	directory := t.TempDir()
	requestFile := filepath.Join(directory, "iphone-recovery.request")
	statusFile := filepath.Join(directory, "iphone-recovery-status.json")
	handler := APINetworkRecoveryHandler(requestFile, statusFile)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/network/reconnect", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Xray-Action", "reconnect-iphone")
	request.Header.Set("Origin", "http://example.com")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data NetworkRecoveryStatus `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Data.Enabled || !response.Data.Active || response.Data.State != "queued" || response.Data.RequestID == "" {
		t.Fatalf("response=%#v", response.Data)
	}
	if _, err := os.Stat(requestFile); err != nil {
		t.Fatalf("request file was not queued: %v", err)
	}

	duplicate := httptest.NewRecorder()
	handler.ServeHTTP(duplicate, request.Clone(request.Context()))
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
}

func TestNetworkRecoveryHandlerRejectsCrossOriginFormPost(t *testing.T) {
	handler := APINetworkRecoveryHandler(
		filepath.Join(t.TempDir(), "request"),
		filepath.Join(t.TempDir(), "status"),
	)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/network/reconnect", strings.NewReader(""))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://malicious.example")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestNetworkRecoveryHandlerReportsDisabled(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/network/reconnect", nil)
	APINetworkRecoveryHandler("", "").ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"enabled":false`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
