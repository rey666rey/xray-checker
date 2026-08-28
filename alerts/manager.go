package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"xray-checker/checker"
	"xray-checker/logger"
	"xray-checker/models"
)

const (
	settingsVersion = 2
	stateVersion    = 2
	defaultBatchSec = 30
	defaultRepeatHr = 12

	DeliveryAuto   = "auto"
	DeliveryDirect = "direct"
	DeliveryCustom = "custom"
)

type Preferences struct {
	CriticalFailures bool `json:"criticalFailures"`
	IPChanges        bool `json:"ipChanges"`
	Recoveries       bool `json:"recoveries"`
	NewIPFailures    bool `json:"newIpFailures"`
	Unstable         bool `json:"unstable"`
	NetworkStatus    bool `json:"networkStatus"`
	BatchWindowSec   int  `json:"batchWindowSec"`
	RepeatHours      int  `json:"repeatHours"`
}

type Settings struct {
	Version      int         `json:"version"`
	Enabled      bool        `json:"enabled"`
	ChatID       int64       `json:"chatId"`
	ChatLabel    string      `json:"chatLabel"`
	BotUsername  string      `json:"botUsername"`
	DeliveryMode string      `json:"deliveryMode"`
	Preferences  Preferences `json:"preferences"`
}

type SaveSettingsRequest struct {
	Token          string      `json:"token"`
	ChatID         int64       `json:"chatId"`
	ChatLabel      string      `json:"chatLabel"`
	Enabled        bool        `json:"enabled"`
	DeliveryMode   string      `json:"deliveryMode"`
	CustomProxyURL string      `json:"customProxyUrl"`
	Preferences    Preferences `json:"preferences"`
}

type PublicStatus struct {
	Available             bool        `json:"available"`
	Configured            bool        `json:"configured"`
	Enabled               bool        `json:"enabled"`
	Connected             bool        `json:"connected"`
	ManagedByEnvironment  bool        `json:"managedByEnvironment"`
	ProxyManagedByEnv     bool        `json:"proxyManagedByEnvironment"`
	State                 string      `json:"state"`
	BotUsername           string      `json:"botUsername,omitempty"`
	Recipient             string      `json:"recipient,omitempty"`
	LastDeliveryAt        int64       `json:"lastDeliveryAt,omitempty"`
	LastError             string      `json:"lastError,omitempty"`
	DeliveryMode          string      `json:"deliveryMode"`
	CustomProxyConfigured bool        `json:"customProxyConfigured"`
	LastRoute             string      `json:"lastRoute,omitempty"`
	AvailableRoutes       int         `json:"availableRoutes"`
	Preferences           Preferences `json:"preferences"`
}

type nodeSnapshot struct {
	NodeID            string
	Server            string
	Names             []string
	Affected          []string
	Subscriptions     []string
	State             string
	AddressChangedAt  int64
	PreviousAddress   string
	CurrentAddress    string
	Failures          int
	LastSuccess       int64
	LastCheck         int64
	LastError         string
	LatencyMs         int64
	IncidentStartedAt int64
	NewIPFailed       bool
}

type observedNode struct {
	State             string `json:"state"`
	AddressChangedAt  int64  `json:"addressChangedAt,omitempty"`
	LastAlertAt       int64  `json:"lastAlertAt,omitempty"`
	LastSeen          int64  `json:"lastSeen"`
	IncidentStartedAt int64  `json:"incidentStartedAt,omitempty"`
}

type notification struct {
	ID                string   `json:"id"`
	Kind              string   `json:"kind"`
	NodeID            string   `json:"nodeId,omitempty"`
	Server            string   `json:"server,omitempty"`
	Names             []string `json:"names,omitempty"`
	Affected          []string `json:"affected,omitempty"`
	Subscriptions     []string `json:"subscriptions,omitempty"`
	PreviousAddress   string   `json:"previousAddress,omitempty"`
	CurrentAddress    string   `json:"currentAddress,omitempty"`
	Failures          int      `json:"failures,omitempty"`
	LastSuccess       int64    `json:"lastSuccess,omitempty"`
	LastError         string   `json:"lastError,omitempty"`
	LatencyMs         int64    `json:"latencyMs,omitempty"`
	IncidentStartedAt int64    `json:"incidentStartedAt,omitempty"`
	NetworkDownAt     int64    `json:"networkDownAt,omitempty"`
	CreatedAt         int64    `json:"createdAt"`
	AddressChangedAt  int64    `json:"addressChangedAt,omitempty"`
}

type managerState struct {
	Version            int                     `json:"version"`
	Initialized        bool                    `json:"initialized"`
	LastScanAt         int64                   `json:"lastScanAt,omitempty"`
	Nodes              map[string]observedNode `json:"nodes"`
	Pending            []notification          `json:"pending,omitempty"`
	BatchStartedAt     int64                   `json:"batchStartedAt,omitempty"`
	NetworkInitialized bool                    `json:"networkInitialized"`
	NetworkReady       bool                    `json:"networkReady"`
	NetworkDownAt      int64                   `json:"networkDownAt,omitempty"`
	LastDeliveryAt     int64                   `json:"lastDeliveryAt,omitempty"`
	LastError          string                  `json:"lastError,omitempty"`
	RetryAt            int64                   `json:"retryAt,omitempty"`
	LastRoute          string                  `json:"lastRoute,omitempty"`
	LastRouteID        string                  `json:"lastRouteId,omitempty"`
	RouteFailures      map[string]int64        `json:"routeFailures,omitempty"`
}

