package subscription

import (
	"strings"
	"testing"

	"xray-checker/models"
)

func TestFilterExcludedHostsMatchesPrefixesAndStandaloneMarkersCaseInsensitively(t *testing.T) {
	ltePrimary := &models.ProxyConfig{Name: "🇷🇺 LTE Moscow"}
	lteBackup := &models.ProxyConfig{Name: "  [lTe] backup"}
	lteAutoselect := &models.ProxyConfig{Name: "🇪🇺 Автовыбор LTE | WL_LV16", GroupName: "🇪🇺 Автовыбор LTE"}
	ltePrefixedSuffix := &models.ProxyConfig{Name: "LTE2 node"}
	old := &models.ProxyConfig{Name: "OLD Amsterdam"}
	keptWord := &models.ProxyConfig{Name: "MALTESE node"}
	keptNodeLabel := &models.ProxyConfig{Name: "Germany | LTE relay", GroupName: "Germany"}

	kept, excluded, err := FilterExcludedHosts(
		[]*models.ProxyConfig{ltePrimary, keptWord, lteBackup, lteAutoselect, ltePrefixedSuffix, keptNodeLabel, old},
		[]string{" LTE ", "old"},
	)
	if err != nil {
		t.Fatalf("FilterExcludedHosts() error = %v", err)
	}
	if excluded != 5 {
		t.Fatalf("excluded = %d, want 5", excluded)
	}
	if len(kept) != 2 || kept[0] != keptWord || kept[1] != keptNodeLabel {
		t.Fatalf("kept = %#v, want only non-marker matches", kept)
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
