package xray

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadGeoFileInstallsCompleteResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("geo-data"))
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "geo.dat")
	if err := NewGeoFileManager("").downloadFile(server.URL, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "geo-data" {
		t.Fatalf("downloaded data=%q", data)
	}
}

func TestDownloadGeoFileRejectsEmptyResponseWithoutTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "geo.dat")
	if err := NewGeoFileManager("").downloadFile(server.URL, target); err == nil {
		t.Fatal("expected empty response to fail")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target must not exist after failed download: %v", err)
	}
}
