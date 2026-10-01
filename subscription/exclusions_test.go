package subscription

import (
	"strings"
	"testing"

	"xray-checker/models"
)

func TestFilterExcludedHostsMatchesTrimmedNamePrefixesCaseInsensitively(t *testing.T) {
	ltePrimary := &models.ProxyConfig{Name: "🇷🇺 LTE Moscow"}
	lteBackup := &models.ProxyConfig{Name: "  [lTe] backup"}
	old := &models.ProxyConfig{Name: "OLD Amsterdam"}
	keptHost := &models.ProxyConfig{Name: "Germany LTE"}

	kept, excluded, err := FilterExcludedHosts(
		[]*models.ProxyConfig{ltePrimary, keptHost, lteBackup, old},
		[]string{" LTE ", "old"},
	)
	if err != nil {
		t.Fatalf("FilterExcludedHosts() error = %v", err)
	}
	if excluded != 3 {
		t.Fatalf("excluded = %d, want 3", excluded)
	}
	if len(kept) != 1 || kept[0] != keptHost {
		t.Fatalf("kept = %#v, want only non-prefix match", kept)
	}
}

func TestFilterExcludedHostsIgnoresBlankPrefixes(t *testing.T) {
	configs := []*models.ProxyConfig{{Name: "LTE Moscow"}}
	kept, excluded, err := FilterExcludedHosts(configs, []string{"", "  "})
	if err != nil || excluded != 0 || len(kept) != 1 {
		t.Fatalf("kept=%#v excluded=%d err=%v", kept, excluded, err)
	}
}

func TestFilterExcludedHostsRejectsAccidentalEmptySet(t *testing.T) {
	configs := []*models.ProxyConfig{{Name: "LTE Moscow"}, {Name: "LTE Berlin"}}
	kept, excluded, err := FilterExcludedHosts(configs, []string{"lte"})
	if err == nil || !strings.Contains(err.Error(), "matched all 2") {
		t.Fatalf("kept=%#v excluded=%d err=%v; want all-hosts error", kept, excluded, err)
	}
	if excluded != 2 || kept != nil {
		t.Fatalf("kept=%#v excluded=%d, want nil/2", kept, excluded)
	}
}
