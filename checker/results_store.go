package checker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xray-checker/logger"
)

const resultsSnapshotVersion = 1

type persistedResultsSnapshot struct {
	Version  int                    `json:"version"`
	Complete bool                   `json:"complete"`
	Results  []persistedProxyResult `json:"results"`
}

type persistedProxyResult struct {
	Protocol  string `json:"protocol"`
	Address   string `json:"address"`
	Name      string `json:"name"`
	SubName   string `json:"subName"`
	StableID  string `json:"stableId"`
	GroupName string `json:"groupName"`
	Online    bool   `json:"online"`
	Unstable  bool   `json:"unstable,omitempty"`
	LatencyNS int64  `json:"latencyNs"`
	LastCheck int64  `json:"lastCheck"`
}

// SetResultsFile restores the most recent completed sweep and enables atomic,
// debounced snapshots. The snapshot lives on a Docker volume, so a Colima bridge
// recreation does not blank the dashboard or force another full sweep.
func (pc *ProxyChecker) SetResultsFile(path string) error {
	pc.resultsFile = strings.TrimSpace(path)
	if pc.resultsFile == "" {
		return nil
	}

	if err := pc.loadResults(); err != nil {
		return err
	}
	pc.persistSignal = make(chan struct{}, 1)
	go pc.resultsPersistLoop()
	return nil
}

func (pc *ProxyChecker) loadResults() error {
	data, err := os.ReadFile(pc.resultsFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read results snapshot: %w", err)
	}

	var snapshot persistedResultsSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode results snapshot: %w", err)
	}
	if snapshot.Version != resultsSnapshotVersion {
		return fmt.Errorf("unsupported results snapshot version %d", snapshot.Version)
	}

	byStableID := make(map[string]persistedProxyResult, len(snapshot.Results))
	byDisplay := make(map[string][]persistedProxyResult, len(snapshot.Results))
	for _, item := range snapshot.Results {
		previous, exists := byStableID[item.StableID]
		if !exists || item.LastCheck > previous.LastCheck {
			byStableID[item.StableID] = item
		}
		key := persistedDisplayKey(item.Protocol, item.Name, item.SubName, item.GroupName)
		byDisplay[key] = append(byDisplay[key], item)
	}

	restored := 0
	pc.mu.RLock()
	for _, proxy := range pc.proxies {
		if proxy.StableID == "" {
			proxy.StableID = proxy.GenerateStableID()
		}
		item, exists := byStableID[proxy.StableID]
		if !exists {
			candidates := byDisplay[persistedDisplayKey(proxy.Protocol, proxy.Name, proxy.SubName, proxy.GroupName)]
			if len(candidates) == 1 {
				item, exists = candidates[0], true
			} else {
				address := fmt.Sprintf("%s:%d", proxy.Server, proxy.Port)
				for _, candidate := range candidates {
					if candidate.Address == address {
						item, exists = candidate, true
						break
					}
				}
			}
			if !exists {
				continue
			}
		}
		pc.results.Store(proxyMetricKey(proxy), proxyResult{
			status:    item.Online,
			unstable:  item.Unstable,
			latency:   time.Duration(item.LatencyNS),
			lastCheck: time.Unix(item.LastCheck, 0),
		})
		restored++
	}
	pc.mu.RUnlock()

	complete := snapshot.Complete && pc.currentProxySetHasResults()
	pc.resultsComplete.Store(complete)
	pc.resumeFromSnapshot.Store(restored > 0 && !complete)
	pc.resumeWasComplete.Store(snapshot.Complete && restored > 0 && !complete)
	logger.Info("Restored %d proxy results from disk (complete=%t)", restored, complete)
	return nil
}

func persistedDisplayKey(protocol, name, subName, groupName string) string {
	return strings.Join([]string{protocol, name, subName, groupName}, "\x00")
}

func (pc *ProxyChecker) currentProxySetHasResults() bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	if len(pc.proxies) == 0 {
		return false
	}
	for _, proxy := range pc.proxies {
		if proxy.StableID == "" {
			proxy.StableID = proxy.GenerateStableID()
		}
		if _, ok := pc.results.Load(proxyMetricKey(proxy)); !ok {
			return false
		}
	}
	return true
}

func (pc *ProxyChecker) storeResult(key proxyMetricLabels, result proxyResult) {
	pc.results.Store(key, result)
	pc.scheduleResultsPersist()
}

func (pc *ProxyChecker) markResultsComplete(complete bool) {
	pc.resultsComplete.Store(complete)
	if complete {
		if err := pc.persistResults(); err != nil {
			logger.Warn("Could not save completed proxy results: %v", err)
		}
		return
	}
	pc.scheduleResultsPersist()
}

func (pc *ProxyChecker) HasCompleteResults() bool {
	return pc.resultsFile != "" && pc.resultsComplete.Load() && pc.currentProxySetHasResults()
}

func (pc *ProxyChecker) scheduleResultsPersist() {
	if pc.persistSignal == nil {
		return
	}
	select {
	case pc.persistSignal <- struct{}{}:
	default:
	}
}

func (pc *ProxyChecker) resultsPersistLoop() {
	for range pc.persistSignal {
		time.Sleep(300 * time.Millisecond)
		for {
			select {
			case <-pc.persistSignal:
			default:
				if err := pc.persistResults(); err != nil {
					logger.Warn("Could not save proxy results: %v", err)
				}
				goto next
			}
		}
	next:
	}
}

func (pc *ProxyChecker) persistResults() error {
	if pc.resultsFile == "" {
		return nil
	}
	pc.persistMu.Lock()
	defer pc.persistMu.Unlock()

	snapshot := persistedResultsSnapshot{
		Version:  resultsSnapshotVersion,
		Complete: pc.resultsComplete.Load(),
	}
	pc.mu.RLock()
	proxies := make([]proxyMetricLabels, 0, len(pc.proxies))
	for _, proxy := range pc.proxies {
		if proxy.StableID == "" {
			proxy.StableID = proxy.GenerateStableID()
		}
		proxies = append(proxies, proxyMetricKey(proxy))
	}
	pc.mu.RUnlock()
	for _, key := range proxies {
		resultValue, exists := pc.results.Load(key)
		if !exists {
			continue
		}
		result := resultValue.(proxyResult)
		snapshot.Results = append(snapshot.Results, persistedProxyResult{
			Protocol:  key.protocol,
			Address:   key.address,
			Name:      key.name,
			SubName:   key.subName,
			StableID:  key.stableID,
			GroupName: key.groupName,
			Online:    result.status,
			Unstable:  result.unstable,
			LatencyNS: int64(result.latency),
			LastCheck: result.lastCheck.Unix(),
		})
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode results snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(pc.resultsFile), 0o755); err != nil {
		return fmt.Errorf("create results directory: %w", err)
	}
	temporary := pc.resultsFile + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write results snapshot: %w", err)
	}
	if err := os.Rename(temporary, pc.resultsFile); err != nil {
		return fmt.Errorf("replace results snapshot: %w", err)
	}
	return nil
}
