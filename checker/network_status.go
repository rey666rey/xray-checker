package checker

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"xray-checker/logger"
)

const defaultNetworkStatusMaxAge = 15 * time.Second

// NetworkStatus is written by the iPhone route monitor sidecar. Managed is
// false when no monitor file was configured, preserving the normal standalone
// checker behavior on Linux and other deployments.
type NetworkStatus struct {
	Managed   bool   `json:"managed"`
	Ready     bool   `json:"ready"`
	State     string `json:"state"`
	Interface string `json:"interface,omitempty"`
	LocalIP   string `json:"localIp,omitempty"`
	PublicIP  string `json:"publicIp,omitempty"`
	Message   string `json:"message,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
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

func (pc *ProxyChecker) GetNetworkStatus() NetworkStatus {
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
			Message: "Waiting for the iPhone connection monitor",
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
	if status.UpdatedAt == 0 || time.Since(time.Unix(status.UpdatedAt, 0)) > maxAge {
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
