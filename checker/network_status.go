package checker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xray-checker/logger"
	"xray-checker/networksource"
)

const defaultNetworkStatusMaxAge = 15 * time.Second

// NetworkStatus is written by the iPhone route monitor sidecar. Managed is
// false when no monitor file was configured, preserving the normal standalone
// checker behavior on Linux and other deployments.
type NetworkStatus struct {
	SourceID  string `json:"sourceId,omitempty"`
	Label     string `json:"label,omitempty"`
	Epoch     string `json:"epoch,omitempty"`
	Managed   bool   `json:"managed"`
	Ready     bool   `json:"ready"`
	State     string `json:"state"`
	Interface string `json:"interface,omitempty"`
	LocalIP   string `json:"localIp,omitempty"`
	PublicIP  string `json:"publicIp,omitempty"`
	Message   string `json:"message,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

func (pc *ProxyChecker) SetNetworkSource(dir, id, epoch string) {
	pc.networkSourceDir, pc.networkSourceID, pc.networkSourceEpoch = strings.TrimSpace(dir), id, epoch
}

func (pc *ProxyChecker) applySourceGate(status NetworkStatus) NetworkStatus {
	if pc.networkSourceDir == "" {
		return status
	}
	status.Managed = true
	if status.Label == "" {
		if status.SourceID == "router" {
			status.Label = "LTE router"
		} else {
			status.Label = "iPhone USB"
		}
	}
	for _, name := range []string{networksource.RequestName, networksource.ProcessingName} {
		data, err := os.ReadFile(filepath.Join(pc.networkSourceDir, name))
		if err == nil {
			var request networksource.Request
			if len(data) > 2048 || json.Unmarshal(data, &request) != nil || request.Action == "activate" {
				status.Ready = false
				status.State = "switching"
				status.Message = "Network source transition in progress"
				return status
			}
		} else if !os.IsNotExist(err) {
			status.Ready = false
			status.State = "unknown"
			status.Message = "Source transition state is unavailable"
			return status
		}
	}
	s, err := networksource.Read(pc.networkSourceDir)
	if err != nil || !s.Fresh(time.Now()) {
		status.Ready = false
		status.State = "unknown"
		status.Message = "Host network source supervisor is unavailable"
		return status
	}
	if s.Job.Active() && s.Job.Action == "activate" {
		status.Ready = false
		status.State = "switching"
		status.Message = s.Job.Message
		return status
	}
	if s.ActiveSourceID != pc.networkSourceID || s.Epoch != pc.networkSourceEpoch || status.SourceID != pc.networkSourceID || status.Epoch != pc.networkSourceEpoch {
		status.Ready = false
		status.State = "unknown"
		status.Message = "Waiting for the selected source session"
		return status
	}
	return status
}

// SetNetworkStatusFile enables connection-aware checks. A stale or unavailable
// status file pauses new proxy requests instead of silently testing over a
// fallback network.
func (pc *ProxyChecker) SetNetworkStatusFile(path string, maxAge time.Duration) {
	pc.networkStatusFile = strings.TrimSpace(path)
	if maxAge <= 0 {
		maxAge = defaultNetworkStatusMaxAge
	}
	pc.networkStatusMaxAge = maxAge
}

func (pc *ProxyChecker) GetNetworkStatus() (result NetworkStatus) {
	defer func() { result = pc.applySourceGate(result) }()
	if pc.networkStatusFile == "" {
		return NetworkStatus{
			Managed: false,
			Ready:   true,
			State:   "unmanaged",
			Message: "External network monitor is disabled",
		}
	}

	data, err := os.ReadFile(pc.networkStatusFile)
	if err != nil {
		return NetworkStatus{
			Managed: true,
			Ready:   false,
			State:   "waiting",
			Message: "Waiting for the measurement network monitor",
		}
	}

	var status NetworkStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return NetworkStatus{
			Managed: true,
			Ready:   false,
			State:   "unknown",
			Message: "Connection monitor returned invalid status",
		}
	}

	status.Managed = true
	maxAge := pc.networkStatusMaxAge
	if maxAge <= 0 {
		maxAge = defaultNetworkStatusMaxAge
	}
	if status.UpdatedAt == 0 || time.Since(time.Unix(status.UpdatedAt, 0)) > maxAge || time.Until(time.Unix(status.UpdatedAt, 0)) > 5*time.Second {
		status.Ready = false
		status.State = "unknown"
		status.Message = "Connection monitor is not responding"
		return status
	}

	status.Ready = status.State == "connected"
	return status
}

func (pc *ProxyChecker) waitForNetwork() {
	for {
		status := pc.GetNetworkStatus()
		if status.Ready {
			if status.PublicIP != "" {
				pc.setCurrentIP(status.PublicIP)
			}

			pc.networkLogMu.Lock()
			if pc.networkWaiting {
				logger.Info("Mobile connection restored on %s (%s)", status.Interface, status.PublicIP)
				pc.networkWaiting = false
			}
			pc.networkLogMu.Unlock()
			return
		}

		pc.networkLogMu.Lock()
		if !pc.networkWaiting {
			logger.Warn("Proxy checks paused: %s", status.Message)
			pc.networkWaiting = true
		}
		pc.networkLogMu.Unlock()
		time.Sleep(time.Second)
	}
}

// retryAfterNetworkOutage allows the host monitor a moment to observe a cable
// removal that happened while an HTTP request was in flight. A confirmed outage
// is retried without storing a false offline result.
func (pc *ProxyChecker) retryAfterNetworkOutage() bool {
	if pc.networkStatusFile == "" {
		return false
	}
	time.Sleep(2 * time.Second)
	return !pc.GetNetworkStatus().Ready
}

func (pc *ProxyChecker) setCurrentIP(ip string) {
	pc.ipMu.Lock()
	pc.currentIP = strings.TrimSpace(ip)
	pc.ipInitialized = pc.currentIP != ""
	pc.ipMu.Unlock()
}

func (pc *ProxyChecker) getCurrentIP() string {
	pc.ipMu.RLock()
	defer pc.ipMu.RUnlock()
	return pc.currentIP
}
