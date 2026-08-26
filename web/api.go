package web

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"xray-checker/checker"
	"xray-checker/config"
	"xray-checker/logger"
	"xray-checker/models"
	"xray-checker/xray"
)

//go:embed openapi.yaml
var openAPISpec []byte

type ProxyInfo struct {
	Index                int                    `json:"index"`
	StableID             string                 `json:"stableId"`
	Name                 string                 `json:"name"`
	SubName              string                 `json:"subName"`
	GroupName            string                 `json:"groupName"`
	Server               string                 `json:"server"`
	Port                 int                    `json:"port"`
	Protocol             string                 `json:"protocol"`
	ProxyPort            int                    `json:"proxyPort"`
	Online               bool                   `json:"online"`
	Unstable             bool                   `json:"unstable"`
	LatencyMs            int64                  `json:"latencyMs"`
	LastCheck            int64                  `json:"lastCheck"`
	LogicalID            string                 `json:"logicalId"`
	HostID               string                 `json:"hostId"`
	NodeID               string                 `json:"nodeId"`
	MonitorState         checker.NodeState      `json:"monitorState"`
	PreviousAddress      string                 `json:"previousAddress,omitempty"`
	ResolvedIPs          []string               `json:"resolvedIps,omitempty"`
	PreviousResolvedIPs  []string               `json:"previousResolvedIps,omitempty"`
	AddressChangedAt     int64                  `json:"addressChangedAt,omitempty"`
	Failures             int                    `json:"consecutiveFailures"`
	Successes            int                    `json:"consecutiveSuccesses"`
	LastSuccess          int64                  `json:"lastSuccess,omitempty"`
	LastError            string                 `json:"lastError,omitempty"`
	ExitIP               string                 `json:"exitIp,omitempty"`
	NextCheck            int64                  `json:"nextCheck,omitempty"`
	History              []checker.NodeEvent    `json:"history,omitempty"`
	EndpointFirstSeen    int64                  `json:"endpointFirstSeen,omitempty"`
	EndpointLastSeen     int64                  `json:"endpointLastSeen,omitempty"`
	EndpointMissingPolls int                    `json:"endpointMissingPolls,omitempty"`
	MetricsLabels        map[string]string      `json:"metricsLabels,omitempty"`
	GeneratedConfig      map[string]interface{} `json:"generatedConfig,omitempty"`
}

type NodeGroupInfo struct {
	NodeID           string                 `json:"nodeId"`
	Server           string                 `json:"server,omitempty"`
	State            string                 `json:"state"`
	TotalBindings    int                    `json:"totalBindings"`
	OnlineBindings   int                    `json:"onlineBindings"`
	UnstableBindings int                    `json:"unstableBindings"`
	RepairBindings   int                    `json:"repairBindings"`
	HostCount        int                    `json:"hostCount"`
	Missing          bool                   `json:"missing"`
	Bindings         []ProxyInfo            `json:"bindings"`
	Diagnosis        *checker.NodeDiagnosis `json:"diagnosis,omitempty"`
}

type PublicProxyInfo struct {
	StableID         string            `json:"stableId"`
	Name             string            `json:"name"`
	GroupName        string            `json:"groupName"`
	Online           bool              `json:"online"`
	Unstable         bool              `json:"unstable"`
	LatencyMs        int64             `json:"latencyMs"`
	LastCheck        int64             `json:"lastCheck"`
	MonitorState     checker.NodeState `json:"monitorState"`
	Failures         int               `json:"consecutiveFailures"`
	AddressChangedAt int64             `json:"addressChangedAt,omitempty"`
}

type StatusResponse struct {
	Total            int   `json:"total"`
	Online           int   `json:"online"`
	Offline          int   `json:"offline"`
	Unstable         int   `json:"unstable"`
	AvgLatencyMs     int64 `json:"avgLatencyMs"`
	NeedsReplacement int   `json:"needsReplacement"`
	Verifying        int   `json:"verifying"`
}