type Manager struct {
	checker      *checker.ProxyChecker
	client       *TelegramClient
	settingsFile string
	tokenFile    string
	proxyFile    string
	stateFile    string
	envToken     string
	envChatID    int64
	envProxy     string
	startPort    int

	mu          sync.RWMutex
	deliveryMu  sync.Mutex
	settings    Settings
	token       string
	customProxy string
	state       managerState
}

func DefaultPreferences() Preferences {
	return Preferences{
		CriticalFailures: true,
		IPChanges:        true,
		Recoveries:       true,
		NewIPFailures:    true,
		Unstable:         false,
		NetworkStatus:    true,
		BatchWindowSec:   defaultBatchSec,
		RepeatHours:      defaultRepeatHr,
	}
}

func NewManager(proxyChecker *checker.ProxyChecker, startPort int, settingsFile, envToken string, envChatID int64, envProxy string) (*Manager, error) {
	settingsFile = strings.TrimSpace(settingsFile)
	manager := &Manager{
		checker:      proxyChecker,
		client:       NewTelegramClient(),
		settingsFile: settingsFile,
		envToken:     strings.TrimSpace(envToken),
		envChatID:    envChatID,
		envProxy:     strings.TrimSpace(envProxy),
		startPort:    startPort,
		settings: Settings{
			Version:      settingsVersion,
			DeliveryMode: DeliveryAuto,
			Preferences:  DefaultPreferences(),
		},
		state: managerState{Version: stateVersion, Nodes: make(map[string]observedNode), RouteFailures: make(map[string]int64)},
	}
	if settingsFile == "" {
		return manager, nil
	}
	extension := filepath.Ext(settingsFile)
	base := strings.TrimSuffix(settingsFile, extension)
	manager.tokenFile = base + ".token"
	manager.proxyFile = base + ".proxy"
	manager.stateFile = base + ".state.json"
	if err := manager.load(); err != nil {
		return nil, err
	}
	if manager.envToken != "" {
		manager.token = manager.envToken
		manager.settings.Enabled = true
		if manager.envChatID != 0 {
			manager.settings.ChatID = manager.envChatID
		}
	}
	if manager.envProxy != "" {
		manager.customProxy = manager.envProxy
	}
	manager.settings.DeliveryMode = normalizeDeliveryMode(manager.settings.DeliveryMode)
	manager.normalizePreferences()
	return manager, nil
}

func (m *Manager) Start(ctx context.Context) {
	if m.settingsFile == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		m.evaluate(ctx, time.Now())
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				m.evaluate(ctx, now)
			}
		}
	}()
}

func (m *Manager) Status() PublicStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	configured := strings.TrimSpace(m.token) != "" && m.settings.ChatID != 0
	state := "not_configured"
	if configured && !m.settings.Enabled {
		state = "disabled"
	} else if configured && m.state.LastError != "" {
		state = "error"
	} else if configured {
		state = "connected"
	}
	return PublicStatus{
		Available:             m.settingsFile != "",
		Configured:            configured,
		Enabled:               m.settings.Enabled,
		Connected:             configured && m.settings.Enabled && m.state.LastError == "",
		ManagedByEnvironment:  m.envToken != "",
		ProxyManagedByEnv:     m.envProxy != "",
		State:                 state,
		BotUsername:           m.settings.BotUsername,
		Recipient:             m.settings.ChatLabel,
		LastDeliveryAt:        m.state.LastDeliveryAt,
		LastError:             m.state.LastError,
		DeliveryMode:          normalizeDeliveryMode(m.settings.DeliveryMode),
		CustomProxyConfigured: m.customProxy != "",
		LastRoute:             m.state.LastRoute,
		AvailableRoutes:       m.healthyRouteCount(),
		Preferences:           m.settings.Preferences,
	}
}

func (m *Manager) VerifyToken(ctx context.Context, supplied, deliveryMode, customProxyURL string) (BotInfo, error) {
	token, err := m.resolveToken(supplied)
	if err != nil {
		return BotInfo{}, err
	}
	var bot BotInfo
	err = m.tryDelivery(ctx, deliveryMode, customProxyURL, nil, func(client *TelegramClient) error {
		var verifyErr error
		bot, verifyErr = client.Verify(ctx, token)
		return verifyErr
	})
	return bot, err
}

func (m *Manager) DiscoverChats(ctx context.Context, supplied, deliveryMode, customProxyURL string) ([]ChatInfo, error) {
	token, err := m.resolveToken(supplied)
	if err != nil {
		return nil, err
	}
	var chats []ChatInfo
	err = m.tryDelivery(ctx, deliveryMode, customProxyURL, nil, func(client *TelegramClient) error {
		var discoverErr error
		chats, discoverErr = client.DiscoverChats(ctx, token)
		return discoverErr
	})
	return chats, err
}

