package checker

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xray-checker/logger"
	"xray-checker/models"
)

type NodeState string

const (
	NodeUnknown          NodeState = "unknown"
	NodeHealthy          NodeState = "healthy"
	NodeUnstable         NodeState = "unstable"
	NodeSuspected        NodeState = "suspected"
	NodeNeedsReplacement NodeState = "needs_replacement"
	NodeIPChanged        NodeState = "ip_changed"
	NodeVerifyingNewIP   NodeState = "verifying_new_ip"
	NodeFixed            NodeState = "fixed"
	NodeNewIPFailed      NodeState = "new_ip_failed"
)

type CheckReason string

const (
	CheckReasonInitial   CheckReason = "initial"
	CheckReasonScheduled CheckReason = "scheduled"
	CheckReasonChanged   CheckReason = "configuration_changed"
	CheckReasonManual    CheckReason = "manual"
)

type NodeEvent struct {
	At          int64     `json:"at"`
	Type        string    `json:"type"`
	State       NodeState `json:"state"`
	FromAddress string    `json:"fromAddress,omitempty"`
	ToAddress   string    `json:"toAddress,omitempty"`
	Online      bool      `json:"online,omitempty"`
	Unstable    bool      `json:"unstable,omitempty"`
	Message     string    `json:"message,omitempty"`
}

type NodeMonitorState struct {
	LogicalID            string      `json:"logicalId"`
	State                NodeState   `json:"state"`
	CurrentAddress       string      `json:"currentAddress"`
	PreviousAddress      string      `json:"previousAddress,omitempty"`
	ResolvedIPs          []string    `json:"resolvedIps,omitempty"`
	PreviousResolvedIPs  []string    `json:"previousResolvedIps,omitempty"`
	AddressChangedAt     int64       `json:"addressChangedAt,omitempty"`
	Revision             string      `json:"revision,omitempty"`
	RevisionVersion      int         `json:"revisionVersion,omitempty"`
	ConsecutiveFailures  int         `json:"consecutiveFailures"`
	ConsecutiveSuccesses int         `json:"consecutiveSuccesses"`
	LastCheck            int64       `json:"lastCheck,omitempty"`
	LastSuccess          int64       `json:"lastSuccess,omitempty"`
	LastError            string      `json:"lastError,omitempty"`
	ExitIP               string      `json:"exitIp,omitempty"`
	NextCheck            int64       `json:"nextCheck,omitempty"`
	UpdatedAt            int64       `json:"updatedAt"`
	History              []NodeEvent `json:"history,omitempty"`
}

type monitorSnapshot struct {
	Version int                 `json:"version"`
	Nodes   []*NodeMonitorState `json:"nodes"`
}

const (
	monitorSnapshotVersion = 1
	monitorRevisionVersion = 2
	monitorHistoryLimit    = 40
	maxTargetedBatch       = 50
)

func (pc *ProxyChecker) SetMonitorFile(path string) error {
	pc.monitorFile = strings.TrimSpace(path)
	pc.monitorMu.Lock()
	if pc.monitor == nil {
		pc.monitor = make(map[string]*NodeMonitorState)
	}
	pc.monitorMu.Unlock()

	var loadErr error
	if pc.monitorFile != "" {
		loadErr = pc.loadMonitor()
		pc.monitorSignal = make(chan struct{}, 1)
		go pc.monitorPersistLoop()
	}
	pc.reconcileMonitorWithCurrentProxies()
	return loadErr
}

func (pc *ProxyChecker) loadMonitor() error {
	data, err := os.ReadFile(pc.monitorFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read node monitor: %w", err)
	}
	var snapshot monitorSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode node monitor: %w", err)
	}
	if snapshot.Version != monitorSnapshotVersion {
		return fmt.Errorf("unsupported node monitor version %d", snapshot.Version)
	}
	pc.monitorMu.Lock()
	for _, node := range snapshot.Nodes {
		if node != nil && node.LogicalID != "" {
			pc.monitor[node.LogicalID] = node
		}
	}
	pc.monitorMu.Unlock()
	logger.Info("Restored repair history for %d logical nodes", len(snapshot.Nodes))
	return nil
}