type ConfigResponse struct {
	CheckInterval              int      `json:"checkInterval"`
	InitialCheckOnly           bool     `json:"initialCheckOnly"`
	CheckMethod                string   `json:"checkMethod"`
	Timeout                    int      `json:"timeout"`
	StartPort                  int      `json:"startPort"`
	SubscriptionUpdate         bool     `json:"subscriptionUpdate"`
	SubscriptionUpdateInterval int      `json:"subscriptionUpdateInterval"`
	SubscriptionPoolSamples    int      `json:"subscriptionPoolSamples"`
	SimulateLatency            bool     `json:"simulateLatency"`
	SubscriptionNames          []string `json:"subscriptionNames"`
}

type SystemInfoResponse struct {
	Version   string `json:"version"`
	Uptime    string `json:"uptime"`
	UptimeSec int64  `json:"uptimeSec"`
	Instance  string `json:"instance"`
}

type SystemIPResponse struct {
	IP string `json:"ip"`
}

type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Data:    data,
	})
}

func writeError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(APIResponse{
		Success: false,
		Error:   message,
	})
}

func toProxyInfo(proxy *models.ProxyConfig, online, unstable bool, latency time.Duration, lastCheck int64,
	monitor checker.NodeMonitorState, observation checker.EndpointObservation, startPort int, includeDetails bool) ProxyInfo {
	info := ProxyInfo{
		Index:            proxy.Index,
		StableID:         proxy.StableID,
		Name:             proxy.Name,
		SubName:          proxy.SubName,
		GroupName:        proxy.GroupName,
		Server:           proxy.Server,
		Port:             proxy.Port,
		Protocol:         proxy.Protocol,
		ProxyPort:        startPort + proxy.Index,
		Online:           online,
		Unstable:         unstable,
		LatencyMs:        latency.Milliseconds(),
		LastCheck:        lastCheck,
		LogicalID:        proxy.LogicalID,
		HostID:           proxy.HostID,
		NodeID:           proxy.NodeID,
		MonitorState:     monitor.State,
		AddressChangedAt: monitor.AddressChangedAt,
		Failures:         monitor.ConsecutiveFailures,
		Successes:        monitor.ConsecutiveSuccesses,
		LastSuccess:      monitor.LastSuccess,
		NextCheck:        monitor.NextCheck,
		MetricsLabels:    proxy.MetricsLabels,
	}
	if observation.LastSeenAt > 0 {
		info.EndpointFirstSeen = observation.FirstSeenAt
		info.EndpointLastSeen = observation.LastSeenAt
		info.EndpointMissingPolls = observation.MissingPolls
	}
	if includeDetails {
		info.PreviousAddress = monitor.PreviousAddress
		info.ResolvedIPs = monitor.ResolvedIPs
		info.PreviousResolvedIPs = monitor.PreviousResolvedIPs
		info.LastError = monitor.LastError
		info.ExitIP = monitor.ExitIP
		info.History = monitor.History
		outbound := xray.NewConfigGenerator().GenerateProxyOutbound(proxy)
		info.GeneratedConfig = sanitizeGeneratedConfig(outbound)
	}
	return info
}

// shouldShowServerDetails reports whether sensitive proxy details (server
// address/port on the dashboard, generated config in the API) may be exposed.
// Details are off unless WEB_SHOW_DETAILS is set, and additionally suppressed in
// public mode unless the operator declares the dashboard is protected by an
// external auth proxy via WEB_TRUSTED_EXTERNAL_AUTH.
func shouldShowServerDetails() bool {
	if !config.CLIConfig.Web.ShowServerDetails {
		return false
	}
	if config.CLIConfig.Web.Public && !config.CLIConfig.Web.TrustedExternalAuth {
		return false
	}
	return true
}

// sanitizeGeneratedConfig returns a copy of the generated outbound config with
// secret values masked, safe to expose for inspection.
func sanitizeGeneratedConfig(value interface{}) map[string]interface{} {
	sanitized, ok := sanitizeGeneratedValue(value).(map[string]interface{})
	if !ok {
		return nil
	}
	return sanitized
}

