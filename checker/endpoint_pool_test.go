package checker

import (
	"testing"

	"xray-checker/models"
)

func poolProxy(name, server string) *models.ProxyConfig {
	return &models.ProxyConfig{Protocol: "vless", Security: "reality", Type: "tcp",
		Name: name, SubName: "220V", Server: server, Port: 443,
		UUID: "00000000-0000-4000-8000-000000000001"}
}

func TestEndpointPoolAccumulatesAlternatingNodesForOneHost(t *testing.T) {
	old := poolProxy("🇪🇸 Испания", "37.143.130.191")
	pool := NewEndpointPool([]*models.ProxyConfig{old})

	configs, stats := pool.Observe(
		[]*models.ProxyConfig{poolProxy(old.Name, "149.33.45.111")},
		[]*models.ProxyConfig{poolProxy(old.Name, "37.143.130.170")},
	)
	if stats.Added != 2 || len(configs) != 3 {
		t.Fatalf("stats=%+v configs=%d, want two additions and retained old binding", stats, len(configs))
	}
	hostIDs := map[string]bool{}
	nodeIDs := map[string]bool{}
	for _, proxy := range configs {
		hostIDs[proxy.HostID] = true
		nodeIDs[proxy.NodeID] = true
	}
	if len(hostIDs) != 1 || len(nodeIDs) != 3 {
		t.Fatalf("host IDs=%v node IDs=%v", hostIDs, nodeIDs)
	}
}

func TestEndpointPoolGroupsDifferentHostsOnOnePhysicalNode(t *testing.T) {
	bindings := []*models.ProxyConfig{
		poolProxy("🇩🇪 Германия (s1)", "192.0.2.10"),
		poolProxy("🇩🇪 Германия (s1 v2)", "192.0.2.10"),
		poolProxy("🇩🇪 Германия (s1 v3)", "192.0.2.10"),
	}
	pool := NewEndpointPool(bindings)
	configs, _ := pool.Observe(bindings)
	nodeIDs := map[string]bool{}
	hostIDs := map[string]bool{}
	for _, proxy := range configs {
		nodeIDs[proxy.NodeID] = true
		hostIDs[proxy.HostID] = true
	}
	if len(nodeIDs) != 1 || len(hostIDs) != 3 {
		t.Fatalf("node IDs=%v host IDs=%v, want one node and three hosts", nodeIDs, hostIDs)
	}
}

func TestEndpointPoolKeepsRunningSNIForExistingEndpoint(t *testing.T) {
	current := poolProxy("host", "192.0.2.10")
	current.SNI = "working.example"
	pool := NewEndpointPool([]*models.ProxyConfig{current})
	first := poolProxy("host", "192.0.2.10")
	first.SNI = "random-a.example"
	second := poolProxy("host", "192.0.2.10")
	second.SNI = "random-b.example"

	configs, stats := pool.Observe([]*models.ProxyConfig{first}, []*models.ProxyConfig{second})
	if len(configs) != 1 || configs[0].SNI != "working.example" || stats.Updated != 0 {
		t.Fatalf("configs=%#v stats=%+v, running SNI must remain stable", configs, stats)
	}
}

func TestEndpointPoolDetachesOnlyAfterLongAbsence(t *testing.T) {
	proxy := poolProxy("host", "192.0.2.10")
	pool := NewEndpointPool([]*models.ProxyConfig{proxy})
	for round := 1; round < endpointDetachAfterPolls; round++ {
		configs, _ := pool.Observe(nil)
		if len(configs) != 1 {
			t.Fatalf("binding detached at round %d", round)
		}
	}
	configs, stats := pool.Observe(nil)
	if len(configs) != 0 || stats.Detached != 1 {
		t.Fatalf("configs=%d stats=%+v, want detached binding", len(configs), stats)
	}
}

func TestEndpointPoolReplacesPortAfterTwoAuthoritativeRounds(t *testing.T) {
	old := poolProxy("host", "192.0.2.10")
	old.Port = 8443
	pool := NewEndpointPool([]*models.ProxyConfig{old})
	newPort := poolProxy("host", "192.0.2.10")
	newPort.Port = 443

	configs, stats := pool.Observe(
		[]*models.ProxyConfig{newPort},
		[]*models.ProxyConfig{newPort},
	)
	if len(configs) != 2 || stats.Detached != 0 || stats.Missing != 1 {
		t.Fatalf("first replacement round: configs=%d stats=%+v", len(configs), stats)
	}

	configs, stats = pool.Observe(
		[]*models.ProxyConfig{newPort},
		[]*models.ProxyConfig{newPort},
	)
	if len(configs) != 1 || configs[0].Port != 443 || stats.Detached != 1 {
		t.Fatalf("confirmed replacement: configs=%#v stats=%+v", configs, stats)
	}
}

func TestEndpointPoolKeepsPortsThatAppearTogether(t *testing.T) {
	first := poolProxy("host", "192.0.2.10")
	first.Port = 443
	second := poolProxy("host", "192.0.2.10")
	second.Port = 8443
	pool := NewEndpointPool([]*models.ProxyConfig{first, second})

	for round := 0; round < 3; round++ {
		configs, stats := pool.Observe(
			[]*models.ProxyConfig{first, second},
			[]*models.ProxyConfig{first, second},
		)
		if len(configs) != 2 || stats.Detached != 0 || stats.Missing != 0 {
			t.Fatalf("round %d: configs=%d stats=%+v", round, len(configs), stats)
		}
	}
}