func (pc *ProxyChecker) reconcileMonitorWithCurrentProxies() {
	proxies := pc.GetProxies()
	models.AssignLogicalIDs(proxies)
	now := time.Now()
	pc.monitorMu.Lock()
	for _, proxy := range proxies {
		node := pc.monitor[proxy.LogicalID]
		if node == nil {
			node = &NodeMonitorState{
				LogicalID:       proxy.LogicalID,
				State:           NodeUnknown,
				CurrentAddress:  proxyAddress(proxy),
				Revision:        proxy.GenerateRevisionID(),
				RevisionVersion: monitorRevisionVersion,
				ResolvedIPs:     literalServerIPs(proxy.Server),
				UpdatedAt:       now.Unix(),
			}
			if result, ok := pc.results.Load(proxyMetricKey(proxy)); ok {
				pc.initializeNodeFromResult(node, result.(proxyResult), now)
			}
			pc.monitor[proxy.LogicalID] = node
			continue
		}
		currentRevision := proxy.GenerateRevisionID()
		if node.RevisionVersion < monitorRevisionVersion {
			// ProxyConfig gained topology-only fields. Older revision hashes used
			// the whole struct shape, so that schema addition looked like a config
			// change for every node even though its address was identical. Adopt
			// the new hash without creating a repair event; undo only the exact
			// same-address artifact produced by the transitional build.
			node.Revision = currentRevision
			node.RevisionVersion = monitorRevisionVersion
			node.CurrentAddress = proxyAddress(proxy)
			if node.AddressChangedAt > 0 && node.PreviousAddress == node.CurrentAddress {
				node.PreviousAddress = ""
				node.AddressChangedAt = 0
				if result, ok := pc.results.Load(proxyMetricKey(proxy)); ok {
					node.ConsecutiveFailures = 0
					node.ConsecutiveSuccesses = 0
					pc.initializeNodeFromResult(node, result.(proxyResult), now)
				}
			}
			node.UpdatedAt = now.Unix()
			continue
		}
		if node.Revision != "" && node.Revision != currentRevision {
			markNodeRevisionChanged(node, proxy, now)
		} else {
			node.CurrentAddress = proxyAddress(proxy)
			node.Revision = currentRevision
			node.RevisionVersion = monitorRevisionVersion
			if len(node.ResolvedIPs) == 0 {
				node.ResolvedIPs = literalServerIPs(proxy.Server)
			}
			node.UpdatedAt = now.Unix()
			if node.NextCheck <= now.Unix() && (node.State == NodeHealthy || node.State == NodeFixed) {
				node.NextCheck = nextHealthyCheck(node.LogicalID, now).Unix()
			}
		}
	}
	pc.monitorMu.Unlock()
	pc.scheduleMonitorPersist()
}

func (pc *ProxyChecker) initializeNodeFromResult(node *NodeMonitorState, result proxyResult, now time.Time) {
	node.LastCheck = result.lastCheck.Unix()
	node.LastError = result.lastError
	node.ExitIP = result.exitIP
	if result.status {
		node.LastSuccess = result.lastCheck.Unix()
		node.ConsecutiveSuccesses = 1
		if result.unstable {
			node.State = NodeUnstable
			node.NextCheck = nextUnstableCheck(node.LogicalID, now).Unix()
		} else {
			node.State = NodeHealthy
			node.NextCheck = nextHealthyCheck(node.LogicalID, now).Unix()
		}
	} else {
		node.State = NodeSuspected
		node.ConsecutiveFailures = 1
		node.NextCheck = now.Add(30 * time.Second).Unix()
	}
}

