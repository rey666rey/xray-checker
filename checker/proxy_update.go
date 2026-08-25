package checker

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"xray-checker/models"
)

func ProxySetRevisionFingerprint(proxies []*models.ProxyConfig) string {
	items := make([]string, 0, len(proxies))
	for _, proxy := range proxies {
		items = append(items, displayIdentity(proxy)+"\x00"+proxy.GenerateRevisionID())
	}
	sort.Strings(items)
	sum := sha256.Sum256([]byte(strings.Join(items, "\x1f")))
	return hex.EncodeToString(sum[:])
}

type ProxyChangeKind string

const (
	ProxyAdded   ProxyChangeKind = "added"
	ProxyChanged ProxyChangeKind = "changed"
	ProxyRenamed ProxyChangeKind = "renamed"
	ProxyRemoved ProxyChangeKind = "removed"
)

type ProxyChange struct {
	Kind          ProxyChangeKind
	Old           *models.ProxyConfig
	New           *models.ProxyConfig
	ChangedFields []string
}

type ProxyUpdatePlan struct {
	Changes []ProxyChange
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func (plan ProxyUpdatePlan) ProxiesToCheck() []*models.ProxyConfig {
	proxies := make([]*models.ProxyConfig, 0)
	for _, change := range plan.Changes {
		if change.Kind == ProxyAdded || change.Kind == ProxyChanged {
			proxies = append(proxies, change.New)
		}
	}
	return proxies
}

func (plan ProxyUpdatePlan) Count(kind ProxyChangeKind) int {
	count := 0
	for _, change := range plan.Changes {
		if change.Kind == kind {
			count++
		}
	}
	return count
}

// PlanProxyUpdate keeps a logical node identity across subscription revisions.
// Exact private connection revisions are paired first (covering renames), then
// remaining entries are paired by panel display identity (covering IP/config
// replacements). Duplicate names are paired deterministically.
func PlanProxyUpdate(oldProxies, newProxies []*models.ProxyConfig) ProxyUpdatePlan {
	models.AssignLogicalIDs(oldProxies)

	old := append([]*models.ProxyConfig(nil), oldProxies...)
	newSet := append([]*models.ProxyConfig(nil), newProxies...)
	sortProxySet(old)
	sortProxySet(newSet)

	matchedOld := make(map[*models.ProxyConfig]bool, len(old))
	matchedNew := make(map[*models.ProxyConfig]bool, len(newSet))
	plan := ProxyUpdatePlan{}

	match := func(key func(*models.ProxyConfig) string) {
		buckets := make(map[string][]*models.ProxyConfig)
		for _, proxy := range old {
			if !matchedOld[proxy] {
				buckets[key(proxy)] = append(buckets[key(proxy)], proxy)
			}
		}
		for _, next := range newSet {
			if matchedNew[next] {
				continue
			}
			candidates := buckets[key(next)]
			if len(candidates) == 0 {
				continue
			}
			candidateIndex := 0
			for index, candidate := range candidates {
				if displayIdentity(candidate) == displayIdentity(next) {
					candidateIndex = index
					break
				}
			}
			previous := candidates[candidateIndex]
			buckets[key(next)] = append(candidates[:candidateIndex], candidates[candidateIndex+1:]...)
			matchedOld[previous] = true
			matchedNew[next] = true
			next.LogicalID = previous.LogicalID
			kind := ProxyRenamed
			if previous.GenerateRevisionID() != next.GenerateRevisionID() {
				kind = ProxyChanged
			}
			if kind == ProxyChanged || displayIdentity(previous) != displayIdentity(next) {
				change := ProxyChange{Kind: kind, Old: previous, New: next}
				if kind == ProxyChanged {
					change.ChangedFields = models.RevisionDiffFields(previous, next)
				}
				plan.Changes = append(plan.Changes, change)
			}
		}
	}

	match(func(proxy *models.ProxyConfig) string { return proxy.GenerateRevisionID() })
	match(displayIdentity)

	models.AssignLogicalIDs(newSet)
	for _, proxy := range newSet {
		if !matchedNew[proxy] {
			plan.Changes = append(plan.Changes, ProxyChange{Kind: ProxyAdded, New: proxy})
		}
	}
	for _, proxy := range old {
		if !matchedOld[proxy] {
			plan.Changes = append(plan.Changes, ProxyChange{Kind: ProxyRemoved, Old: proxy})
		}
	}
	return plan
}

func displayIdentity(proxy *models.ProxyConfig) string {
	return strings.Join([]string{
		strings.ToLower(strings.TrimSpace(proxy.Protocol)),
		strings.TrimSpace(proxy.SubName),
		strings.TrimSpace(proxy.GroupName),
		strings.TrimSpace(proxy.Name),
	}, "\x00")
}

func sortProxySet(proxies []*models.ProxyConfig) {
	sort.SliceStable(proxies, func(i, j int) bool {
		left := displayIdentity(proxies[i]) + "\x00" + proxies[i].GenerateRevisionID()
		right := displayIdentity(proxies[j]) + "\x00" + proxies[j].GenerateRevisionID()
		return left < right
	})
}
