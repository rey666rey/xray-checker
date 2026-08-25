package checker

import (
	"path/filepath"
	"testing"
	"time"

	"xray-checker/models"
)

func monitorTestProxy(name, server string) *models.ProxyConfig {
	return &models.ProxyConfig{
		Protocol: "vless", Name: name, SubName: "panel-a", GroupName: "group-a",
		Server: server, Port: 443, UUID: "00000000-0000-4000-8000-000000000001",
	}
}

func TestPlanProxyUpdatePreservesLogicalIDAcrossAddressChange(t *testing.T) {
	oldProxy := monitorTestProxy("node-1", "192.0.2.1")
	models.AssignLogicalIDs([]*models.ProxyConfig{oldProxy})
	newProxy := monitorTestProxy("node-1", "192.0.2.2")

	plan := PlanProxyUpdate([]*models.ProxyConfig{oldProxy}, []*models.ProxyConfig{newProxy})
	if plan.Count(ProxyChanged) != 1 {
		t.Fatalf("changed count = %d, want 1", plan.Count(ProxyChanged))
	}
	if newProxy.LogicalID != oldProxy.LogicalID {
		t.Fatalf("logical ID changed: %q != %q", newProxy.LogicalID, oldProxy.LogicalID)
	}
	if newProxy.GenerateStableID() == oldProxy.GenerateStableID() {
		t.Fatal("connection StableID must change with the address")
	}
}

func TestPlanProxyUpdateTreatsRenameAsSameRevision(t *testing.T) {
	oldProxy := monitorTestProxy("old name", "192.0.2.1")
	models.AssignLogicalIDs([]*models.ProxyConfig{oldProxy})
	newProxy := monitorTestProxy("new name", "192.0.2.1")

	plan := PlanProxyUpdate([]*models.ProxyConfig{oldProxy}, []*models.ProxyConfig{newProxy})
	if plan.Count(ProxyRenamed) != 1 || len(plan.ProxiesToCheck()) != 0 {
		t.Fatalf("plan = %#v, want one rename and no check", plan)
	}
	if newProxy.LogicalID != oldProxy.LogicalID {
		t.Fatal("rename did not preserve logical ID")
	}
}

func TestPlanProxyUpdateDisambiguatesNewDuplicateDisplayNode(t *testing.T) {
	oldProxy := monitorTestProxy("same name", "192.0.2.1")
	models.AssignLogicalIDs([]*models.ProxyConfig{oldProxy})
	unchanged := monitorTestProxy("same name", "192.0.2.1")
	added := monitorTestProxy("same name", "192.0.2.2")

	plan := PlanProxyUpdate([]*models.ProxyConfig{oldProxy}, []*models.ProxyConfig{unchanged, added})
	if plan.Count(ProxyAdded) != 1 {
		t.Fatalf("added count = %d, want 1", plan.Count(ProxyAdded))
	}
	if unchanged.LogicalID != oldProxy.LogicalID {
		t.Fatal("unchanged duplicate lost its logical ID")
	}
	if added.LogicalID == "" || added.LogicalID == unchanged.LogicalID {
		t.Fatalf("duplicate logical IDs: %q and %q", unchanged.LogicalID, added.LogicalID)
	}
}

func TestApplyProxyUpdateRestoresArchivedHostHistory(t *testing.T) {
	returned := monitorTestProxy("returning", "192.0.2.10")
	models.AssignLogicalIDs([]*models.ProxyConfig{returned})
	returned.StableID = returned.GenerateStableID()
	pc := NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	now := time.Now().Add(-time.Hour).Unix()
	pc.monitor = map[string]*NodeMonitorState{
		returned.LogicalID: {
			LogicalID:      returned.LogicalID,
			State:          NodeNeedsReplacement,
			CurrentAddress: "192.0.2.9:443",
			History:        []NodeEvent{{At: now, Type: "removed", State: NodeNeedsReplacement}},
		},
	}

	pc.ApplyProxyUpdate([]*models.ProxyConfig{returned}, ProxyUpdatePlan{Changes: []ProxyChange{{Kind: ProxyAdded, New: returned}}})
	node, ok := pc.GetNodeMonitorByStableID(returned.StableID)
	if !ok || node.State != NodeUnknown || node.NextCheck == 0 || len(node.History) != 2 || node.History[1].Type != "returned" {
		t.Fatalf("returned host state = %#v, found=%t", node, ok)
	}
}

