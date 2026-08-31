package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"xray-checker/checker"
)

func TestAPIAccessChecksListsEmptyHistory(t *testing.T) {
	proxyChecker := checker.NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/access-checks", nil)
	APIAccessChecksHandler(proxyChecker).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"data":[]`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIAccessChecksRejectsUnsafeTarget(t *testing.T) {
	proxyChecker := checker.NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/access-checks", strings.NewReader(`{"ip":"127.0.0.1","port":22,"method":"ssh"}`))
	request.Header.Set("Content-Type", "application/json")
	APIAccessChecksHandler(proxyChecker).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "public unicast") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIAccessChecksRejectsUnknownFields(t *testing.T) {
	proxyChecker := checker.NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/access-checks", strings.NewReader(`{"ip":"203.0.113.10","port":22,"method":"ssh","scan":true}`))
	APIAccessChecksHandler(proxyChecker).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unknown field") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIAccessChecksReturnsNotFound(t *testing.T) {
	proxyChecker := checker.NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/access-checks/missing", nil)
	APIAccessChecksHandler(proxyChecker).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
