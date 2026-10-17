package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"xray-checker/models"
)

func waveProxy(index int, subscription string) *models.ProxyConfig {
	p := monitorTestProxy(fmt.Sprintf("node-%d", index), fmt.Sprintf("198.18.%d.%d", index/256, index%256))
	p.SubName = subscription
	models.AssignLogicalIDs([]*models.ProxyConfig{p})
	models.AssignTopologyIDs([]*models.ProxyConfig{p})
	p.StableID = p.GenerateStableID()
	return p
}

func TestFailureWaveDoesNotRediagnoseKnownOutages(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	proxies := make([]*models.ProxyConfig, 300)
	for i := range proxies {
		proxies[i] = waveProxy(i, "panel")
	}
	pc := NewProxyChecker(proxies, 10000, "", 1, "", "", 1, 1, "urltest", 30)
	pc.monitor = make(map[string]*NodeMonitorState)
	for _, p := range proxies {
		pc.monitor[p.LogicalID] = &NodeMonitorState{
			LogicalID: p.LogicalID, ConsecutiveFailures: 5, FailureEpisode: 1,
			LastCheck: now.Unix(), LastSuccess: now.Add(-time.Hour).Unix(),
		}
		pc.results.Store(proxyMetricKey(p), proxyResult{lastCheck: now})
		pc.diagnosisHistory[p.NodeID] = []NodeDiagnosis{{
			NodeID: p.NodeID, Revision: nodeDiagnosisRevision([]*models.ProxyConfig{p}),
			State: DiagnosisCompleted, Verdict: DiagnosisTunnelFailed,
			Control: ControlDiagnosis{Online: true}, CompletedAt: now.Add(-time.Minute).Unix(),
			Bindings:        []BindingDiagnosis{{StableID: p.StableID, Attempts: 3}},
			FailureEpisodes: map[string]uint64{p.StableID: 1},
		}}
	}
	pc.enqueueAutomaticDiagnoses(proxies)
	if len(pc.diagnosisQueue) != 0 {
		t.Fatalf("known 300-node wave queued %d repeat investigations", len(pc.diagnosisQueue))
	}
	// Recovery followed by another failure is a new episode, even within the
	// same second and with the same endpoint and error message.
	p := proxies[0]
	monitor := pc.monitor[p.LogicalID]
	pc.applyMonitorResult(monitor, proxyResult{status: true, lastCheck: now}, CheckReasonScheduled, now)
	for i := 1; i <= 2; i++ {
		result := proxyResult{lastCheck: now.Add(time.Duration(i))}
		pc.results.Store(proxyMetricKey(p), result)
		pc.applyMonitorResult(monitor, result, CheckReasonScheduled, now)
	}
	if !pc.automaticDiagnosisNeeded(p.NodeID) {
		t.Fatal("a new failure episode reused the previous episode's diagnosis")
	}
	// A connection revision and the expiry of negative evidence both require
	// a new investigation, despite matching failure symptoms.
	p = proxies[1]
	p.UUID = "new-credential"
	if !pc.automaticDiagnosisNeeded(p.NodeID) {
		t.Fatal("changed credentials reused the previous revision's diagnosis")
	}
	p = proxies[2]
	pc.diagnosisHistory[p.NodeID][0].CompletedAt = now.Add(-diagnosisFailureFreshDuration - time.Second).Unix()
	if !pc.automaticDiagnosisNeeded(p.NodeID) {
		t.Fatal("expired diagnosis was not renewed")
	}
	p = proxies[3]
	pc.diagnosisHistory[p.NodeID][0].FailureKinds = map[string]string{p.StableID: "timeout"}
	pc.monitor[p.LogicalID].LastError = "connection reset by peer"
	if !pc.automaticDiagnosisNeeded(p.NodeID) {
		t.Fatal("changed failure symptoms reused the old diagnosis")
	}
}

func TestWaveSchedulerReservesObservationAndReplacements(t *testing.T) {
	now := time.Now()
	pc := NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 30)
	pc.monitor = make(map[string]*NodeMonitorState)
	for i := 0; i < 450; i++ {
		sub, state := "failing-panel-a", NodeNeedsReplacement
		if i >= 300 && i < 350 {
			sub, state = "healthy-panel", NodeHealthy
		} else if i >= 350 && i < 400 {
			sub, state = "new-panel", NodeConfigChanged
		} else if i >= 400 {
			sub = "failing-panel-b"
		}
		p := waveProxy(i, sub)
		pc.proxies = append(pc.proxies, p)
		pc.monitor[p.LogicalID] = &NodeMonitorState{State: state, NextCheck: now.Add(-time.Minute).Unix()}
	}
	due := pc.dueProxies(now, 50)
	counts := map[string]int{}
	for _, p := range due {
		counts[p.SubName]++
	}
	if len(due) != 50 || counts["healthy-panel"] != 15 || counts["new-panel"] != 10 || counts["failing-panel-b"] < 12 {
		t.Fatalf("wave monopolized a reserved lane or subscription: %v", counts)
	}
	for _, p := range due {
		pc.monitor[p.LogicalID].NextCheck = now.Add(time.Hour).Unix()
	}
	next := pc.dueProxies(now, 50)
	for _, p := range next {
		if pc.monitor[p.LogicalID].NextCheck > now.Unix() {
			t.Fatal("already-serviced target displaced a due target")
		}
	}
}

