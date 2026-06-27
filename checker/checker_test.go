package checker

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"xray-checker/metrics"
	"xray-checker/models"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

var metricsOnce sync.Once

func ensureMetrics() { metricsOnce.Do(func() { metrics.InitMetrics("") }) }

func mkProxy(server, name, stableID string) *models.ProxyConfig {
	return &models.ProxyConfig{
		Protocol: "vless",
		Server:   server,
		Port:     443,
		Name:     name,
		SubName:  "sub",
		StableID: stableID,
	}
}

// recordUp mimics what checkProxyInternal writes on a successful check.
func recordUp(pc *ProxyChecker, p *models.ProxyConfig, lat time.Duration) {
	addr := fmt.Sprintf("%s:%d", p.Server, p.Port)
	metrics.RecordProxyStatus(p.Protocol, addr, p.Name, p.SubName, 1)
	metrics.RecordProxyLatency(p.Protocol, addr, p.Name, p.SubName, lat)
	pc.currentMetrics.Store(proxyMetricKey(p), true)
	pc.latencyMetrics.Store(proxyMetricKey(p), lat)
}

// Reproduces the #148 scenario: a subscription update must not blank out metrics,
// and after the post-update check, series for removed proxies must be pruned.
func TestUpdateProxiesKeepsMetricsAndReconcilePrunesStale(t *testing.T) {
	ensureMetrics()

	a := mkProxy("1.1.1.1", "A", "ida")
	b := mkProxy("2.2.2.2", "B", "idb")
	pc := NewProxyChecker([]*models.ProxyConfig{a, b}, 10000, "", 5, "", "", 5, 1, "ip")

	// Initial check populated A and B.
	recordUp(pc, a, 100*time.Millisecond)
	recordUp(pc, b, 200*time.Millisecond)
	if got := testutil.CollectAndCount(metrics.GetProxyStatusMetric()); got != 2 {
		t.Fatalf("expected 2 status series after initial check, got %d", got)
	}

	// Subscription update: B removed, C added.
	c := mkProxy("3.3.3.3", "C", "idc")
	pc.UpdateProxies([]*models.ProxyConfig{a, c})

	// UpdateProxies must NOT clear existing series (the #148 fix): A and B remain,
	// so /metrics never goes empty between the update and the next check.
	if got := testutil.CollectAndCount(metrics.GetProxyStatusMetric()); got != 2 {
		t.Fatalf("UpdateProxies must not clear metrics; expected 2 series, got %d", got)
	}
	if v := testutil.ToFloat64(metrics.GetProxyStatusMetric().WithLabelValues("vless", "1.1.1.1:443", "A", "sub")); v != 1 {
		t.Fatalf("surviving proxy A must keep status 1 across the update, got %v", v)
	}

	// Immediate post-update check refreshes A and populates C.
	recordUp(pc, a, 120*time.Millisecond)
	recordUp(pc, c, 300*time.Millisecond)

	pc.ReconcileMetrics()

	// After reconcile: A and C present, B pruned.
	if got := testutil.CollectAndCount(metrics.GetProxyStatusMetric()); got != 2 {
		t.Fatalf("after reconcile expected 2 series (A,C), got %d", got)
	}
	if _, ok := pc.currentMetrics.Load(proxyMetricKey(b)); ok {
		t.Errorf("removed proxy B should be pruned from currentMetrics")
	}
	if _, ok := pc.latencyMetrics.Load(proxyMetricKey(b)); ok {
		t.Errorf("removed proxy B should be pruned from latencyMetrics")
	}
	for _, p := range []*models.ProxyConfig{a, c} {
		if _, ok := pc.currentMetrics.Load(proxyMetricKey(p)); !ok {
			t.Errorf("proxy %s should remain in currentMetrics", p.Name)
		}
	}
}