func TestMonitorRevisionSchemaMigrationDoesNotMarkSameAddressChanged(t *testing.T) {
	proxy := monitorTestProxy("stable", "192.0.2.10")
	proxy.StableID = proxy.GenerateStableID()
	models.AssignLogicalIDs([]*models.ProxyConfig{proxy})
	pc := NewProxyChecker([]*models.ProxyConfig{proxy}, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	pc.results.Store(proxyMetricKey(proxy), proxyResult{status: true, lastCheck: time.Now()})
	pc.monitor = map[string]*NodeMonitorState{
		proxy.LogicalID: {
			LogicalID: proxy.LogicalID, State: NodeVerifyingNewIP,
			CurrentAddress: proxyAddress(proxy), PreviousAddress: proxyAddress(proxy),
			AddressChangedAt: time.Now().Unix(), Revision: "legacy-schema-hash",
		},
	}

	pc.reconcileMonitorWithCurrentProxies()
	node := pc.monitor[proxy.LogicalID]
	if node.State != NodeHealthy || node.PreviousAddress != "" || node.AddressChangedAt != 0 || node.RevisionVersion != monitorRevisionVersion {
		t.Fatalf("migration left a false endpoint change: %#v", node)
	}
}

func TestMonitorConfirmsFailureAndNewAddressRepair(t *testing.T) {
	oldProxy := monitorTestProxy("node-1", "192.0.2.1")
	oldProxy.Index = 0
	oldProxy.StableID = oldProxy.GenerateStableID()
	models.AssignLogicalIDs([]*models.ProxyConfig{oldProxy})
	pc := NewProxyChecker([]*models.ProxyConfig{oldProxy}, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	if err := pc.SetMonitorFile(filepath.Join(t.TempDir(), "node-history.json")); err != nil {
		t.Fatal(err)
	}

	failed := proxyResult{status: false, lastCheck: time.Now(), lastError: "timeout"}
	for round := 1; round <= 3; round++ {
		pc.results.Store(proxyMetricKey(oldProxy), failed)
		reason := CheckReasonScheduled
		if round == 1 {
			reason = CheckReasonInitial
		}
		pc.recordMonitorResults([]*models.ProxyConfig{oldProxy}, reason)
	}
	node, _ := pc.GetNodeMonitorByStableID(oldProxy.StableID)
	if node.State != NodeNeedsReplacement || node.ConsecutiveFailures != 3 {
		t.Fatalf("state after failures = %s/%d", node.State, node.ConsecutiveFailures)
	}

	newProxy := monitorTestProxy("node-1", "192.0.2.2")
	newProxy.Index = 0
	newProxy.StableID = newProxy.GenerateStableID()
	plan := PlanProxyUpdate([]*models.ProxyConfig{oldProxy}, []*models.ProxyConfig{newProxy})
	pc.ApplyProxyUpdate([]*models.ProxyConfig{newProxy}, plan)
	node, _ = pc.GetNodeMonitorByStableID(newProxy.StableID)
	if node.State != NodeIPChanged || node.PreviousAddress != "192.0.2.1:443" {
		t.Fatalf("change state = %#v", node)
	}

	success := proxyResult{status: true, lastCheck: time.Now(), exitIP: "198.51.100.8"}
	pc.results.Store(proxyMetricKey(newProxy), success)
	pc.recordMonitorResults([]*models.ProxyConfig{newProxy}, CheckReasonChanged)
	node, _ = pc.GetNodeMonitorByStableID(newProxy.StableID)
	if node.State != NodeVerifyingNewIP {
		t.Fatalf("first success state = %s", node.State)
	}
	pc.results.Store(proxyMetricKey(newProxy), success)
	pc.recordMonitorResults([]*models.ProxyConfig{newProxy}, CheckReasonScheduled)
	node, _ = pc.GetNodeMonitorByStableID(newProxy.StableID)
	if node.State != NodeFixed || node.ExitIP != "198.51.100.8" {
		t.Fatalf("second success state = %#v", node)
	}
}