func TestWaveSchedulerUsesDeadlineInsteadOfDisplayOrder(t *testing.T) {
	now := time.Now()
	pc := NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 30)
	pc.monitor = make(map[string]*NodeMonitorState)
	for i := 0; i < 300; i++ {
		p := waveProxy(i, "panel")
		pc.proxies = append(pc.proxies, p)
		due := now.Add(-time.Minute).Unix()
		if i >= 250 {
			due = now.Add(-time.Hour).Unix()
		}
		pc.monitor[p.LogicalID] = &NodeMonitorState{State: NodeNeedsReplacement, NextCheck: due}
	}
	for _, p := range pc.dueProxies(now, 50) {
		if pc.monitor[p.LogicalID].NextCheck != now.Add(-time.Hour).Unix() {
			t.Fatal("a recent failure displaced the oldest overdue target")
		}
	}
}

func TestProbeBudgetLimitsWaveAndPreservesNormalCapacity(t *testing.T) {
	budget := newProbeBudget(10)
	first, err := budget.acquire(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if release, err := budget.acquire(ctx, true); err == nil {
		release()
		t.Fatal("diagnostics consumed more than their allowance")
	}
	normal, err := budget.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	normal()
	first()
	if budget.active.Load() != 0 {
		t.Fatal("canceled waiter leaked a slot")
	}

	budget = newProbeBudget(16)
	proxies := make([]*models.ProxyConfig, 300)
	ready := make(chan struct{}, 16)
	gate := make(chan struct{})
	done := make(chan struct{})
	var completed atomic.Int64
	go func() {
		runBoundedChecks(proxies, 64, func(_ *models.ProxyConfig) {
			release, err := budget.acquire(context.Background(), false)
			if err != nil {
				return
			}
			select {
			case ready <- struct{}{}:
			default:
			}
			<-gate
			completed.Add(1)
			release()
		})
		close(done)
	}()
	for i := 0; i < 16; i++ {
		select {
		case <-ready:
		case <-time.After(time.Second):
			close(gate)
			t.Fatal("workers failed to acquire the available budget")
		}
	}
	if budget.active.Load() != 16 {
		t.Errorf("active probes = %d, want 16", budget.active.Load())
	}
	close(gate)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wave did not drain")
	}
	if completed.Load() != 300 || budget.active.Load() != 0 {
		t.Fatal("wave lost jobs or leaked capacity")
	}
}

func TestReloadCancelsAutomaticDiagnosisWithoutGlobalPriority(t *testing.T) {
	p := waveProxy(1, "panel")
	pc := NewProxyChecker([]*models.ProxyConfig{p}, 10000, "", 1, "", "", 1, 1, "urltest", 10)
	if _, _, err := pc.beginNodeDiagnosis(p.NodeID, "automatic"); err != nil {
		t.Fatal(err)
	}
	defer pc.diagnosisCancel()
	ready, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		pc.runtimeMu.RLock()
		close(ready)
		<-pc.diagnosisContext.Done()
		pc.runtimeMu.RUnlock()
		close(stopped)
	}()
	<-ready
	if err := pc.WithChecksPaused(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("reload remained blocked by automatic diagnosis")
	}
}

func TestScheduledCheckContinuesDuringDiagnosis(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	p := waveProxy(1, "panel")
	pc := NewProxyChecker([]*models.ProxyConfig{p}, port, "", 1, "", "", 1, 1, "urltest", 10)
	pc.SetURLTestOptions("http://example.invalid/", "Success", 2, 1, 1)
	pc.SetMonitorFile("")
	pc.diagnosisRunning = true
	if err := pc.checkProxySet([]*models.ProxyConfig{p}, CheckReasonScheduled); err != nil {
		t.Fatal(err)
	}
	if _, _, _, found := pc.GetProxyResultByStableID(p.StableID); !found {
		t.Fatal("diagnosis suppressed ordinary observation")
	}
}