// sanitizeGeneratedValue recursively walks a generated config value and masks
// the middle of any secret field. Only low-entropy/credential keys are masked;
// public material such as reality publicKey/shortId is left intact so the config
// stays useful for debugging.
func sanitizeGeneratedValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(typed))
		for key, nested := range typed {
			switch strings.ToLower(key) {
			case "id", "password", "auth", "seed":
				if text, ok := nested.(string); ok {
					result[key] = maskMiddle(text)
					continue
				}
			}
			result[key] = sanitizeGeneratedValue(nested)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(typed))
		for i, nested := range typed {
			result[i] = sanitizeGeneratedValue(nested)
		}
		return result
	case []map[string]interface{}:
		result := make([]interface{}, len(typed))
		for i, nested := range typed {
			result[i] = sanitizeGeneratedValue(nested)
		}
		return result
	case []string:
		result := make([]interface{}, len(typed))
		for i, nested := range typed {
			result[i] = nested
		}
		return result
	default:
		return value
	}
}

// maskMiddle hides the middle of a secret, keeping a short prefix/suffix so the
// value stays recognizable. Short values are fully masked to avoid leaking
// low-entropy secrets.
func maskMiddle(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "****"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

// APIPublicProxiesHandler returns public info for all proxies (no auth required)
// @Summary List all proxies (public)
// @Description Returns a list of all proxies with status (no sensitive data, no auth)
// @Tags public
// @Produce json
// @Success 200 {array} PublicProxyInfo
// @Router /api/v1/public/proxies [get]
func APIPublicProxiesHandler(proxyChecker *checker.ProxyChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proxies := proxyChecker.GetProxies()
		result := make([]PublicProxyInfo, 0, len(proxies))

		for _, proxy := range proxies {
			status, unstable, latency, lastCheck, _ := proxyChecker.GetProxyResultDetailsByStableID(proxy.StableID)
			monitor, _ := proxyChecker.GetNodeMonitorByStableID(proxy.StableID)
			result = append(result, PublicProxyInfo{
				StableID:         proxy.StableID,
				Name:             proxy.Name,
				GroupName:        proxy.GroupName,
				Online:           status,
				Unstable:         unstable,
				LatencyMs:        latency.Milliseconds(),
				LastCheck:        lastCheck,
				MonitorState:     monitor.State,
				Failures:         monitor.ConsecutiveFailures,
				AddressChangedAt: monitor.AddressChangedAt,
			})
		}

		writeJSON(w, result)
	}
}

// APIProxiesHandler returns info for all proxies
// @Summary List all proxies
// @Description Returns a list of all proxies with status information
// @Tags proxies
// @Produce json
// @Success 200 {array} ProxyInfo
// @Router /api/v1/proxies [get]
func APIProxiesHandler(proxyChecker *checker.ProxyChecker, startPort int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proxies := proxyChecker.GetProxies()
		result := make([]ProxyInfo, 0, len(proxies))
		includeDetails := shouldShowServerDetails()

		for _, proxy := range proxies {
			status, unstable, latency, lastCheck, _ := proxyChecker.GetProxyResultDetailsByStableID(proxy.StableID)
			monitor, _ := proxyChecker.GetNodeMonitorByStableID(proxy.StableID)
			observation, _ := proxyChecker.GetEndpointObservation(proxy)
			result = append(result, toProxyInfo(proxy, status, unstable, latency, lastCheck, monitor, observation, startPort, includeDetails))
		}

		writeJSON(w, result)
	}
}

