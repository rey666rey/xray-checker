package alerts

import (
	"path/filepath"
	"testing"

	"xray-checker/checker"
)

func TestSourceSessionStateKeepsSharedPreferencesButNotOldIncidents(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "telegram-settings.json")
	prefs := DefaultPreferences()
	prefs.BatchWindowSec = 45
	prefs.Unstable = true
	if err := writeJSONAtomic(settings, Settings{Version: settingsVersion, Enabled: true, ChatID: 42, DeliveryMode: DeliveryAuto, Preferences: prefs}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(filepath.Join(dir, "telegram-settings.state.json"), managerState{Version: stateVersion, Initialized: true, Nodes: map[string]observedNode{"old": {State: "critical"}}}); err != nil {
		t.Fatal(err)
	}
	pc := checker.NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	manager, err := NewManager(pc, 10000, settings, "fixture-token", 0, "", filepath.Join(dir, "network-sources", "router", "session", "telegram-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if manager.settings.ChatID != 42 || manager.settings.Preferences.BatchWindowSec != 45 || !manager.settings.Preferences.Unstable {
		t.Fatal("shared Telegram preferences were lost")
	}
	if manager.state.Initialized || len(manager.state.Nodes) > 0 {
		t.Fatal("old source incidents crossed into a new session")
	}
}
