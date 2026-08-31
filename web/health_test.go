package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthHandlerReportsCheckFailure(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	HealthHandler(func() error { return errors.New("disk full") }).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "disk full") {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestPersistentStorageHealthCheckWritesToVolume(t *testing.T) {
	if err := PersistentStorageHealthCheck(t.TempDir(), 1)(); err != nil {
		t.Fatal(err)
	}
}