func (pc *ProxyChecker) ApplyProxyUpdate(newProxies []*models.ProxyConfig, plan ProxyUpdatePlan) {
	renamedResults := make(map[string]proxyResult)
	for _, change := range plan.Changes {
		if change.Kind == ProxyRenamed {
			if value, ok := pc.results.Load(proxyMetricKey(change.Old)); ok {
				renamedResults[change.New.LogicalID] = value.(proxyResult)
			}
		}
		if change.Kind == ProxyChanged {
			pc.results.Delete(proxyMetricKey(change.Old))
			pc.results.Delete(proxyMetricKey(change.New))
		}
	}
	pc.UpdateProxies(newProxies)
	for _, change := range plan.Changes {
		if change.Kind == ProxyRenamed {
			if result, ok := renamedResults[change.New.LogicalID]; ok {
				pc.results.Store(proxyMetricKey(change.New), result)
				pc.results.Delete(proxyMetricKey(change.Old))
			}
		}
	}
	now := time.Now()
	pc.monitorMu.Lock()
	if pc.monitor == nil {
		pc.monitor = make(map[string]*NodeMonitorState)
	}
	for _, change := range plan.Changes {
		switch change.Kind {
		case ProxyAdded:
			proxy := change.New
			if previous := pc.monitor[proxy.LogicalID]; previous != nil {
				// A host may legitimately disappear from the subscription and later
				// return. Preserve its repair history, but never trust its old health:
				// schedule a fresh check immediately.
				oldAddress := previous.CurrentAddress
				previous.PreviousAddress = oldAddress
				previous.CurrentAddress = proxyAddress(proxy)
				previous.Revision = proxy.GenerateRevisionID()
				previous.ResolvedIPs = literalServerIPs(proxy.Server)
				previous.State = NodeUnknown
				previous.ConsecutiveFailures = 0
				previous.ConsecutiveSuccesses = 0
				previous.LastError = ""
				previous.ExitIP = ""
				previous.NextCheck = now.Unix()
				previous.UpdatedAt = now.Unix()
				appendNodeEvent(previous, NodeEvent{At: now.Unix(), Type: "returned", State: NodeUnknown,
					FromAddress: oldAddress, ToAddress: proxyAddress(proxy)})
				continue
			}
			pc.monitor[proxy.LogicalID] = &NodeMonitorState{
				LogicalID:       proxy.LogicalID,
				State:           NodeUnknown,
				CurrentAddress:  proxyAddress(proxy),
				Revision:        proxy.GenerateRevisionID(),
				RevisionVersion: monitorRevisionVersion,
				ResolvedIPs:     literalServerIPs(proxy.Server),
				NextCheck:       now.Unix(),
				UpdatedAt:       now.Unix(),
				History: []NodeEvent{{At: now.Unix(), Type: "added", State: NodeUnknown,
					ToAddress: proxyAddress(proxy)}},
			}
		case ProxyChanged:
			node := pc.monitor[change.New.LogicalID]
			if node == nil {
				node = &NodeMonitorState{LogicalID: change.New.LogicalID}
				pc.monitor[change.New.LogicalID] = node
			}
			markNodeRevisionChanged(node, change.New, now)
		case ProxyRenamed:
			if node := pc.monitor[change.New.LogicalID]; node != nil {
				node.UpdatedAt = now.Unix()
				appendNodeEvent(node, NodeEvent{At: now.Unix(), Type: "renamed", State: node.State})
			}
		case ProxyRemoved:
			if node := pc.monitor[change.Old.LogicalID]; node != nil {
				appendNodeEvent(node, NodeEvent{At: now.Unix(), Type: "removed", State: node.State,
					FromAddress: node.CurrentAddress})
				node.UpdatedAt = now.Unix()
			}
		}
	}
	pc.monitorMu.Unlock()
	pc.scheduleMonitorPersist()
}

func markNodeRevisionChanged(node *NodeMonitorState, proxy *models.ProxyConfig, now time.Time) {
	previous := node.CurrentAddress
	if previous == "" {
		previous = proxyAddress(proxy)
	}
	node.PreviousAddress = previous
	node.CurrentAddress = proxyAddress(proxy)
	node.AddressChangedAt = now.Unix()
	node.PreviousResolvedIPs = append([]string(nil), node.ResolvedIPs...)
	node.ResolvedIPs = literalServerIPs(proxy.Server)
	node.Revision = proxy.GenerateRevisionID()
	node.RevisionVersion = monitorRevisionVersion
	node.State = NodeIPChanged
	node.ConsecutiveFailures = 0
	node.ConsecutiveSuccesses = 0
	node.LastError = ""
	node.ExitIP = ""
	node.NextCheck = now.Unix()
	node.UpdatedAt = now.Unix()
	appendNodeEvent(node, NodeEvent{At: now.Unix(), Type: "revision_changed", State: NodeIPChanged,
		FromAddress: previous, ToAddress: node.CurrentAddress})
}

