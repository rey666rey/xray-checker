package checker

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"xray-checker/models"
)

const endpointDetachAfterPolls = 30

// EndpointPool accumulates the endpoints returned for each subscription host.
// Some panels select one member from a host's node pool on every request; a
// snapshot diff therefore cannot represent the real topology. The pool keeps
// every recently observed host+endpoint binding active for the lifetime of the
// container and expires it only after many successful polls without a sighting.
type EndpointPool struct {
	mu         sync.RWMutex
	round      int64
	candidates map[string]*endpointCandidate
}

type endpointCandidate struct {
	config          *models.ProxyConfig
	firstSeenAt     int64
	lastSeenAt      int64
	lastSeenRound   int64
	missingPolls    int
	pendingRevision string
	pendingRounds   int
	pendingConfig   *models.ProxyConfig
}

type EndpointPoolStats struct {
	Bindings int
	Added    int
	Updated  int
	Detached int
	Missing  int
}

type EndpointObservation struct {
	HostID       string `json:"hostId"`
	NodeID       string `json:"nodeId"`
	Server       string `json:"server"`
	Port         int    `json:"port"`
	FirstSeenAt  int64  `json:"firstSeenAt"`
	LastSeenAt   int64  `json:"lastSeenAt"`
	MissingPolls int    `json:"missingPolls"`
}

func NewEndpointPool(initial []*models.ProxyConfig) *EndpointPool {
	pool := &EndpointPool{candidates: make(map[string]*endpointCandidate)}
	now := time.Now().Unix()
	for _, proxy := range initial {
		clone := cloneProxyConfig(proxy)
		ensureTopologyIDs(clone)
		pool.candidates[endpointPoolKey(clone)] = &endpointCandidate{
			config: clone, firstSeenAt: now, lastSeenAt: now,
		}
	}
	return pool
}

func (pc *ProxyChecker) SetEndpointPool(pool *EndpointPool) {
	pc.mu.Lock()
	pc.endpointPool = pool
	pc.mu.Unlock()
}

func (pc *ProxyChecker) GetEndpointObservation(proxy *models.ProxyConfig) (EndpointObservation, bool) {
	pc.mu.RLock()
	pool := pc.endpointPool
	pc.mu.RUnlock()
	if pool == nil {
		return EndpointObservation{}, false
	}
	return pool.Observation(proxy)
}

