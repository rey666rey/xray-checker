package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"xray-checker/alerts"
	"xray-checker/checker"
)

func TestTelegramAlertsStatusIsPrivateAndNeverReturnsToken(t *testing.T) {
	proxyChecker := checker.NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	manager, err := alerts.NewManager(proxyChecker, 10000, filepath.Join(t.TempDir(), "telegram-settings.json"), "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	handler := TelegramAlertsHandler(manager)

	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/alerts/telegram", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(strings.ToLower(recorder.Body.String()), "token") {
		t.Fatalf("status response mentions a token: %s", recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://evil.example/api/v1/alerts/telegram", nil)
	request.Host = "evil.example"
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("non-local host status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
