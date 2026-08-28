package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"xray-checker/checker"
)

func TestTelegramClientVerifyDiscoverAndSend(t *testing.T) {
	var messages atomic.Int32
	server := newTelegramTestServer(t, &messages)
	defer server.Close()

	client := NewTelegramClient()
	client.baseURL = server.URL
	bot, err := client.Verify(context.Background(), "123456:ABC_def")
	if err != nil {
		t.Fatal(err)
	}
	if bot.Username != "checker_alerts_bot" {
		t.Fatalf("username = %q", bot.Username)
	}
	chats, err := client.DiscoverChats(context.Background(), "123456:ABC_def")
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].ID != 42 || chats[0].Label != "Rey" {
		t.Fatalf("unexpected chats: %#v", chats)
	}
	if err := client.SendMessage(context.Background(), "123456:ABC_def", 42, "hello"); err != nil {
		t.Fatal(err)
	}
	if messages.Load() != 1 {
		t.Fatalf("messages = %d", messages.Load())
	}
}

func TestManagerStoresTokenSeparatelyAndKeepsItPrivate(t *testing.T) {
	var messages atomic.Int32
	server := newTelegramTestServer(t, &messages)
	defer server.Close()

	settingsPath := filepath.Join(t.TempDir(), "telegram-settings.json")
	proxyChecker := checker.NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	manager, err := NewManager(proxyChecker, 10000, settingsPath, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	manager.client.baseURL = server.URL

	status, err := manager.Save(context.Background(), SaveSettingsRequest{
		Token: "123456:ABC_def", ChatID: 42, ChatLabel: "Rey", Enabled: true, DeliveryMode: DeliveryDirect,
		Preferences: DefaultPreferences(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.BotUsername != "checker_alerts_bot" {
		t.Fatalf("unexpected status: %#v", status)
	}
	settingsData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(settingsData), "123456:ABC_def") {
		t.Fatal("settings JSON contains the bot token")
	}
	tokenData, err := os.ReadFile(manager.tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(tokenData)) != "123456:ABC_def" {
		t.Fatal("token file does not contain the configured token")
	}
	info, err := os.Stat(manager.tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token permissions = %o", info.Mode().Perm())
	}

	status, err = manager.Save(context.Background(), SaveSettingsRequest{
		Enabled: true, DeliveryMode: DeliveryCustom,
		CustomProxyURL: "socks5h://user:secret@127.0.0.1:1080",
		Preferences:    DefaultPreferences(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.DeliveryMode != DeliveryCustom || !status.CustomProxyConfigured {
		t.Fatalf("custom delivery route was not saved: %#v", status)
	}
	settingsData, err = os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(settingsData), "user:secret") {
		t.Fatal("settings JSON contains custom proxy credentials")
	}
	proxyData, err := os.ReadFile(manager.proxyFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(proxyData)) != "socks5h://user:secret@127.0.0.1:1080" {
		t.Fatal("proxy secret file does not contain the configured URL")
	}
	proxyInfo, err := os.Stat(manager.proxyFile)
	if err != nil {
		t.Fatal(err)
	}
	if proxyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("proxy permissions = %o", proxyInfo.Mode().Perm())
	}

	preferences := DefaultPreferences()
	preferences.IPChanges = false
	status, err = manager.Save(context.Background(), SaveSettingsRequest{
		Enabled: false, DeliveryMode: DeliveryDirect, Preferences: preferences,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.Preferences.IPChanges {
		t.Fatalf("settings update was not applied: %#v", status)
	}
	if err := manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if manager.Status().Configured {
		t.Fatal("manager remains configured after disconnect")
	}
	if _, err := os.Stat(manager.tokenFile); !os.IsNotExist(err) {
		t.Fatalf("token file remains after disconnect: %v", err)
	}
}

func TestTelegramProxyClientValidatesScheme(t *testing.T) {
	for _, valid := range []string{
		"http://127.0.0.1:8080",
		"https://proxy.example:443",
		"socks5://127.0.0.1:1080",
		"socks5h://user:password@127.0.0.1:1080",
	} {
		client, err := telegramProxyClient(valid)
		if err != nil {
			t.Fatalf("telegramProxyClient(%q): %v", valid, err)
		}
		client.CloseIdleConnections()
	}
	for _, invalid := range []string{"", "127.0.0.1:1080", "ftp://127.0.0.1:21"} {
		if _, err := telegramProxyClient(invalid); err == nil {
			t.Fatalf("telegramProxyClient(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestTelegramRetriesOnlyTransportFailures(t *testing.T) {
	if telegramErrorRetryable(errors.New("invalid local input")) {
		t.Fatal("local validation errors must not rotate healthy routes")
	}
	if !telegramErrorRetryable(&telegramCallError{message: "timeout", retryable: true}) {
		t.Fatal("transport errors must rotate to the next route")
	}
	if telegramErrorRetryable(&telegramCallError{message: "unauthorized", retryable: false}) {
		t.Fatal("Telegram API rejection must not rotate healthy routes")
	}
}

func TestFormatNotificationsGroupsRelatedHosts(t *testing.T) {
	messages := formatNotifications([]notification{{
		Kind: "critical", Server: "203.0.113.10", Failures: 3,
		Affected:      []string{"Germany & Reality", "Germany <TLS>"},
		Subscriptions: []string{"LETO"}, LastError: "URL test: 0/2 successful",
		LastSuccess: 1_787_900_000, CreatedAt: 1_787_900_360, IncidentStartedAt: 1_787_900_000,
	}})
	if len(messages) != 1 {
		t.Fatalf("messages = %d", len(messages))
	}
	for _, expected := range []string{
		"🔴 <b>Нода не проходит проверку</b>", "<code>203.0.113.10</code>", "LETO",
		"Germany &amp; Reality", "Germany &lt;TLS&gt;", "3 ошибки подряд", "Apple URL Test: 0/2 успешно",
	} {
		if !strings.Contains(messages[0], expected) {
			t.Fatalf("message does not contain %q: %s", expected, messages[0])
		}
	}
}

func TestFormatNotificationsBuildsCompactDigest(t *testing.T) {
	messages := formatNotifications([]notification{
		{Kind: "critical", Server: "203.0.113.10", Failures: 3, Affected: []string{"Germany Reality"}, Subscriptions: []string{"MASTER"}},
		{Kind: "ip_changed", PreviousAddress: "198.51.100.1", CurrentAddress: "198.51.100.2", Names: []string{"Spain"}, Subscriptions: []string{"LETO"}},
		{Kind: "recovered", Server: "192.0.2.20", LatencyMs: 374, Names: []string{"Turkey"}, Subscriptions: []string{"LETO"}},
	})
	if len(messages) != 1 {
		t.Fatalf("messages = %d", len(messages))
	}
	for _, expected := range []string{
		"🔴 <b>Требуют внимания</b> · 1",
		"🔵 <b>Изменился IP</b> · 1",
		"🟢 <b>Работают снова</b> · 1",
		"<code>198.51.100.1</code> → <code>198.51.100.2</code>",
		"Turkey · LETO · 374 мс",
	} {
		if !strings.Contains(messages[0], expected) {
			t.Fatalf("digest does not contain %q: %s", expected, messages[0])
		}
	}
	if strings.Contains(messages[0], "──────────") {
		t.Fatalf("digest still contains legacy dividers: %s", messages[0])
	}
}

func TestFormatNotificationDistinguishesVerifiedNewIP(t *testing.T) {
	message := formatNotification(notification{
		Kind: "new_ip_verified", Server: "198.51.100.2", CurrentAddress: "198.51.100.2",
		LatencyMs: 412, Names: []string{"Spain"}, Subscriptions: []string{"MASTER"},
	})
	for _, expected := range []string{"🟢 <b>Новый IP работает</b>", "<code>198.51.100.2</code>", "MASTER · Spain", "412 мс"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("message does not contain %q: %s", expected, message)
		}
	}
}

func newTelegramTestServer(t *testing.T, messages *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":     true,
				"result": map[string]interface{}{"id": 1, "username": "checker_alerts_bot", "first_name": "Checker"},
			})
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok": true,
				"result": []interface{}{
					map[string]interface{}{"update_id": 1, "message": map[string]interface{}{
						"text": "/start", "chat": map[string]interface{}{"id": 42, "type": "private", "first_name": "Rey"},
					}},
					map[string]interface{}{"update_id": 2, "message": map[string]interface{}{
						"text": "hello", "chat": map[string]interface{}{"id": 99, "type": "private", "first_name": "Ignored"},
					}},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var payload map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if payload["parse_mode"] != "HTML" {
				http.Error(w, "missing HTML parse mode", http.StatusBadRequest)
				return
			}
			messages.Add(1)
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": map[string]interface{}{"message_id": 1}})
		default:
			http.NotFound(w, r)
		}
	}))
}