// Observe merges multiple successful reads from one poll. A newly observed
// endpoint is immediately testable; unlike the old five-identical-polls rule,
// alternating A/B/A/B responses correctly produce a two-member pool. Existing
// endpoints need 30 missed polls before detachment so pools of up to ten members
// do not flap merely because random sampling did not select one for a while.
func (pool *EndpointPool) Observe(reads ...[]*models.ProxyConfig) ([]*models.ProxyConfig, EndpointPoolStats) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	pool.round++
	now := time.Now().Unix()

	type seenRevision struct {
		config *models.ProxyConfig
		count  int
	}
	seen := make(map[string]map[string]*seenRevision)
	for _, read := range reads {
		for _, proxy := range read {
			clone := cloneProxyConfig(proxy)
			ensureTopologyIDs(clone)
			key := endpointPoolKey(clone)
			revision := clone.GenerateRevisionID()
			if seen[key] == nil {
				seen[key] = make(map[string]*seenRevision)
			}
			entry := seen[key][revision]
			if entry == nil {
				entry = &seenRevision{config: clone}
				seen[key][revision] = entry
			}
			entry.count++
		}
	}

	stats := EndpointPoolStats{}
	for key, revisions := range seen {
		var observed *seenRevision
		for _, candidate := range revisions {
			if observed == nil || candidate.count > observed.count {
				observed = candidate
			}
		}
		current := pool.candidates[key]
		if current == nil {
			pool.candidates[key] = &endpointCandidate{
				config: observed.config, firstSeenAt: now, lastSeenAt: now,
				lastSeenRound: pool.round,
			}
			stats.Added++
			continue
		}
		current.lastSeenAt = now
		current.lastSeenRound = pool.round
		current.missingPolls = 0
		observedRevision := observed.config.GenerateRevisionID()
		if current.config.GenerateRevisionID() == observedRevision {
			copyDisplayMetadata(current.config, observed.config)
			current.pendingRevision = ""
			current.pendingRounds = 0
			current.pendingConfig = nil
			continue
		}

		// A credential/transport change on the same endpoint is accepted when it
		// appears in both immediate reads, or in two consecutive poll rounds.
		if observed.count >= 2 {
			current.config = observed.config
			current.pendingRevision = ""
			current.pendingRounds = 0
			current.pendingConfig = nil
			stats.Updated++
			continue
		}
		if current.pendingRevision == observedRevision {
			current.pendingRounds++
		} else {
			current.pendingRevision = observedRevision
			current.pendingRounds = 1
			current.pendingConfig = observed.config
		}
		if current.pendingRounds >= 2 {
			current.config = current.pendingConfig
			current.pendingRevision = ""
			current.pendingRounds = 0
			current.pendingConfig = nil
			stats.Updated++
		}
	}

	for key, candidate := range pool.candidates {
		if candidate.lastSeenRound == pool.round {
			continue
		}
		candidate.missingPolls++
		if candidate.missingPolls >= endpointDetachAfterPolls {
			delete(pool.candidates, key)
			stats.Detached++
			continue
		}
		stats.Missing++
	}

	configs := make([]*models.ProxyConfig, 0, len(pool.candidates))
	for _, candidate := range pool.candidates {
		configs = append(configs, cloneProxyConfig(candidate.config))
	}
	sort.SliceStable(configs, func(i, j int) bool {
		left := configs[i].SubName + "\x00" + configs[i].GroupName + "\x00" + configs[i].Name + "\x00" + configs[i].Server + fmt.Sprintf(":%05d", configs[i].Port)
		right := configs[j].SubName + "\x00" + configs[j].GroupName + "\x00" + configs[j].Name + "\x00" + configs[j].Server + fmt.Sprintf(":%05d", configs[j].Port)
		return left < right
	})
	stats.Bindings = len(configs)
	return configs, stats
}

func (pool *EndpointPool) Observation(proxy *models.ProxyConfig) (EndpointObservation, bool) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	candidate := pool.candidates[endpointPoolKey(proxy)]
	if candidate == nil {
		return EndpointObservation{}, false
	}
	return EndpointObservation{
		HostID: proxy.HostID, NodeID: proxy.NodeID, Server: proxy.Server, Port: proxy.Port,
		FirstSeenAt: candidate.firstSeenAt, LastSeenAt: candidate.lastSeenAt,
		MissingPolls: candidate.missingPolls,
	}, true
}

func ensureTopologyIDs(proxy *models.ProxyConfig) {
	if proxy.HostID == "" {
		proxy.HostID = proxy.GenerateHostID()
	}
	proxy.NodeID = proxy.GenerateNodeID()
	if proxy.LogicalID == "" {
		proxy.LogicalID = proxy.GenerateBindingID()
	}
}

func endpointPoolKey(proxy *models.ProxyConfig) string {
	hostID := proxy.HostID
	if hostID == "" {
		hostID = proxy.GenerateHostID()
	}
	server := strings.ToLower(strings.TrimSpace(proxy.Server))
	if ip := net.ParseIP(server); ip != nil {
		server = ip.String()
	}
	return hostID + "\x00" + server + fmt.Sprintf(":%d", proxy.Port)
}

func copyDisplayMetadata(target, source *models.ProxyConfig) {
	target.Name = source.Name
	target.SubName = source.SubName
	target.GroupName = source.GroupName
	target.MetricsLabels = cloneStringMap(source.MetricsLabels)
	target.HostID = source.HostID
	target.NodeID = source.NodeID
}

func cloneProxyConfig(source *models.ProxyConfig) *models.ProxyConfig {
	clone := *source
	clone.ALPN = append([]string(nil), source.ALPN...)
	clone.WGAddresses = append([]string(nil), source.WGAddresses...)
	clone.WGAllowedIPs = append([]string(nil), source.WGAllowedIPs...)
	clone.WGDNS = append([]string(nil), source.WGDNS...)
	clone.Settings = cloneStringMap(source.Settings)
	clone.MetricsLabels = cloneStringMap(source.MetricsLabels)
	return &clone
}
