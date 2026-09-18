package main

import (
	"testing"

	"xray-checker/checker"
	"xray-checker/models"
	"xray-checker/xray"
)

func TestFindReplacementPrefersCarriedLogicalIdentity(t *testing.T) {
	previous := testReplacementProxy("old", "192.0.2.10", "logical-1", "host-1")
	target := testReplacementProxy("new", "192.0.2.20", previous.LogicalID, previous.HostID)
	xray.PrepareProxyConfigs([]*models.ProxyConfig{previous})
	xray.PrepareProxyConfigs([]*models.ProxyConfig{target})

	verifier := &replacementVerifier{proxyChecker: testReplacementChecker([]*models.ProxyConfig{target})}
	found, detected, candidates := verifier.findReplacement(
		previous, previous.LogicalID, previous.HostID, previous.GenerateRevisionID(), checker.ProxyUpdatePlan{},
	)

	if found != target || !detected || candidates != 1 {
		t.Fatalf("found=%p target=%p detected=%v candidates=%d", found, target, detected, candidates)
	}
}

func TestFindReplacementUsesOnlyActiveHostAlternative(t *testing.T) {
	previous := testReplacementProxy("old", "192.0.2.10", "logical-old", "host-1")
	target := testReplacementProxy("new", "192.0.2.20", "logical-new", previous.HostID)
	proxies := []*models.ProxyConfig{previous, target}
	pool := checker.NewEndpointPool(proxies)
	proxyChecker := testReplacementChecker(proxies)
	proxyChecker.SetEndpointPool(pool)
	verifier := &replacementVerifier{proxyChecker: proxyChecker}

	found, detected, candidates := verifier.findReplacement(
		previous, previous.LogicalID, previous.HostID, previous.GenerateRevisionID(), checker.ProxyUpdatePlan{},
	)

	if found != target || !detected || candidates != 1 {
		t.Fatalf("found=%p target=%p detected=%v candidates=%d", found, target, detected, candidates)
	}
}

func TestFindReplacementRefusesAmbiguousHostAlternatives(t *testing.T) {
	previous := testReplacementProxy("old", "192.0.2.10", "logical-old", "host-1")
	first := testReplacementProxy("first", "192.0.2.20", "logical-first", previous.HostID)
	second := testReplacementProxy("second", "192.0.2.30", "logical-second", previous.HostID)
	proxies := []*models.ProxyConfig{previous, first, second}
	pool := checker.NewEndpointPool(proxies)
	proxyChecker := testReplacementChecker(proxies)
	proxyChecker.SetEndpointPool(pool)
	verifier := &replacementVerifier{proxyChecker: proxyChecker}

	found, detected, candidates := verifier.findReplacement(
		previous, previous.LogicalID, previous.HostID, previous.GenerateRevisionID(), checker.ProxyUpdatePlan{},
	)

	if found != nil || detected || candidates != 2 {
		t.Fatalf("found=%p detected=%v candidates=%d", found, detected, candidates)
	}
}

func TestFindReplacementDoesNotRecheckMissingOldBinding(t *testing.T) {
	previous := testReplacementProxy("old", "192.0.2.10", "logical-old", "host-1")
	pool := checker.NewEndpointPool([]*models.ProxyConfig{previous})
	pool.Observe([]*models.ProxyConfig{})
	proxyChecker := testReplacementChecker([]*models.ProxyConfig{previous})
	proxyChecker.SetEndpointPool(pool)
	verifier := &replacementVerifier{proxyChecker: proxyChecker}

	found, detected, candidates := verifier.findReplacement(
		previous, previous.LogicalID, previous.HostID, previous.GenerateRevisionID(), checker.ProxyUpdatePlan{},
	)

	if found != nil || detected || candidates != 0 {
		t.Fatalf("found=%p detected=%v candidates=%d", found, detected, candidates)
	}
}

func TestFindReplacementPrefersFreshDiffOverAccumulatedPool(t *testing.T) {
	previous := testReplacementProxy("old", "192.0.2.10", "logical-old", "host-1")
	existing := testReplacementProxy("existing", "192.0.2.20", "logical-existing", previous.HostID)
	fresh := testReplacementProxy("fresh", "192.0.2.30", "logical-fresh", previous.HostID)
	proxies := []*models.ProxyConfig{previous, existing, fresh}
	pool := checker.NewEndpointPool(proxies)
	proxyChecker := testReplacementChecker(proxies)
	proxyChecker.SetEndpointPool(pool)
	verifier := &replacementVerifier{proxyChecker: proxyChecker}
	plan := checker.ProxyUpdatePlan{Changes: []checker.ProxyChange{{Kind: checker.ProxyAdded, New: fresh}}}

	found, detected, candidates := verifier.findReplacement(
		previous, previous.LogicalID, previous.HostID, previous.GenerateRevisionID(), plan,
	)

	if found != fresh || !detected || candidates != 1 {
		t.Fatalf("found=%p fresh=%p detected=%v candidates=%d", found, fresh, detected, candidates)
	}
}

func testReplacementProxy(stableID, server, logicalID, hostID string) *models.ProxyConfig {
	return &models.ProxyConfig{
		Protocol:  "vless",
		Server:    server,
		Port:      443,
		Name:      "replacement-test",
		UUID:      "00000000-0000-0000-0000-000000000001",
		StableID:  stableID,
		LogicalID: logicalID,
		HostID:    hostID,
	}
}

func testReplacementChecker(proxies []*models.ProxyConfig) *checker.ProxyChecker {
	return checker.NewProxyChecker(proxies, 10000, "", 1, "", "", 1, 0, "url", 1)
}