// APIProxyHandler returns info for a single proxy
// @Summary Get proxy by ID
// @Description Returns information for a specific proxy
// @Tags proxies
// @Produce json
// @Param stableID path string true "Proxy Stable ID"
// @Success 200 {object} ProxyInfo
// @Failure 404 {object} map[string]string
// @Router /api/v1/proxies/{stableID} [get]
func APIProxyHandler(proxyChecker *checker.ProxyChecker, startPort int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		prefix := "/api/v1/proxies/"
		if !strings.HasPrefix(path, prefix) {
			writeError(w, "Invalid path", http.StatusBadRequest)
			return
		}

		remainder := strings.Trim(strings.TrimPrefix(path, prefix), "/")
		parts := strings.Split(remainder, "/")
		stableID := parts[0]
		if stableID == "" {
			writeError(w, "Proxy ID is required", http.StatusBadRequest)
			return
		}

		proxy, exists := proxyChecker.GetProxyByStableID(stableID)
		if !exists {
			writeError(w, "Proxy not found", http.StatusNotFound)
			return
		}

		if len(parts) == 2 && parts[1] == "recheck" {
			if r.Method != http.MethodPost {
				writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := proxyChecker.RecheckProxy(stableID); err != nil {
				logger.Warn("Manual proxy recheck failed: %v", err)
				writeError(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			proxy, exists = proxyChecker.GetProxyByStableID(stableID)
			if !exists {
				writeError(w, "Proxy disappeared during recheck", http.StatusNotFound)
				return
			}
			status, unstable, latency, lastCheck, _ := proxyChecker.GetProxyResultDetailsByStableID(proxy.StableID)
			monitor, _ := proxyChecker.GetNodeMonitorByStableID(proxy.StableID)
			observation, _ := proxyChecker.GetEndpointObservation(proxy)
			writeJSON(w, toProxyInfo(proxy, status, unstable, latency, lastCheck, monitor, observation, startPort, shouldShowServerDetails()))
			return
		}
		if len(parts) != 1 || r.Method != http.MethodGet {
			writeError(w, "Invalid proxy action", http.StatusBadRequest)
			return
		}
		status, unstable, latency, lastCheck, _ := proxyChecker.GetProxyResultDetailsByStableID(proxy.StableID)
		monitor, _ := proxyChecker.GetNodeMonitorByStableID(proxy.StableID)
		observation, _ := proxyChecker.GetEndpointObservation(proxy)
		writeJSON(w, toProxyInfo(proxy, status, unstable, latency, lastCheck, monitor, observation, startPort, shouldShowServerDetails()))
	}
}

// APINodesHandler returns a node-centric view of the many-to-many topology and
// queues a recheck for all host/inbound bindings attached to one endpoint.
func APINodesHandler(proxyChecker *checker.ProxyChecker, startPort int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes" {
			remainder := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/"), "/")
			parts := strings.Split(remainder, "/")
			if len(parts) != 2 || parts[0] == "" {
				writeError(w, "Invalid node action", http.StatusBadRequest)
				return
			}
			nodeID := parts[0]
			found := false
			for _, proxy := range proxyChecker.GetProxies() {
				if proxy.NodeID == nodeID {
					found = true
					break
				}
			}
			if !found {
				writeError(w, "Node not found", http.StatusNotFound)
				return
			}
			switch parts[1] {
			case "recheck":
				if r.Method != http.MethodPost {
					writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
					return
				}
				if err := proxyChecker.RecheckNode(nodeID); err != nil {
					logger.Warn("Manual node recheck failed: %v", err)
					writeError(w, err.Error(), http.StatusServiceUnavailable)
					return
				}
				writeJSON(w, map[string]bool{"completed": true})
			case "diagnose":
				if r.Method != http.MethodPost {
					writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
					return
				}
				run, err := proxyChecker.StartNodeDiagnosis(nodeID)
				if err != nil {
					status := http.StatusBadRequest
					if errors.Is(err, checker.ErrDiagnosisBusy) {
						status = http.StatusConflict
					}
					writeError(w, err.Error(), status)
					return
				}
				writeJSON(w, run)
			case "diagnosis":
				if r.Method != http.MethodGet {
					writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
					return
				}
				writeJSON(w, proxyChecker.GetNodeDiagnosisHistory(nodeID))
			default:
				writeError(w, "Invalid node action", http.StatusBadRequest)
			}
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		includeDetails := shouldShowServerDetails()
		groups := make(map[string]*NodeGroupInfo)
		hosts := make(map[string]map[string]bool)
		checked := make(map[string]int)
		for _, proxy := range proxyChecker.GetProxies() {
			status, unstable, latency, lastCheck, _ := proxyChecker.GetProxyResultDetailsByStableID(proxy.StableID)
			monitor, _ := proxyChecker.GetNodeMonitorByStableID(proxy.StableID)
			observation, _ := proxyChecker.GetEndpointObservation(proxy)
			binding := toProxyInfo(proxy, status, unstable, latency, lastCheck, monitor, observation, startPort, includeDetails)
			group := groups[proxy.NodeID]
			if group == nil {
				group = &NodeGroupInfo{NodeID: proxy.NodeID}
				if includeDetails {
					group.Server = proxy.Server
				}
				groups[proxy.NodeID] = group
				hosts[proxy.NodeID] = make(map[string]bool)
			}
			group.Bindings = append(group.Bindings, binding)
			group.TotalBindings++
			hosts[proxy.NodeID][proxy.HostID] = true
			if lastCheck > 0 {
				checked[proxy.NodeID]++
			}
			if status {
				group.OnlineBindings++
			}
			if unstable {
				group.UnstableBindings++
			}
			if observation.MissingPolls > 0 {
				group.Missing = true
			}
			switch monitor.State {
			case checker.NodeNeedsReplacement, checker.NodeNewIPFailed:
				group.RepairBindings++
			}
		}

		result := make([]NodeGroupInfo, 0, len(groups))
		for nodeID, group := range groups {
			group.HostCount = len(hosts[nodeID])
			if diagnosis, ok := proxyChecker.GetNodeDiagnosis(nodeID); ok {
				group.Diagnosis = &diagnosis
			}
			switch {
			case checked[nodeID] == 0:
				group.State = "unknown"
			case group.Missing:
				group.State = "missing"
			case group.OnlineBindings == group.TotalBindings && group.UnstableBindings > 0:
				group.State = "unstable"
			case group.OnlineBindings == group.TotalBindings:
				group.State = "healthy"
			case group.OnlineBindings == 0:
				group.State = "offline"
			default:
				group.State = "degraded"
			}
			sort.SliceStable(group.Bindings, func(i, j int) bool {
				return group.Bindings[i].Name < group.Bindings[j].Name
			})
			result = append(result, *group)
		}
		sort.SliceStable(result, func(i, j int) bool {
			if result[i].RepairBindings != result[j].RepairBindings {
				return result[i].RepairBindings > result[j].RepairBindings
			}
			return result[i].Server < result[j].Server
		})
		writeJSON(w, result)
	}
}

// APIStatusHandler returns system status summary
// @Summary Get system status
// @Description Returns summary statistics about all proxies
// @Tags status
// @Produce json
// @Success 200 {object} StatusResponse
// @Router /api/v1/status [get]
func APIStatusHandler(proxyChecker *checker.ProxyChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proxies := proxyChecker.GetProxies()

		var online, offline, unstable, needsReplacement, verifying int
		var totalLatency int64
		var latencyCount int

		for _, proxy := range proxies {
			status, isUnstable, latency, _, _ := proxyChecker.GetProxyResultDetailsByStableID(proxy.StableID)
			monitor, _ := proxyChecker.GetNodeMonitorByStableID(proxy.StableID)
			switch monitor.State {
			case checker.NodeNeedsReplacement, checker.NodeNewIPFailed:
				needsReplacement++
			case checker.NodeIPChanged, checker.NodeVerifyingNewIP:
				verifying++
			}
			if status {
				online++
				if isUnstable {
					unstable++
				}
				if latency > 0 {
					totalLatency += latency.Milliseconds()
					latencyCount++
				}
			} else {
				offline++
			}
		}

		var avgLatency int64
		if latencyCount > 0 {
			avgLatency = totalLatency / int64(latencyCount)
		}

		writeJSON(w, StatusResponse{
			Total:            len(proxies),
			Online:           online,
			Offline:          offline,
			Unstable:         unstable,
			AvgLatencyMs:     avgLatency,
			NeedsReplacement: needsReplacement,
			Verifying:        verifying,
		})
	}
}