// RefreshResolvedIPs detects DNS endpoint changes even when the subscription
// text itself is unchanged. Literal IP endpoints are handled without a lookup.
// Returned nodes should be verified immediately using the normal changed-node
// comprehensive check.
func (pc *ProxyChecker) RefreshResolvedIPs() []*models.ProxyConfig {
	proxies := pc.GetProxies()
	type resolution struct {
		proxy *models.ProxyConfig
		ips   []string
	}
	results := make(chan resolution, len(proxies))
	sem := make(chan struct{}, 20)
	for _, proxy := range proxies {
		go func(proxy *models.ProxyConfig) {
			if literal := literalServerIPs(proxy.Server); len(literal) > 0 {
				results <- resolution{proxy: proxy, ips: literal}
				return
			}
			sem <- struct{}{}
			ips, err := net.LookupIP(proxy.Server)
			<-sem
			if err != nil {
				results <- resolution{proxy: proxy}
				return
			}
			text := make([]string, 0, len(ips))
			for _, ip := range ips {
				text = append(text, ip.String())
			}
			sortStrings(text)
			results <- resolution{proxy: proxy, ips: text}
		}(proxy)
	}

	changed := make([]*models.ProxyConfig, 0)
	now := time.Now()
	pc.monitorMu.Lock()
	for range proxies {
		resolved := <-results
		if len(resolved.ips) == 0 {
			continue
		}
		node := pc.monitor[resolved.proxy.LogicalID]
		if node == nil {
			continue
		}
		if len(node.ResolvedIPs) == 0 {
			node.ResolvedIPs = resolved.ips
			node.UpdatedAt = now.Unix()
			continue
		}
		if equalStrings(node.ResolvedIPs, resolved.ips) {
			continue
		}
		node.PreviousResolvedIPs = append([]string(nil), node.ResolvedIPs...)
		node.ResolvedIPs = append([]string(nil), resolved.ips...)
		node.AddressChangedAt = now.Unix()
		node.State = NodeIPChanged
		node.ConsecutiveFailures = 0
		node.ConsecutiveSuccesses = 0
		node.NextCheck = now.Unix()
		node.UpdatedAt = now.Unix()
		appendNodeEvent(node, NodeEvent{At: now.Unix(), Type: "dns_changed", State: NodeIPChanged,
			FromAddress: strings.Join(node.PreviousResolvedIPs, ", "), ToAddress: strings.Join(node.ResolvedIPs, ", ")})
		changed = append(changed, resolved.proxy)
	}
	pc.monitorMu.Unlock()
	if len(changed) > 0 {
		logger.Info("Detected resolved IP changes for %d node(s)", len(changed))
		pc.scheduleMonitorPersist()
	}
	return changed
}

func (pc *ProxyChecker) recordMonitorResults(proxies []*models.ProxyConfig, reason CheckReason) {
	now := time.Now()
	pc.monitorMu.Lock()
	if pc.monitor == nil {
		pc.monitor = make(map[string]*NodeMonitorState)
	}
	for _, proxy := range proxies {
		if proxy.LogicalID == "" {
			proxy.LogicalID = proxy.GenerateLogicalID()
		}
		value, ok := pc.results.Load(proxyMetricKey(proxy))
		if !ok {
			continue
		}
		node := pc.monitor[proxy.LogicalID]
		if node == nil {
			node = &NodeMonitorState{LogicalID: proxy.LogicalID, State: NodeUnknown,
				CurrentAddress: proxyAddress(proxy), Revision: proxy.GenerateRevisionID(), RevisionVersion: monitorRevisionVersion}
			pc.monitor[proxy.LogicalID] = node
		}
		pc.applyMonitorResult(node, value.(proxyResult), reason, now)
	}
	pc.monitorMu.Unlock()
	pc.scheduleMonitorPersist()
}

