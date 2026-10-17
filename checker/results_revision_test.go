package checker

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"xray-checker/models"
)

func TestSavedSuccessCannotCertifyRotatedCredentials(t *testing.T) {
	p := waveProxy(1, "panel")
	path := filepath.Join(t.TempDir(), "results.json")
	first := NewProxyChecker([]*models.ProxyConfig{p}, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	first.resultsFile = path
	first.results.Store(proxyMetricKey(p), proxyResult{status: true, lastCheck: time.Now()})
	first.markResultsComplete(true)
	clone := *p
	clone.UUID = "rotated-credential"
	second := NewProxyChecker([]*models.ProxyConfig{&clone}, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	if err := second.SetResultsFile(path); err != nil {
		t.Fatal(err)
	}
	if second.HasCompleteResults() || len(second.MetricsSnapshot()) != 0 {
		t.Fatal("new credentials inherited a previous success")
	}
}

func TestCorruptResultsStillEnableFuturePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	pc := NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	if err := pc.SetResultsFile(path); err == nil {
		t.Fatal("corrupt snapshot was accepted")
	}
	if pc.persistSignal == nil {
		t.Fatal("a read error disabled all subsequent persistence")
	}
}