// APIConfigHandler returns current configuration
// @Summary Get current configuration
// @Description Returns the current checker configuration
// @Tags config
// @Produce json
// @Success 200 {object} ConfigResponse
// @Router /api/v1/config [get]
func APIConfigHandler(proxyChecker *checker.ProxyChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subNames := CollectSubscriptionNames(proxyChecker.GetProxies())
		writeJSON(w, ConfigResponse{
			CheckInterval:              config.CLIConfig.Proxy.CheckInterval,
			InitialCheckOnly:           config.CLIConfig.Proxy.InitialCheckOnly,
			CheckMethod:                config.CLIConfig.Proxy.CheckMethod,
			Timeout:                    config.CLIConfig.Proxy.Timeout,
			StartPort:                  config.CLIConfig.Xray.StartPort,
			SubscriptionUpdate:         config.CLIConfig.Subscription.Update,
			SubscriptionUpdateInterval: config.CLIConfig.Subscription.UpdateInterval,
			SubscriptionPoolSamples:    config.CLIConfig.Subscription.PoolSamples,
			SimulateLatency:            config.CLIConfig.Proxy.SimulateLatency,
			SubscriptionNames:          subNames,
		})
	}
}

func CollectSubscriptionNames(proxies []*models.ProxyConfig) []string {
	seen := make(map[string]bool)
	var names []string
	for _, proxy := range proxies {
		if proxy.SubName != "" && !seen[proxy.SubName] {
			seen[proxy.SubName] = true
			names = append(names, proxy.SubName)
		}
	}
	return names
}

