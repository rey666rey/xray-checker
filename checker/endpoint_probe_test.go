package checker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"xray-checker/models"
)

func TestEndpointProbeTargetsDeduplicateAndSkipSuspected(t *testing.T) {
	first := &models.ProxyConfig{Protocol: "vless", Server: "192.0.2.1", Port: 443, LogicalID: "one", StableID: "one"}
	second := &models.ProxyConfig{Protocol: "trojan", Server: "192.0.2.1", Port: 443, LogicalID: "two", StableID: "two"}
	suspected := &models.ProxyConfig{Protocol: "vless", Server: "192.0.2.2", Port: 443, LogicalID: "three", StableID: "three"}
	udp := &models.ProxyConfig{Protocol: "hysteria", Server: "192.0.2.3", Port: 443, LogicalID: "four", StableID: "four"}
	pc := NewProxyChecker([]*models.ProxyConfig{first, second, suspected, udp}, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	pc.monitor = map[string]*NodeMonitorState{
		"one":   {LogicalID: "one", State: NodeHealthy},
		"two":   {LogicalID: "two", State: NodeFixed},
		"three": {LogicalID: "three", State: NodeSuspected},
		"four":  {LogicalID: "four", State: NodeHealthy},
	}

	targets := pc.endpointProbeTargets()
	if len(targets) != 1 {
		t.Fatalf("targets=%d, want 1: %#v", len(targets), targets)
	}
	if targets[0].address != "192.0.2.1:443" || len(targets[0].proxies) != 2 {
		t.Fatalf("unexpected deduplicated target: %#v", targets[0])
	}
}

func TestConfirmFailedEndpointProbesRequiresTwoFailures(t *testing.T) {
	failed := &models.ProxyConfig{Protocol: "vless", Server: "192.0.2.1", Port: 443, StableID: "failed"}
	transient := &models.ProxyConfig{Protocol: "vless", Server: "192.0.2.2", Port: 443, StableID: "transient"}
	pc := NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	config := endpointProbeConfig{timeout: time.Second, confirmDelay: 0, concurrency: 2}

	var mu sync.Mutex
	calls := make(map[string]int)
	dial := func(_ context.Context, _, address string, _ time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		calls[address]++
		if address == "192.0.2.1:443" || calls[address] == 1 {
			return errors.New("unreachable")
		}
		return nil
	}
	targets := []endpointProbeTarget{
		{key: "failed", address: "192.0.2.1:443", proxies: []*models.ProxyConfig{failed}},
		{key: "transient", address: "192.0.2.2:443", proxies: []*models.ProxyConfig{transient}},
	}

	result := pc.confirmFailedEndpointProbes(targets, "col0", config, dial)
	if len(result) != 1 || result[0].StableID != "failed" {
		t.Fatalf("confirmed=%#v, want only failed endpoint", result)
	}
	if calls["192.0.2.1:443"] != 2 || calls["192.0.2.2:443"] != 2 {
		t.Fatalf("calls=%v, want two attempts for each initially failed endpoint", calls)
	}
}

func TestEndpointProbeInitialDelayWithinInterval(t *testing.T) {
	interval := 90 * time.Second
	first := endpointProbeInitialDelay("192.0.2.1:443", interval)
	second := endpointProbeInitialDelay("192.0.2.1:443", interval)
	if first != second || first < 0 || first >= interval {
		t.Fatalf("delay=%s/%s, want deterministic value within %s", first, second, interval)
	}
}
