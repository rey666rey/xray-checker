package xray

import (
	"xray-checker/models"
)

func PrepareProxyConfigs(proxies []*models.ProxyConfig) {
	for i := range proxies {
		proxies[i].Index = i
	}
	// Assign final StableIDs over the whole set so that identical-connection configs
	// are separated deterministically (see models.AssignStableIDs).
	models.AssignStableIDs(proxies)
}

func IsConfigsEqual(old, new []*models.ProxyConfig) bool {
	if len(old) != len(new) {
		return false
	}

	// Compare on the base content hash directly (not the assigned StableID, which may
	// carry a collision suffix) so the comparison is independent of dedup ordering and
	// detects only genuine subscription content changes.
	oldMap := make(map[string]bool)
	newMap := make(map[string]bool)

	for _, cfg := range old {
		oldMap[cfg.GenerateStableID()] = true
	}

	for _, cfg := range new {
		newMap[cfg.GenerateStableID()] = true
	}

	for id := range oldMap {
		if !newMap[id] {
			return false
		}
	}

	for id := range newMap {
		if !oldMap[id] {
			return false
		}
	}

	return true
}