// APISystemInfoHandler returns system info
// @Summary Get system info
// @Description Returns version, uptime, and instance information
// @Tags system
// @Produce json
// @Success 200 {object} SystemInfoResponse
// @Router /api/v1/system/info [get]
func APISystemInfoHandler(version string, startTime time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uptime := time.Since(startTime)
		writeJSON(w, SystemInfoResponse{
			Version:   version,
			Uptime:    formatDuration(uptime),
			UptimeSec: int64(uptime.Seconds()),
			Instance:  config.CLIConfig.Metrics.Instance,
		})
	}
}

// APISystemIPHandler returns current IP
// @Summary Get current IP
// @Description Returns the current detected IP address
// @Tags system
// @Produce json
// @Success 200 {object} SystemIPResponse
// @Failure 500 {object} map[string]string
// @Router /api/v1/system/ip [get]
func APISystemIPHandler(proxyChecker *checker.ProxyChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip, err := proxyChecker.GetCurrentIP()
		if err != nil {
			writeError(w, "Failed to get IP", http.StatusInternalServerError)
			return
		}
		writeJSON(w, SystemIPResponse{IP: ip})
	}
}

// APINetworkStatusHandler returns the monitored iPhone route status.
// @Summary Get mobile network status
// @Description Returns whether the monitored iPhone route is ready for proxy checks
// @Tags system
// @Produce json
// @Success 200 {object} checker.NetworkStatus
// @Router /api/v1/network [get]
func APINetworkStatusHandler(proxyChecker *checker.ProxyChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, proxyChecker.GetNetworkStatus())
	}
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm %ds", days, hours, minutes, seconds)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func APIOpenAPIHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Write(openAPISpec)
	}
}

func APIDocsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(swaggerUIHTML))
	}
}

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Xray Checker API</title>
  <style>
    body { margin: 0; padding: 0; }
    .swagger-ui .topbar { display: none; }
  </style>
  <script>
    // Detect base path from current URL (e.g., /xray/api/v1/docs -> /xray)
    (function() {
      const path = window.location.pathname;
      const apiIdx = path.indexOf('/api/v1/docs');
      const basePath = apiIdx > 0 ? path.substring(0, apiIdx) : '';
      document.write('<link rel="stylesheet" href="' + basePath + '/static/swagger-ui.css">');
    })();
  </script>
</head>
<body>
  <div id="swagger-ui"></div>
  <script>
    (function() {
      const path = window.location.pathname;
      const apiIdx = path.indexOf('/api/v1/docs');
      const basePath = apiIdx > 0 ? path.substring(0, apiIdx) : '';

      const script = document.createElement('script');
      script.src = basePath + '/static/swagger-ui-bundle.js';
      script.onload = function() {
        SwaggerUIBundle({
          url: './openapi.yaml',
          dom_id: '#swagger-ui',
          presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
          layout: 'BaseLayout'
        });
      };
      document.body.appendChild(script);
    })();
  </script>
</body>
</html>`