func (pc *ProxyChecker) applyMonitorResult(node *NodeMonitorState, result proxyResult, reason CheckReason, now time.Time) {
	wasReplacement := node.State == NodeIPChanged || node.State == NodeVerifyingNewIP ||
		node.State == NodeNewIPFailed || reason == CheckReasonChanged
	node.LastCheck = result.lastCheck.Unix()
	node.LastError = result.lastError
	node.ExitIP = result.exitIP
	node.UpdatedAt = now.Unix()

	if result.status {
		node.LastSuccess = result.lastCheck.Unix()
		node.ConsecutiveFailures = 0
		if result.unstable {
			node.ConsecutiveSuccesses = 0
			if wasReplacement {
				node.State = NodeVerifyingNewIP
				node.NextCheck = now.Add(time.Minute).Unix()
			} else {
				node.State = NodeUnstable
				node.NextCheck = nextUnstableCheck(node.LogicalID, now).Unix()
			}
		} else if wasReplacement {
			node.ConsecutiveSuccesses++
			if node.ConsecutiveSuccesses >= 2 {
				node.State = NodeFixed
				node.NextCheck = nextHealthyCheck(node.LogicalID, now).Unix()
			} else {
				node.State = NodeVerifyingNewIP
				node.NextCheck = now.Add(45 * time.Second).Unix()
			}
		} else {
			node.ConsecutiveSuccesses++
			if node.State == NodeFixed {
				node.State = NodeHealthy
			} else {
				node.State = NodeHealthy
			}
			node.NextCheck = nextHealthyCheck(node.LogicalID, now).Unix()
		}
	} else {
		node.ConsecutiveSuccesses = 0
		node.ConsecutiveFailures++
		if wasReplacement {
			if node.ConsecutiveFailures >= 2 {
				node.State = NodeNewIPFailed
				node.NextCheck = now.Add(6 * time.Hour).Unix()
			} else {
				node.State = NodeVerifyingNewIP
				node.NextCheck = now.Add(30 * time.Second).Unix()
			}
		} else if node.ConsecutiveFailures >= 3 {
			node.State = NodeNeedsReplacement
			node.NextCheck = now.Add(6 * time.Hour).Unix()
		} else {
			node.State = NodeSuspected
			if node.ConsecutiveFailures == 1 {
				node.NextCheck = now.Add(30 * time.Second).Unix()
			} else {
				node.NextCheck = now.Add(2 * time.Minute).Unix()
			}
		}
	}
	appendNodeEvent(node, NodeEvent{At: now.Unix(), Type: "check", State: node.State,
		Online: result.status, Unstable: result.unstable, Message: result.lastError})
}

func (pc *ProxyChecker) StartMonitorScheduler(tick time.Duration) {
	if tick <= 0 {
		tick = 10 * time.Second
	}
	go func() {
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for range ticker.C {
			pc.CheckDueProxies()
		}
	}()
}

func (pc *ProxyChecker) CheckDueProxies() {
	if !pc.GetNetworkStatus().Ready {
		return
	}
	now := time.Now().Unix()
	proxies := pc.GetProxies()
	due := make([]*models.ProxyConfig, 0)
	pc.monitorMu.RLock()
	for _, proxy := range proxies {
		if node := pc.monitor[proxy.LogicalID]; node != nil && node.NextCheck > 0 && node.NextCheck <= now {
			due = append(due, proxy)
		}
	}
	pc.monitorMu.RUnlock()
	if len(due) == 0 {
		return
	}
	if len(due) > maxTargetedBatch {
		due = due[:maxTargetedBatch]
	}
	if err := pc.checkProxySet(due, CheckReasonScheduled); err != nil {
		logger.Warn("Scheduled node checks skipped: %v", err)
	}
}

func (pc *ProxyChecker) RecheckProxy(stableID string) error {
	proxy, ok := pc.GetProxyByStableID(stableID)
	if !ok {
		return fmt.Errorf("proxy not found")
	}
	return pc.checkProxySet([]*models.ProxyConfig{proxy}, CheckReasonManual)
}

func (pc *ProxyChecker) RecheckNode(nodeID string) error {
	proxies := pc.GetProxies()
	bindings := make([]*models.ProxyConfig, 0)
	for _, proxy := range proxies {
		if proxy.NodeID == nodeID {
			bindings = append(bindings, proxy)
		}
	}
	if len(bindings) == 0 {
		return fmt.Errorf("node not found")
	}
	if len(bindings) > maxTargetedBatch {
		bindings = bindings[:maxTargetedBatch]
	}
	return pc.checkProxySet(bindings, CheckReasonManual)
}

func (pc *ProxyChecker) CheckUpdatedProxies(proxies []*models.ProxyConfig) error {
	if len(proxies) == 0 {
		return nil
	}
	if len(proxies) > maxTargetedBatch {
		logger.Info("Queued %d updated nodes; verifying the first %d now and leaving the rest to bounded scheduler batches",
			len(proxies), maxTargetedBatch)
		proxies = proxies[:maxTargetedBatch]
	}
	return pc.checkProxySet(proxies, CheckReasonChanged)
}

// WithChecksPaused prevents Xray from being restarted while requests are using
// its local SOCKS listeners.
func (pc *ProxyChecker) WithChecksPaused(update func() error) error {
	pc.checkCycleMu.Lock()
	defer pc.checkCycleMu.Unlock()
	return update()
}