func TestSameAddressRevisionDoesNotCreateIPRepair(t *testing.T) {
	p := waveProxy(1, "panel")
	now := time.Now()
	node := &NodeMonitorState{State: NodeHealthy, CurrentAddress: proxyAddress(p), Revision: p.GenerateRevisionID()}
	p.SNI = "new.example"
	markNodeRevisionChanged(node, p, now)
	if node.State != NodeConfigChanged || node.PreviousAddress != "" || node.AddressChangedAt != 0 {
		t.Fatalf("false IP replacement: %#v", node)
	}
	pc := NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 10)
	pc.applyMonitorResult(node, proxyResult{status: true, lastCheck: now}, CheckReasonChanged, now)
	if node.State != NodeHealthy {
		t.Fatalf("verified config entered IP repair state: %s", node.State)
	}
	// A parameter change during a real replacement keeps its confirmation rule.
	node.State, node.PreviousAddress = NodeVerifyingNewIP, "198.18.0.99:443"
	markNodeRevisionChanged(node, p, now)
	if node.State != NodeIPChanged {
		t.Fatal("parameter edit waived real replacement confirmation")
	}
}

func TestRepeatedMonitorSnapshotDoesNotConfirmFailureTwice(t *testing.T) {
	p := waveProxy(1, "panel")
	pc := NewProxyChecker([]*models.ProxyConfig{p}, 10000, "", 1, "", "", 1, 1, "urltest", 10)
	pc.SetMonitorFile("")
	pc.results.Store(proxyMetricKey(p), proxyResult{lastCheck: time.Now()})
	pc.recordMonitorResults([]*models.ProxyConfig{p}, CheckReasonScheduled)
	pc.recordMonitorResults([]*models.ProxyConfig{p}, CheckReasonTCPProbe)
	monitor, _ := pc.GetNodeMonitorByStableID(p.StableID)
	if monitor.ConsecutiveFailures != 1 || len(pc.diagnosisQueue) != 0 {
		t.Fatal("one observation was counted as two independent failures")
	}
}