func (m *Manager) Save(ctx context.Context, request SaveSettingsRequest) (PublicStatus, error) {
	if m.settingsFile == "" {
		return PublicStatus{}, errors.New("Telegram settings storage is disabled")
	}
	token, err := m.resolveToken(request.Token)
	if err != nil {
		return PublicStatus{}, err
	}
	m.mu.RLock()
	previousSettings := m.settings
	previousToken := m.token
	previousProxy := m.customProxy
	m.mu.RUnlock()
	if request.ChatID == 0 {
		request.ChatID = previousSettings.ChatID
	}
	if request.ChatLabel == "" {
		request.ChatLabel = previousSettings.ChatLabel
	}
	if request.ChatID == 0 {
		return PublicStatus{}, errors.New("Choose a Telegram recipient")
	}
	deliveryMode := request.DeliveryMode
	if strings.TrimSpace(deliveryMode) == "" {
		deliveryMode = previousSettings.DeliveryMode
	}
	deliveryMode = normalizeDeliveryMode(deliveryMode)
	customProxy := strings.TrimSpace(request.CustomProxyURL)
	if customProxy == "" {
		customProxy = previousProxy
	}
	if m.envProxy != "" {
		customProxy = m.envProxy
	}
	if deliveryMode == DeliveryCustom {
		if customProxy == "" {
			return PublicStatus{}, errors.New("Custom Telegram proxy URL is required")
		}
		if _, err := telegramProxyClient(customProxy); err != nil {
			return PublicStatus{}, err
		}
	}
	botUsername := previousSettings.BotUsername
	if strings.TrimSpace(request.Token) != "" || botUsername == "" {
		bot, verifyErr := m.verifyThroughRoute(ctx, token, deliveryMode, customProxy)
		if verifyErr != nil {
			return PublicStatus{}, verifyErr
		}
		botUsername = bot.Username
	}

	preferences := request.Preferences
	normalizePreferences(&preferences)
	settings := Settings{
		Version:      settingsVersion,
		Enabled:      request.Enabled,
		ChatID:       request.ChatID,
		ChatLabel:    strings.TrimSpace(request.ChatLabel),
		BotUsername:  botUsername,
		DeliveryMode: deliveryMode,
		Preferences:  preferences,
	}
	if settings.ChatLabel == "" {
		settings.ChatLabel = strconv.FormatInt(settings.ChatID, 10)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.envToken == "" && strings.TrimSpace(request.Token) != "" {
		if err := writeAtomic(m.tokenFile, []byte(strings.TrimSpace(request.Token)+"\n"), 0o600); err != nil {
			return PublicStatus{}, fmt.Errorf("save Telegram token: %w", err)
		}
		m.token = strings.TrimSpace(request.Token)
	}
	if m.envProxy == "" && strings.TrimSpace(request.CustomProxyURL) != "" {
		if err := writeAtomic(m.proxyFile, []byte(strings.TrimSpace(request.CustomProxyURL)+"\n"), 0o600); err != nil {
			return PublicStatus{}, fmt.Errorf("save Telegram proxy: %w", err)
		}
		m.customProxy = strings.TrimSpace(request.CustomProxyURL)
	} else if m.envProxy != "" {
		m.customProxy = m.envProxy
	}
	if err := writeJSONAtomic(m.settingsFile, settings); err != nil {
		return PublicStatus{}, fmt.Errorf("save Telegram settings: %w", err)
	}
	m.settings = settings
	identityChanged := previousSettings.ChatID != settings.ChatID ||
		(strings.TrimSpace(request.Token) != "" && strings.TrimSpace(request.Token) != previousToken)
	wasConfigured := previousToken != "" && previousSettings.ChatID != 0
	enabledChanged := previousSettings.Enabled != settings.Enabled
	routeChanged := normalizeDeliveryMode(previousSettings.DeliveryMode) != settings.DeliveryMode ||
		(strings.TrimSpace(request.CustomProxyURL) != "" && strings.TrimSpace(request.CustomProxyURL) != previousProxy)
	if identityChanged || !wasConfigured || enabledChanged {
		m.state = managerState{
			Version: stateVersion, Nodes: make(map[string]observedNode), RouteFailures: make(map[string]int64),
		}
		if err := m.persistStateLocked(); err != nil {
			logger.Warn("Could not reset Telegram alert state: %v", err)
		}
	}
	if routeChanged {
		m.state.LastError = ""
		m.state.RetryAt = 0
	}
	return m.statusLocked(), nil
}

func (m *Manager) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.envToken != "" {
		return errors.New("Telegram is managed through environment variables")
	}
	m.settings = Settings{Version: settingsVersion, DeliveryMode: DeliveryAuto, Preferences: DefaultPreferences()}
	m.token = ""
	m.customProxy = ""
	m.state = managerState{Version: stateVersion, Nodes: make(map[string]observedNode), RouteFailures: make(map[string]int64)}
	for _, path := range []string{m.settingsFile, m.tokenFile, m.proxyFile, m.stateFile} {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (m *Manager) SendTest(ctx context.Context) error {
	m.mu.RLock()
	token := m.token
	settings := m.settings
	m.mu.RUnlock()
	if token == "" || settings.ChatID == 0 {
		return errors.New("Telegram is not configured")
	}

	nodes := m.collectNodes()
	critical := 0
	for _, node := range nodes {
		if node.State == "critical" {
			critical++
		}
	}
	network := m.checker.GetNetworkStatus()
	networkLabel := "доступен"
	if network.Managed {
		if network.Ready {
			networkLabel = "iPhone подключён"
			if network.PublicIP != "" {
				networkLabel += " · <code>" + escapeHTML(network.PublicIP) + "</code>"
			}
		} else {
			networkLabel = "соединение с iPhone недоступно"
		}
	}
	message := fmt.Sprintf("🧪 <b>Тестовый алерт</b>\n\n🟢 <b>Telegram подключён</b>\nНод в мониторинге: %d\nТребуют внимания: %d\nМаршрут: %s\n\n<i>Так будут выглядеть уведомления Xray Checker.</i>",
		len(nodes), critical, networkLabel)
	if err := m.tryDelivery(ctx, settings.DeliveryMode, "", nil, func(client *TelegramClient) error {
		return client.SendMessage(ctx, token, settings.ChatID, message)
	}); err != nil {
		m.recordDelivery(err)
		return err
	}
	m.recordDelivery(nil)
	return nil
}

func (m *Manager) resolveToken(supplied string) (string, error) {
	if token := strings.TrimSpace(supplied); token != "" {
		if m.envToken != "" && token != m.envToken {
			return "", errors.New("Telegram token is managed through environment variables")
		}
		return token, nil
	}
	m.mu.RLock()
	token := m.token
	m.mu.RUnlock()
	if token == "" {
		return "", errors.New("Telegram bot token is required")
	}
	return token, nil
}

func (m *Manager) evaluate(ctx context.Context, now time.Time) {
	m.mu.RLock()
	enabled := m.settings.Enabled && m.token != "" && m.settings.ChatID != 0
	m.mu.RUnlock()
	if !enabled {
		return
	}

	nodes := m.collectNodes()
	network := m.checker.GetNetworkStatus()
	m.mu.Lock()
	m.evaluateNetworkLocked(network, now)
	if !m.state.Initialized {
		for _, node := range nodes {
			m.state.Nodes[node.NodeID] = observedNode{
				State: node.State, AddressChangedAt: node.AddressChangedAt, LastSeen: now.Unix(),
			}
		}
		m.state.Initialized = true
		m.state.LastScanAt = now.Unix()
		_ = m.persistStateLocked()
		m.mu.Unlock()
		return
	}

	for _, node := range nodes {
		observed, exists := m.state.Nodes[node.NodeID]
		if !exists {
			observed = observedNode{State: node.State, AddressChangedAt: node.AddressChangedAt}
			if node.AddressChangedAt > m.state.LastScanAt && m.settings.Preferences.IPChanges {
				m.enqueueLocked(notificationFromNode("ip_changed", node, now))
			}
			if node.State == "critical" && node.LastCheck >= m.state.LastScanAt {
				m.enqueueCriticalLocked(node, now, &observed)
			}
		} else {
			if node.AddressChangedAt > observed.AddressChangedAt && m.settings.Preferences.IPChanges {
				m.enqueueLocked(notificationFromNode("ip_changed", node, now))
			}
			if node.State == "critical" {
				if observed.State != "critical" {
					m.enqueueCriticalLocked(node, now, &observed)
				} else if m.settings.Preferences.RepeatHours > 0 &&
					now.Unix()-observed.LastAlertAt >= int64(m.settings.Preferences.RepeatHours)*3600 {
					node.IncidentStartedAt = observed.IncidentStartedAt
					item := notificationFromNode("reminder", node, now)
					m.enqueueLocked(item)
					observed.LastAlertAt = now.Unix()
				}
			} else if node.State == "healthy" &&
				(observed.State == "critical" || observed.State == "verifying" || observed.State == "unstable") &&
				m.settings.Preferences.Recoveries {
				node.IncidentStartedAt = observed.IncidentStartedAt
				kind := "recovered"
				if observed.State == "verifying" {
					kind = "new_ip_verified"
				}
				m.enqueueLocked(notificationFromNode(kind, node, now))
			} else if node.State == "unstable" && observed.State != "unstable" &&
				m.settings.Preferences.Unstable {
				m.enqueueLocked(notificationFromNode("unstable", node, now))
			}
		}
		if node.State != "critical" {
			observed.IncidentStartedAt = 0
		}
		observed.State = node.State
		observed.AddressChangedAt = node.AddressChangedAt
		observed.LastSeen = now.Unix()
		m.state.Nodes[node.NodeID] = observed
	}
	for nodeID, observed := range m.state.Nodes {
		if now.Unix()-observed.LastSeen > 7*24*3600 {
			delete(m.state.Nodes, nodeID)
		}
	}
	m.state.LastScanAt = now.Unix()
	shouldFlush := len(m.state.Pending) > 0 &&
		now.Unix()-m.state.BatchStartedAt >= int64(m.settings.Preferences.BatchWindowSec) &&
		now.Unix() >= m.state.RetryAt
	_ = m.persistStateLocked()
	m.mu.Unlock()

	if shouldFlush {
		m.flush(ctx)
	}
}

func (m *Manager) evaluateNetworkLocked(network checker.NetworkStatus, now time.Time) {
	if !network.Managed {
		return
	}
	if !m.state.NetworkInitialized {
		m.state.NetworkInitialized = true
		m.state.NetworkReady = network.Ready
		if !network.Ready {
			m.state.NetworkDownAt = now.Unix()
		}
		return
	}
	if !network.Ready && m.state.NetworkReady {
		m.state.NetworkReady = false
		m.state.NetworkDownAt = now.Unix()
		return
	}
	if network.Ready && !m.state.NetworkReady {
		downAt := m.state.NetworkDownAt
		m.state.NetworkReady = true
		m.state.NetworkDownAt = 0
		if m.settings.Preferences.NetworkStatus && downAt > 0 && now.Unix()-downAt >= 30 {
			m.enqueueLocked(notification{
				ID: fmt.Sprintf("network_recovered:%d", now.Unix()), Kind: "network_recovered",
				NetworkDownAt: downAt, CreatedAt: now.Unix(),
			})
		}
	}
}

func (m *Manager) enqueueCriticalLocked(node nodeSnapshot, now time.Time, observed *observedNode) {
	if observed.IncidentStartedAt == 0 {
		observed.IncidentStartedAt = node.LastSuccess
		if observed.IncidentStartedAt == 0 {
			observed.IncidentStartedAt = node.LastCheck
		}
		if observed.IncidentStartedAt == 0 {
			observed.IncidentStartedAt = now.Unix()
		}
	}
	node.IncidentStartedAt = observed.IncidentStartedAt
	kind := "critical"
	if node.NewIPFailed {
		if !m.settings.Preferences.NewIPFailures {
			return
		}
		kind = "new_ip_failed"
	} else if !m.settings.Preferences.CriticalFailures {
		return
	}
	m.enqueueLocked(notificationFromNode(kind, node, now))
	observed.LastAlertAt = now.Unix()
}

func (m *Manager) enqueueLocked(item notification) {
	for _, pending := range m.state.Pending {
		if pending.ID == item.ID {
			return
		}
	}
	m.state.Pending = append(m.state.Pending, item)
	if m.state.BatchStartedAt == 0 {
		m.state.BatchStartedAt = item.CreatedAt
	}
}

func (m *Manager) flush(ctx context.Context) {
	m.mu.RLock()
	pending := append([]notification(nil), m.state.Pending...)
	token := m.token
	chatID := m.settings.ChatID
	deliveryMode := m.settings.DeliveryMode
	m.mu.RUnlock()
	if len(pending) == 0 || token == "" || chatID == 0 {
		return
	}

	messages := formatNotifications(pending)
	excluded := make(map[string]bool)
	for _, item := range pending {
		if item.NodeID != "" {
			excluded[item.NodeID] = true
		}
	}
	for _, message := range messages {
		if err := m.tryDelivery(ctx, deliveryMode, "", excluded, func(client *TelegramClient) error {
			return client.SendMessage(ctx, token, chatID, message)
		}); err != nil {
			m.mu.Lock()
			m.state.LastError = err.Error()
			m.state.RetryAt = time.Now().Add(time.Minute).Unix()
			_ = m.persistStateLocked()
			m.mu.Unlock()
			logger.Warn("Could not deliver Telegram alerts: %v", err)
			return
		}
	}

	m.mu.Lock()
	m.state.Pending = nil
	m.state.BatchStartedAt = 0
	m.state.LastDeliveryAt = time.Now().Unix()
	m.state.LastError = ""
	m.state.RetryAt = 0
	_ = m.persistStateLocked()
	m.mu.Unlock()
}

func (m *Manager) collectNodes() []nodeSnapshot {
	type builder struct {
		nodeSnapshot
		names         map[string]bool
		affected      map[string]bool
		subscriptions map[string]bool
		critical      bool
		verify        bool
		unstable      bool
		unclear       bool
		allGood       bool
	}
	groups := make(map[string]*builder)
	for _, proxy := range m.checker.GetProxies() {
		nodeID := proxy.NodeID
		if nodeID == "" {
			nodeID = proxy.GenerateNodeID()
		}
		group := groups[nodeID]
		if group == nil {
			group = &builder{
				nodeSnapshot: nodeSnapshot{NodeID: nodeID, Server: proxy.Server},
				names:        make(map[string]bool), affected: make(map[string]bool),
				subscriptions: make(map[string]bool), allGood: true,
			}
			groups[nodeID] = group
		}
		name := strings.TrimSpace(proxy.Name)
		if name == "" {
			name = proxy.Server
		}
		group.names[name] = true
		if subscription := strings.TrimSpace(proxy.SubName); subscription != "" {
			group.subscriptions[subscription] = true
		}
		if _, _, latency, _, found := m.checker.GetProxyResultDetailsByStableID(proxy.StableID); found {
			if latencyMs := latency.Milliseconds(); latencyMs > group.LatencyMs {
				group.LatencyMs = latencyMs
			}
		}
		monitor, ok := m.checker.GetNodeMonitorByStableID(proxy.StableID)
		if !ok {
			group.allGood = false
			group.unclear = true
			continue
		}
		if monitor.AddressChangedAt > group.AddressChangedAt {
			group.AddressChangedAt = monitor.AddressChangedAt
			group.PreviousAddress = displayAddress(monitor.PreviousAddress, monitor.PreviousResolvedIPs)
			group.CurrentAddress = displayAddress(monitor.CurrentAddress, monitor.ResolvedIPs)
		}
		if monitor.ConsecutiveFailures > group.Failures {
			group.Failures = monitor.ConsecutiveFailures
		}
		if monitor.LastSuccess > group.LastSuccess {
			group.LastSuccess = monitor.LastSuccess
		}
		if monitor.LastCheck > group.LastCheck {
			group.LastCheck = monitor.LastCheck
		}
		if monitor.LastError != "" {
			group.LastError = monitor.LastError
		}
		switch monitor.State {
		case checker.NodeNeedsReplacement:
			group.critical = true
			group.allGood = false
			group.affected[name] = true
		case checker.NodeNewIPFailed:
			group.critical = true
			group.NewIPFailed = true
			group.allGood = false
			group.affected[name] = true
		case checker.NodeIPChanged, checker.NodeVerifyingNewIP:
			group.verify = true
			group.allGood = false
			group.affected[name] = true
		case checker.NodeUnstable:
			group.unstable = true
			group.allGood = false
			group.affected[name] = true
		case checker.NodeHealthy, checker.NodeFixed:
		default:
			group.allGood = false
			group.unclear = true
		}
	}

	result := make([]nodeSnapshot, 0, len(groups))
	for _, group := range groups {
		group.Names = mapKeys(group.names)
		group.Affected = mapKeys(group.affected)
		group.Subscriptions = mapKeys(group.subscriptions)
		switch {
		case group.critical:
			group.State = "critical"
		case group.verify:
			group.State = "verifying"
		case group.unstable && !group.unclear:
			group.State = "unstable"
		case group.allGood:
			group.State = "healthy"
		default:
			group.State = "unclear"
		}
		if group.CurrentAddress == "" {
			group.CurrentAddress = group.Server
		}
		result = append(result, group.nodeSnapshot)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Server < result[j].Server })
	return result
}

func notificationFromNode(kind string, node nodeSnapshot, now time.Time) notification {
	return notification{
		ID:   fmt.Sprintf("%s:%s:%d", kind, node.NodeID, maxInt64(node.AddressChangedAt, now.Unix())),
		Kind: kind, NodeID: node.NodeID, Server: node.Server,
		Names: node.Names, Affected: node.Affected, Subscriptions: node.Subscriptions,
		PreviousAddress: node.PreviousAddress, CurrentAddress: node.CurrentAddress,
		Failures: node.Failures, LastSuccess: node.LastSuccess, LastError: node.LastError,
		LatencyMs: node.LatencyMs, IncidentStartedAt: node.IncidentStartedAt,
		CreatedAt: now.Unix(), AddressChangedAt: node.AddressChangedAt,
	}
}

func formatNotifications(items []notification) []string {
	if len(items) == 0 {
		return nil
	}
	if len(items) == 1 {
		return []string{formatNotification(items[0])}
	}

	type digestGroup struct {
		title string
		kinds map[string]bool
	}
	groups := []digestGroup{
		{title: "🔴 <b>Требуют внимания</b>", kinds: map[string]bool{"critical": true, "new_ip_failed": true, "reminder": true}},
		{title: "🟠 <b>Работают нестабильно</b>", kinds: map[string]bool{"unstable": true}},
		{title: "🔵 <b>Изменился IP</b>", kinds: map[string]bool{"ip_changed": true}},
		{title: "🟢 <b>Работают снова</b>", kinds: map[string]bool{"recovered": true, "new_ip_verified": true}},
		{title: "📶 <b>Соединение с iPhone</b>", kinds: map[string]bool{"network_recovered": true}},
	}

	used := make(map[int]bool, len(items))
	blocks := make([]string, 0, len(groups))
	for _, group := range groups {
		groupItems := make([]notification, 0)
		for index, item := range items {
			if group.kinds[item.Kind] {
				used[index] = true
				groupItems = append(groupItems, item)
			}
		}
		blocks = append(blocks, formatDigestBlocks(group.title, groupItems)...)
	}
	unknown := make([]notification, 0)
	for index, item := range items {
		if !used[index] {
			unknown = append(unknown, item)
		}
	}
	blocks = append(blocks, formatDigestBlocks("ℹ️ <b>События мониторинга</b>", unknown)...)

	return packTelegramMessages(blocks)
}

func formatNotification(item notification) string {
	if item.Kind == "network_recovered" {
		duration := humanAlertDuration(item.CreatedAt - item.NetworkDownAt)
		return fmt.Sprintf("📶 <b>Соединение с iPhone восстановлено</b>\n\nМобильный маршрут был недоступен %s.\n\n<i>Проверки нод продолжены.</i>", duration)
	}
	title := map[string]string{
		"critical":        "🔴 <b>Нода не проходит проверку</b>",
		"new_ip_failed":   "🔴 <b>Новый IP не заработал</b>",
		"ip_changed":      "🔵 <b>IP изменён</b>",
		"recovered":       "🟢 <b>Нода снова работает</b>",
		"new_ip_verified": "🟢 <b>Новый IP работает</b>",
		"unstable":        "🟠 <b>Нода работает нестабильно</b>",
		"reminder":        "🔴 <b>Нода всё ещё недоступна</b>",
	}[item.Kind]
	if title == "" {
		title = "ℹ️ <b>Событие Xray Checker</b>"
	}
	lines := []string{title, ""}
	if item.Kind == "ip_changed" && (item.PreviousAddress != "" || item.CurrentAddress != "") {
		lines = append(lines, alertCode(emptyDash(item.PreviousAddress))+" → "+alertCode(emptyDash(item.CurrentAddress)))
	} else {
		address := item.Server
		if (item.Kind == "new_ip_failed" || item.Kind == "new_ip_verified") && item.CurrentAddress != "" {
			address = item.CurrentAddress
		}
		lines = append(lines, alertCode(displayServer(address)))
	}
	if context := alertContext(item); context != "" {
		lines = append(lines, context)
	}
	lines = append(lines, alertHosts(item)...)

	switch item.Kind {
	case "critical", "new_ip_failed", "reminder":
		facts := make([]string, 0, 2)
		if item.Failures > 0 {
			facts = append(facts, failurePhrase(item.Failures)+" подряд")
		}
		if item.IncidentStartedAt > 0 && item.CreatedAt >= item.IncidentStartedAt {
			facts = append(facts, "около "+humanAlertDuration(item.CreatedAt-item.IncidentStartedAt))
		}
		if len(facts) > 0 {
			lines = append(lines, "", strings.Join(facts, " · "))
		}
		if item.LastSuccess > 0 {
			lines = append(lines, "Последний успех: "+formatAlertTime(item.LastSuccess))
		}
		if reason := humanAlertReason(item.LastError); reason != "" {
			lines = append(lines, "Причина: "+reason)
		}
	case "ip_changed":
		lines = append(lines, "", "Проверяем новый адрес…")
	case "recovered":
		if item.IncidentStartedAt > 0 && item.CreatedAt >= item.IncidentStartedAt {
			lines = append(lines, "", "Не работала около "+humanAlertDuration(item.CreatedAt-item.IncidentStartedAt))
		}
		if item.LatencyMs > 0 {
			lines = append(lines, fmt.Sprintf("Apple URL Test пройден · %d мс", item.LatencyMs))
		}
	case "new_ip_verified":
		lines = append(lines, "")
		if item.LatencyMs > 0 {
			lines = append(lines, fmt.Sprintf("Apple URL Test пройден · %d мс", item.LatencyMs))
		} else {
			lines = append(lines, "Apple URL Test пройден")
		}
	case "unstable":
		lines = append(lines, "", "Apple URL Test проходит не во всех попытках.")
		if item.LatencyMs > 0 {
			lines = append(lines, fmt.Sprintf("Последняя задержка: %d мс", item.LatencyMs))
		}
	}
	return strings.Join(lines, "\n")
}

func formatDigestBlocks(title string, items []notification) []string {
	if len(items) == 0 {
		return nil
	}
	heading := fmt.Sprintf("%s · %d", title, len(items))
	blocks := make([]string, 0, 1)
	current := heading
	for _, item := range items {
		line := formatDigestLine(item)
		candidate := current + "\n" + line
		if len([]rune(candidate)) > 3200 && current != heading {
			blocks = append(blocks, current)
			current = heading + "\n" + line
		} else {
			current = candidate
		}
	}
	blocks = append(blocks, current)
	return blocks
}

func formatDigestLine(item notification) string {
	if item.Kind == "network_recovered" {
		return "• маршрут восстановлен за " + humanAlertDuration(item.CreatedAt-item.NetworkDownAt)
	}
	context := compactAlertContext(item)
	if item.Kind == "ip_changed" {
		line := "• " + alertCode(emptyDash(item.PreviousAddress)) + " → " + alertCode(emptyDash(item.CurrentAddress))
		if context != "" {
			line += " — " + context
		}
		return line
	}
	address := item.Server
	if (item.Kind == "new_ip_failed" || item.Kind == "new_ip_verified") && item.CurrentAddress != "" {
		address = item.CurrentAddress
	}
	line := "• " + alertCode(displayServer(address))
	if context != "" {
		line += " — " + context
	}
	switch item.Kind {
	case "critical", "reminder":
		if item.Failures > 0 {
			line += " · " + failurePhrase(item.Failures)
		}
	case "new_ip_failed":
		line += " · новый IP не прошёл проверку"
	case "new_ip_verified":
		line += " · новый IP проверен"
	case "recovered", "unstable":
		if item.LatencyMs > 0 {
			line += fmt.Sprintf(" · %d мс", item.LatencyMs)
		}
	}
	return line
}

func packTelegramMessages(blocks []string) []string {
	messages := make([]string, 0, 1)
	current := ""
	for _, block := range blocks {
		candidate := block
		if current != "" {
			candidate = current + "\n\n" + block
		}
		if len([]rune(candidate)) > 3800 && current != "" {
			messages = append(messages, current)
			current = block
		} else {
			current = candidate
		}
	}
	if current != "" {
		messages = append(messages, current)
	}
	return messages
}

func (m *Manager) load() error {
	if data, err := os.ReadFile(m.settingsFile); err == nil {
		if err := json.Unmarshal(data, &m.settings); err != nil {
			return fmt.Errorf("decode Telegram settings: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read Telegram settings: %w", err)
	}
	if data, err := os.ReadFile(m.tokenFile); err == nil {
		m.token = strings.TrimSpace(string(data))
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read Telegram token: %w", err)
	}
	if data, err := os.ReadFile(m.proxyFile); err == nil {
		m.customProxy = strings.TrimSpace(string(data))
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read Telegram proxy: %w", err)
	}
	if data, err := os.ReadFile(m.stateFile); err == nil {
		if err := json.Unmarshal(data, &m.state); err != nil {
			return fmt.Errorf("decode Telegram alert state: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read Telegram alert state: %w", err)
	}
	if m.state.Nodes == nil {
		m.state.Nodes = make(map[string]observedNode)
	}
	if m.state.RouteFailures == nil {
		m.state.RouteFailures = make(map[string]int64)
	}
	m.state.Version = stateVersion
	return nil
}

func (m *Manager) normalizePreferences() {
	normalizePreferences(&m.settings.Preferences)
}

func normalizePreferences(preferences *Preferences) {
	if preferences.BatchWindowSec <= 0 {
		preferences.BatchWindowSec = defaultBatchSec
	}
	if preferences.BatchWindowSec > 300 {
		preferences.BatchWindowSec = 300
	}
	if preferences.RepeatHours < 0 {
		preferences.RepeatHours = 0
	}
	if preferences.RepeatHours > 168 {
		preferences.RepeatHours = 168
	}
}

func (m *Manager) persistStateLocked() error {
	if m.stateFile == "" {
		return nil
	}
	return writeJSONAtomic(m.stateFile, m.state)
}

func (m *Manager) recordDelivery(deliveryErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if deliveryErr != nil {
		m.state.LastError = deliveryErr.Error()
		m.state.RetryAt = time.Now().Add(time.Minute).Unix()
	} else {
		m.state.LastError = ""
		m.state.RetryAt = 0
		m.state.LastDeliveryAt = time.Now().Unix()
	}
	_ = m.persistStateLocked()
}

func (m *Manager) statusLocked() PublicStatus {
	configured := m.token != "" && m.settings.ChatID != 0
	state := "not_configured"
	if configured && !m.settings.Enabled {
		state = "disabled"
	} else if configured && m.state.LastError != "" {
		state = "error"
	} else if configured {
		state = "connected"
	}
	return PublicStatus{
		Available: m.settingsFile != "", Configured: configured, Enabled: m.settings.Enabled,
		Connected:            configured && m.settings.Enabled && m.state.LastError == "",
		ManagedByEnvironment: m.envToken != "", State: state,
		ProxyManagedByEnv: m.envProxy != "",
		BotUsername:       m.settings.BotUsername, Recipient: m.settings.ChatLabel,
		LastDeliveryAt: m.state.LastDeliveryAt, LastError: m.state.LastError,
		DeliveryMode:          normalizeDeliveryMode(m.settings.DeliveryMode),
		CustomProxyConfigured: m.customProxy != "",
		LastRoute:             m.state.LastRoute,
		AvailableRoutes:       m.healthyRouteCount(),
		Preferences:           m.settings.Preferences,
	}
}

func writeJSONAtomic(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'), 0o600)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(temporary, mode); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func mapKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func displayAddress(address string, resolved []string) string {
	if len(resolved) > 0 {
		return strings.Join(resolved, ", ")
	}
	return displayServer(address)
}

func displayServer(address string) string {
	address = strings.TrimSpace(address)
	if host, _, err := net.SplitHostPort(address); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(address, "[]")
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return displayServer(value)
}

func trimMessage(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit-1]) + "…"
}

func alertContext(item notification) string {
	parts := make([]string, 0, 2)
	if subscriptions := escapedList(item.Subscriptions, 2); subscriptions != "" {
		parts = append(parts, subscriptions)
	}
	names := notificationNames(item)
	if len(names) == 1 {
		parts = append(parts, escapeHTML(trimMessage(names[0], 70)))
	}
	return strings.Join(parts, " · ")
}

func compactAlertContext(item notification) string {
	parts := make([]string, 0, 2)
	names := notificationNames(item)
	if len(names) > 0 {
		name := escapeHTML(trimMessage(names[0], 48))
		if len(names) > 1 {
			name += fmt.Sprintf(" +%d", len(names)-1)
		}
		parts = append(parts, name)
	}
	if subscriptions := escapedList(item.Subscriptions, 1); subscriptions != "" {
		parts = append(parts, subscriptions)
	}
	return strings.Join(parts, " · ")
}

func alertHosts(item notification) []string {
	names := notificationNames(item)
	if len(names) <= 1 {
		return nil
	}
	lines := []string{"", fmt.Sprintf("Связанные хосты: %d", len(names))}
	limit := len(names)
	if limit > 3 {
		limit = 3
	}
	for _, name := range names[:limit] {
		lines = append(lines, "• "+escapeHTML(trimMessage(name, 80)))
	}
	if len(names) > limit {
		lines = append(lines, fmt.Sprintf("• + ещё %d", len(names)-limit))
	}
	return lines
}

func notificationNames(item notification) []string {
	if len(item.Affected) > 0 {
		return item.Affected
	}
	return item.Names
}

func escapedList(values []string, limit int) string {
	if len(values) == 0 || limit <= 0 {
		return ""
	}
	visible := len(values)
	if visible > limit {
		visible = limit
	}
	parts := make([]string, 0, visible+1)
	for _, value := range values[:visible] {
		parts = append(parts, escapeHTML(trimMessage(value, 40)))
	}
	if len(values) > visible {
		parts = append(parts, fmt.Sprintf("+%d", len(values)-visible))
	}
	return strings.Join(parts, " · ")
}

func alertCode(value string) string {
	return "<code>" + escapeHTML(strings.TrimSpace(value)) + "</code>"
}

func escapeHTML(value string) string {
	return html.EscapeString(strings.TrimSpace(value))
}

func formatAlertTime(timestamp int64) string {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		location = time.FixedZone("MSK", 3*60*60)
	}
	return time.Unix(timestamp, 0).In(location).Format("15:04")
}