func (pc *ProxyChecker) checkProxySet(proxies []*models.ProxyConfig, reason CheckReason) error {
	if status := pc.GetNetworkStatus(); !status.Ready {
		return fmt.Errorf("mobile network unavailable: %s", status.Message)
	}
	pc.checkCycleMu.Lock()
	defer pc.checkCycleMu.Unlock()
	if _, err := pc.GetCurrentIP(); err != nil {
		return err
	}
	logger.Info("Checking %d node(s), reason=%s", len(proxies), reason)
	timeout := pc.retryTimeout
	if timeout <= 0 {
		timeout = pc.ipCheckTimeout
	}
	runBoundedChecks(proxies, pc.retryConcurrency, func(proxy *models.ProxyConfig) {
		verifyExitIP := reason == CheckReasonChanged
		pc.monitorMu.RLock()
		if node := pc.monitor[proxy.LogicalID]; node != nil {
			verifyExitIP = verifyExitIP || node.State == NodeIPChanged || node.State == NodeVerifyingNewIP || node.State == NodeNewIPFailed
		}
		pc.monitorMu.RUnlock()
		pc.checkProxyInternalWithMode(proxy, timeout, false, verifyExitIP)
	})
	pc.recordMonitorResults(proxies, reason)
	return nil
}

func (pc *ProxyChecker) GetNodeMonitorByStableID(stableID string) (NodeMonitorState, bool) {
	proxy, ok := pc.GetProxyByStableID(stableID)
	if !ok {
		return NodeMonitorState{}, false
	}
	pc.monitorMu.RLock()
	defer pc.monitorMu.RUnlock()
	node := pc.monitor[proxy.LogicalID]
	if node == nil {
		return NodeMonitorState{LogicalID: proxy.LogicalID, State: NodeUnknown,
			CurrentAddress: proxyAddress(proxy)}, true
	}
	copyNode := *node
	copyNode.History = append([]NodeEvent(nil), node.History...)
	return copyNode, true
}

func proxyAddress(proxy *models.ProxyConfig) string {
	return fmt.Sprintf("%s:%d", proxy.Server, proxy.Port)
}

func literalServerIPs(server string) []string {
	if ip := net.ParseIP(strings.TrimSpace(server)); ip != nil {
		return []string{ip.String()}
	}
	return nil
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func nextHealthyCheck(logicalID string, now time.Time) time.Time {
	h := fnv.New32a()
	_, _ = h.Write([]byte(logicalID))
	return now.Add(45*time.Minute + time.Duration(h.Sum32()%901)*time.Second)
}

func nextUnstableCheck(logicalID string, now time.Time) time.Time {
	h := fnv.New32a()
	_, _ = h.Write([]byte(logicalID))
	return now.Add(4*time.Minute + 30*time.Second + time.Duration(h.Sum32()%61)*time.Second)
}

func appendNodeEvent(node *NodeMonitorState, event NodeEvent) {
	node.History = append(node.History, event)
	if len(node.History) > monitorHistoryLimit {
		node.History = append([]NodeEvent(nil), node.History[len(node.History)-monitorHistoryLimit:]...)
	}
}

func (pc *ProxyChecker) scheduleMonitorPersist() {
	if pc.monitorSignal == nil {
		return
	}
	select {
	case pc.monitorSignal <- struct{}{}:
	default:
	}
}

func (pc *ProxyChecker) monitorPersistLoop() {
	for range pc.monitorSignal {
		time.Sleep(300 * time.Millisecond)
		for {
			select {
			case <-pc.monitorSignal:
			default:
				if err := pc.persistMonitor(); err != nil {
					logger.Warn("Could not save node repair history: %v", err)
				}
				goto next
			}
		}
	next:
	}
}

func (pc *ProxyChecker) persistMonitor() error {
	if pc.monitorFile == "" {
		return nil
	}
	snapshot := monitorSnapshot{Version: monitorSnapshotVersion}
	pc.monitorMu.RLock()
	for _, node := range pc.monitor {
		copyNode := *node
		copyNode.History = append([]NodeEvent(nil), node.History...)
		snapshot.Nodes = append(snapshot.Nodes, &copyNode)
	}
	pc.monitorMu.RUnlock()
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pc.monitorFile), 0o755); err != nil {
		return err
	}
	temporary := pc.monitorFile + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, pc.monitorFile)
}