func TestNetworkOutageKeepsResultsAndReleasesRuntime(t *testing.T) {
	p := waveProxy(1, "panel")
	pc := NewProxyChecker([]*models.ProxyConfig{p}, 10000, "", 1, "", "", 1, 1, "urltest", 10)
	pc.SetMonitorFile("")
	before := proxyResult{status: true, lastCheck: time.Now()}
	pc.results.Store(proxyMetricKey(p), before)
	pc.recordMonitorResults([]*models.ProxyConfig{p}, CheckReasonScheduled)
	statusFile := filepath.Join(t.TempDir(), "network.json")
	status, _ := json.Marshal(NetworkStatus{State: "waiting", UpdatedAt: time.Now().Unix()})
	if err := os.WriteFile(statusFile, status, 0600); err != nil {
		t.Fatal(err)
	}
	pc.SetNetworkStatusFile(statusFile, time.Minute)
	pc.checkProxyInternal(p)
	pc.recordMonitorResults([]*models.ProxyConfig{p}, CheckReasonScheduled)
	monitor, _ := pc.GetNodeMonitorByStableID(p.StableID)
	if monitor.ConsecutiveFailures != 0 || !pc.resultsForKey(proxyMetricKey(p)).status {
		t.Fatal("route outage became a proxy failure")
	}
	if err := pc.WithChecksPaused(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestTCPWaveQueuesValidationWithoutInventingOfflineResults(t *testing.T) {
	now := time.Now()
	pc := NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 30)
	pc.monitor = make(map[string]*NodeMonitorState)
	for i := 0; i < 300; i++ {
		p := waveProxy(i, "panel")
		pc.proxies = append(pc.proxies, p)
		pc.monitor[p.LogicalID] = &NodeMonitorState{State: NodeHealthy, NextCheck: now.Add(time.Hour).Unix()}
		pc.results.Store(proxyMetricKey(p), proxyResult{status: true, lastCheck: now})
	}
	pc.queueEndpointValidation(pc.proxies, now)
	if len(pc.dueProxies(now, 50)) != 50 {
		t.Fatal("TCP wave bypassed the bounded queue")
	}
	for _, p := range pc.proxies {
		if !pc.resultsForKey(proxyMetricKey(p)).status || pc.monitor[p.LogicalID].ConsecutiveFailures != 0 {
			t.Fatal("TCP evidence was mistaken for a failed Xray tunnel")
		}
	}
}

func TestNewFailedSiblingInvalidatesPartialNodeDiagnosis(t *testing.T) {
	now := time.Now()
	a, b := waveProxy(1, "panel"), waveProxy(2, "panel")
	b.NodeID, b.Server = a.NodeID, a.Server
	pc := NewProxyChecker([]*models.ProxyConfig{a, b}, 10000, "", 1, "", "", 1, 1, "urltest", 10)
	pc.monitor = map[string]*NodeMonitorState{
		a.LogicalID: {ConsecutiveFailures: 3, FailureEpisode: 1},
		b.LogicalID: {ConsecutiveFailures: 2, FailureEpisode: 1},
	}
	run := NodeDiagnosis{
		NodeID: a.NodeID, Revision: nodeDiagnosisRevision([]*models.ProxyConfig{a, b}), State: DiagnosisCompleted,
		Verdict: DiagnosisDegraded, Control: ControlDiagnosis{Online: true}, CompletedAt: now.Add(-time.Minute).Unix(),
		Bindings: []BindingDiagnosis{{StableID: a.StableID, Attempts: 3}, {StableID: b.StableID, Attempts: 3, Successes: 3}},
	}
	pc.diagnosisHistory[a.NodeID] = []NodeDiagnosis{run}
	pc.results.Store(proxyMetricKey(a), proxyResult{lastCheck: now})
	pc.results.Store(proxyMetricKey(b), proxyResult{status: true, lastCheck: now})
	if pc.automaticDiagnosisNeeded(a.NodeID) {
		t.Fatal("known failed sibling invalidated a partial diagnosis")
	}
	pc.results.Store(proxyMetricKey(b), proxyResult{lastCheck: now})
	if !pc.automaticDiagnosisNeeded(a.NodeID) {
		t.Fatal("new failure of a previously working sibling reused the old diagnosis")
	}
}

func TestEarlierDiagnosisEvidenceDoesNotOverwriteLaterObservation(t *testing.T) {
	p := waveProxy(1, "panel")
	now := time.Now()
	pc := NewProxyChecker([]*models.ProxyConfig{p}, 10000, "", 1, "", "", 1, 1, "urltest", 10)
	pc.results.Store(proxyMetricKey(p), proxyResult{lastCheck: now.Add(-time.Second)})
	pc.applyDiagnosisEvidence(NodeDiagnosis{
		NodeID: p.NodeID, Revision: nodeDiagnosisRevision([]*models.ProxyConfig{p}), State: DiagnosisCompleted, CompletedAt: now.Unix(),
		Bindings: []BindingDiagnosis{{StableID: p.StableID, Attempts: 3, Successes: 3, MeasuredAtNS: now.Add(-time.Minute).UnixNano()}},
	}, now)
	if pc.resultsForKey(proxyMetricKey(p)).status {
		t.Fatal("older diagnosis overrode a later failed observation")
	}
}

func TestMonitoringStatusExcludesArchivedHostsAndRepeatFailures(t *testing.T) {
	p := waveProxy(1, "panel")
	now := time.Now()
	pc := NewProxyChecker([]*models.ProxyConfig{p}, 10000, "", 1, "", "", 1, 1, "urltest", 10)
	pc.monitor = map[string]*NodeMonitorState{
		p.LogicalID: {State: NodeNeedsReplacement, NextCheck: now.Add(-2 * time.Minute).Unix(), ConsecutiveFailures: 20, FailureStartedAt: now.Add(-time.Minute).Unix(), LastSuccess: now.Add(-time.Hour).Unix()},
		"archived":  {State: NodeNeedsReplacement, NextCheck: now.Add(-time.Hour).Unix(), ConsecutiveFailures: 20, FailureStartedAt: now.Add(-time.Minute).Unix(), LastSuccess: now.Add(-time.Hour).Unix()},
	}
	status := pc.monitoringStatus(now)
	if status.PendingChecks != 1 || status.RecentFailures != 1 || status.OldestDelaySeconds != 120 {
		t.Fatalf("inflated monitoring status: %#v", status)
	}
}

func TestMissingResultSnapshotRendersHealthyHistoryAsWaiting(t *testing.T) {
	p := waveProxy(1, "panel")
	pc := NewProxyChecker([]*models.ProxyConfig{p}, 10000, "", 1, "", "", 1, 1, "urltest", 10)
	pc.monitor = map[string]*NodeMonitorState{p.LogicalID: {
		State: NodeHealthy, CurrentAddress: proxyAddress(p), Revision: p.GenerateRevisionID(), RevisionVersion: monitorRevisionVersion,
		NextCheck: time.Now().Add(time.Hour).Unix(),
	}}
	pc.reconcileMonitorWithCurrentProxies()
	monitor, _ := pc.GetNodeMonitorByStableID(p.StableID)
	if monitor.State != NodeUnknown || monitor.NextCheck > time.Now().Unix() {
		t.Fatal("unchecked connection retained a misleading healthy-history state")
	}
}
