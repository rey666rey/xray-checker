package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOriginMutationMiddleware(t *testing.T) {
	called := false
	handler := SameOriginMutationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/nodes/id/recheck", nil)
	request.Header.Set("Origin", "https://malicious.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || called {
		t.Fatalf("cross-origin status=%d called=%v", recorder.Code, called)
	}

	request = httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/nodes/id/recheck", nil)
	request.Header.Set("Origin", "http://localhost")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent || !called {
		t.Fatalf("same-origin status=%d called=%v", recorder.Code, called)
	}
}

func TestSameOriginMutationMiddlewareAllowsReadOnlyCrossOriginRequest(t *testing.T) {
	handler := SameOriginMutationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/status", nil)
	request.Header.Set("Origin", "https://example.com")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("read-only status=%d", recorder.Code)
	}
}

func TestBasicAuthMiddleware(t *testing.T) {
	handler := BasicAuthMiddleware("operator", "secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, credentials := range [][2]string{{"operator", "wrong"}, {"wrong", "secret"}} {
		request := httptest.NewRequest(http.MethodGet, "http://localhost/metrics", nil)
		request.SetBasicAuth(credentials[0], credentials[1])
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("credentials %q/%q status=%d", credentials[0], credentials[1], recorder.Code)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost/metrics", nil)
	request.SetBasicAuth("operator", "secret")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("valid credentials status=%d", recorder.Code)
	}
}
