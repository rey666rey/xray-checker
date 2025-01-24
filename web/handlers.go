package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"xray-checker/checker"
	"xray-checker/config"
	"xray-checker/metrics"
	"xray-checker/models"
)

var registeredEndpoints []EndpointInfo

type EndpointInfo struct {
	Name      string
	URL       string
	ProxyPort int
	Status    bool
	Latency   time.Duration
}

func IndexHandler(version string, proxyChecker *checker.ProxyChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		RegisterConfigEndpoints(proxyChecker.GetProxies(), proxyChecker, config.CLIConfig.Xray.StartPort)

		data := PageData{
			Version:                    version,
			Port:                       config.CLIConfig.Metrics.Port,
			CheckInterval:              config.CLIConfig.Proxy.CheckInterval,
			IPCheckUrl:                 config.CLIConfig.Proxy.IpCheckUrl,
			CheckMethod:                config.CLIConfig.Proxy.CheckMethod,
			StatusCheckUrl:             config.CLIConfig.Proxy.StatusCheckUrl,
			SimulateLatency:            config.CLIConfig.Proxy.SimulateLatency,
			Timeout:                    config.CLIConfig.Proxy.Timeout,
			SubscriptionUpdate:         config.CLIConfig.Subscription.Update,
			SubscriptionUpdateInterval: config.CLIConfig.Subscription.UpdateInterval,
			StartPort:                  config.CLIConfig.Xray.StartPort,
			Instance:                   config.CLIConfig.Metrics.Instance,
			PushUrl:                    metrics.GetPushURL(config.CLIConfig.Metrics.PushURL),
			Endpoints:                  registeredEndpoints,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := RenderIndex(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}
}

func BasicAuthMiddleware(username, password string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || user != username || pass != password {
				w.Header().Set("WWW-Authenticate", `Basic realm="metrics"`)
				http.Error(w, "Unauthorized.", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func ConfigStatusHandler(proxyChecker *checker.ProxyChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path[len("/config/"):]
		if path == "" {
			http.Error(w, "Config path is required", http.StatusBadRequest)
			return
		}

		var found *models.ProxyConfig
		for _, proxy := range proxyChecker.GetProxies() {
			proxyPath := fmt.Sprintf("%d-%s-%s-%d", proxy.Index, proxy.Protocol, proxy.Server, proxy.Port)
			if proxyPath == path {
				found = proxy
				break
			}
		}

		if found == nil {
			http.Error(w, "Config not found", http.StatusNotFound)
			return
		}

		status, latency, err := proxyChecker.GetProxyStatus(found.Name)
		if err != nil {
			http.Error(w, "Config not found", http.StatusNotFound)
			return
		}

		if config.CLIConfig.Proxy.SimulateLatency {
			time.Sleep(time.Duration(latency))
		}

		if status {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Failed"))
		}
	}
}

func RegisterConfigEndpoints(proxies []*models.ProxyConfig, proxyChecker *checker.ProxyChecker, startPort int) {
	registeredEndpoints = make([]EndpointInfo, 0, len(proxies))

	for _, proxy := range proxies {
		endpoint := fmt.Sprintf("./config/%d-%s-%s-%d",
			proxy.Index,
			proxy.Protocol,
			proxy.Server,
			proxy.Port,
		)

		status, latency, _ := proxyChecker.GetProxyStatus(proxy.Name)

		registeredEndpoints = append(registeredEndpoints, EndpointInfo{
			Name:      fmt.Sprintf("%s (%s:%d)", proxy.Name, proxy.Server, proxy.Port),
			URL:       endpoint,
			ProxyPort: startPort + proxy.Index,
			Status:    status,
			Latency:   latency,
		})
	}
}

// PrefixedMux is a custom ServeMux that handles only paths with a specific prefix.
type PrefixServeMux struct {
	prefix string
	mux    *http.ServeMux
}

// NewPrefixedMux creates a new PrefixedMux with the given prefix.
func NewPrefixServeMux(prefix string) (*PrefixServeMux, error) {
	if strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("served url path prefix '%s' should not ends with a '/'", prefix)
	}
	return &PrefixServeMux{
		prefix: prefix,
		mux:    http.NewServeMux(),
	}, nil
}

// Handle registers a handler for a specific pattern, relative to the prefix.
func (pm *PrefixServeMux) Handle(pattern string, handler http.Handler) {
	// Combine the prefix with the pattern for routing.
	pm.mux.Handle(pm.prefix+pattern, http.StripPrefix(pm.prefix, handler))
}

// ServeHTTP processes requests, ensuring only prefixed paths are handled.
func (pm *PrefixServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Filter requests to match the prefix
	if r.URL.Path == pm.prefix || strings.HasPrefix(r.URL.Path, pm.prefix+"/") {
		pm.mux.ServeHTTP(w, r)
	} else {
		// Return 404 for non-matching paths.
		http.NotFound(w, r)
	}
}
