package checker

import "time"

type MonitoringStatus struct {
	Mode               string `json:"mode"`
	PendingChecks      int    `json:"pendingChecks"`
	OldestDelaySeconds int64  `json:"oldestDelaySeconds"`
	QueuedDiagnoses    int    `json:"queuedDiagnoses"`
	DiagnosisRunning   bool   `json:"diagnosisRunning"`
	ActiveProbes       int64  `json:"activeProbes"`
	RecentFailures     int    `json:"recentFailures"`
	RecentRecoveries   int    `json:"recentRecoveries"`
	WindowSeconds      int64  `json:"windowSeconds"`
}

// These counters describe the current set only. Archived hosts and repeat
// failures in the same episode cannot inflate the apparent size of a wave.
func (pc *ProxyChecker) GetMonitoringStatus() MonitoringStatus {
	return pc.monitoringStatus(time.Now())
}

func (pc *ProxyChecker) monitoringStatus(now time.Time) MonitoringStatus {
	status := MonitoringStatus{Mode: "normal", WindowSeconds: 15 * 60}
	proxies := pc.GetProxies()
	cutoff := now.Unix() - status.WindowSeconds
	pc.monitorMu.RLock()
	for _, proxy := range proxies {
		node := pc.monitor[proxy.LogicalID]
		if node == nil {
			continue
		}
		if node.NextCheck > 0 && node.NextCheck <= now.Unix() {
			status.PendingChecks++
			delay := now.Unix() - node.NextCheck
			if delay > status.OldestDelaySeconds {
				status.OldestDelaySeconds = delay
			}
		}
		if node.ConsecutiveFailures >= automaticDiagnosisFailureThreshold && node.FailureStartedAt >= cutoff && node.FailureStartedAt > 0 && node.LastSuccess > 0 && node.LastSuccess <= node.FailureStartedAt {
			status.RecentFailures++
		}
		if node.RecoveredAt >= cutoff && node.RecoveredAt > 0 {
			status.RecentRecoveries++
		}
	}
	pc.monitorMu.RUnlock()
	pc.diagnosisMu.RLock()
	status.QueuedDiagnoses = len(pc.diagnosisQueued)
	status.DiagnosisRunning = pc.diagnosisRunning
	pc.diagnosisMu.RUnlock()
	status.ActiveProbes = pc.probeBudget.active.Load()
	switch {
	case !pc.GetNetworkStatus().Ready:
		status.Mode = "network_waiting"
	case status.RecentFailures >= 10 && status.RecentFailures*10 >= len(proxies):
		status.Mode = "failure_wave"
	case status.PendingChecks >= maxTargetedBatch && status.OldestDelaySeconds >= 60:
		status.Mode = "catching_up"
	}
	return status
}