func humanAlertDuration(seconds int64) string {
	if seconds < 60 {
		return "меньше минуты"
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%d мин", minutes)
	}
	hours := minutes / 60
	remainingMinutes := minutes % 60
	if hours < 24 {
		if remainingMinutes == 0 {
			return fmt.Sprintf("%d ч", hours)
		}
		return fmt.Sprintf("%d ч %d мин", hours, remainingMinutes)
	}
	days := hours / 24
	remainingHours := hours % 24
	if remainingHours == 0 {
		return fmt.Sprintf("%d дн", days)
	}
	return fmt.Sprintf("%d дн %d ч", days, remainingHours)
}

func failurePhrase(count int) string {
	word := "ошибок"
	lastTwo := count % 100
	last := count % 10
	if lastTwo < 11 || lastTwo > 14 {
		switch last {
		case 1:
			word = "ошибка"
		case 2, 3, 4:
			word = "ошибки"
		}
	}
	return fmt.Sprintf("%d %s", count, word)
}

func humanAlertReason(value string) string {
	value = trimMessage(value, 180)
	lower := strings.ToLower(value)
	switch {
	case value == "":
		return ""
	case strings.Contains(lower, "url test:"):
		value = strings.ReplaceAll(value, "URL test", "Apple URL Test")
		value = strings.ReplaceAll(value, "successful", "успешно")
		return escapeHTML(value)
	case strings.Contains(lower, "handshake"):
		return "ошибка TLS/Reality handshake"
	case strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "timeout"):
		return "таймаут соединения"
	case strings.Contains(lower, "connection refused"):
		return "порт отклонил соединение"
	case strings.Contains(lower, "connection reset"):
		return "соединение сброшено"
	case strings.Contains(lower, "no route"):
		return "нет маршрута до IP"
	case strings.Contains(lower, "network unavailable"), strings.Contains(lower, "mobile network unavailable"):
		return "сеть iPhone недоступна"
	case lower == "eof" || strings.HasSuffix(lower, ": eof"):
		return "соединение закрыто сервером"
	default:
		return escapeHTML(value)
	}
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

// Ensure the compiler keeps the topology dependency explicit: alert grouping is
// intentionally based on the same ProxyConfig identifiers used by the dashboard.
var _ = (*models.ProxyConfig)(nil)
