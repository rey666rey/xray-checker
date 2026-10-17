package networksource

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var sessionName = regexp.MustCompile(`^[a-f0-9]{32}$`)

// PruneSessions bounds retained measurements, never settings or legacy history.
// Only the exact source/epoch layout emitted by compose.yaml is eligible.
func PruneSessions(resultsFile string, keep int) error {
	current := filepath.Dir(resultsFile)
	parent := filepath.Dir(current)
	if keep < 1 || filepath.Base(resultsFile) != "results.json" || !sessionName.MatchString(filepath.Base(current)) || (filepath.Base(parent) != "iphone" && filepath.Base(parent) != "router") || filepath.Base(filepath.Dir(parent)) != "network-sources" {
		return nil
	}
	entries, err := os.ReadDir(parent)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	type candidate struct {
		name     string
		modified int64
	}
	var old []candidate
	for _, entry := range entries {
		if !entry.IsDir() || !sessionName.MatchString(entry.Name()) || entry.Name() == filepath.Base(current) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		old = append(old, candidate{entry.Name(), info.ModTime().UnixNano()})
	}
	sort.Slice(old, func(i, j int) bool { return old[i].modified > old[j].modified })
	for _, item := range old[min(keep, len(old)):] {
		if err := os.RemoveAll(filepath.Join(parent, item.name)); err != nil {
			return err
		}
	}
	return nil
}
