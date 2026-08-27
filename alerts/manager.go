package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	NodeID           string
	Server           string
	Names            []string
	Affected         []string
	State            string
	AddressChangedAt int64
	PreviousAddress  string
	CurrentAddress   string
	Failures         int
	LastSuccess      int64
	LastCheck        int64
	LastError        string
	NewIPFailed      bool
}

type observedNode struct {
	State            string `json:"state"`
	AddressChangedAt int64  `json:"addressChangedAt,omitempty"`
	LastAlertAt      int64  `json:"lastAlertAt,omitempty"`
	LastSeen         int64  `json:"lastSeen"`
}

type notification struct {
	ID               string   `json:"id"`
	Kind             string   `json:"kind"`
	NodeID           string   `json:"nodeId,omitempty"`
	Server           string   `json:"server,omitempty"`
	Names            []string `json:"names,omitempty"`
	Affected         []string `json:"affected,omitempty"`
	PreviousAddress  string   `json:"previousAddress,omitempty"`
	CurrentAddress   string   `json:"currentAddress,omitempty"`
	Failures         int      `json:"failures,omitempty"`
	LastSuccess      int64    `json:"lastSuccess,omitempty"`
	LastError        string   `json:"lastError,omitempty"`
	NetworkDownAt    int64    `json:"networkDownAt,omitempty"`
	CreatedAt        int64    `json:"createdAt"`
	AddressChangedAt int64    `json:"addressChangedAt,omitempty"`
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
	networkLabel := "available"
	if network.Managed {
		if network.Ready {
			networkLabel = "iPhone connected"
			if network.PublicIP != "" {
				networkLabel += " · " + network.PublicIP
			}
		} else {
			networkLabel = "iPhone connection unavailable"
		}
	}
	message := fmt.Sprintf("✅ Xray Checker connected\n\nTelegram alerts are configured correctly.\nNodes: %d\nRequire attention: %d\nProbe: %s",
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
					item := notificationFromNode("reminder", node, now)
					m.enqueueLocked(item)
					observed.LastAlertAt = now.Unix()
				}
			} else if node.State == "healthy" &&
				(observed.State == "critical" || observed.State == "verifying" || observed.State == "unstable") &&
				m.settings.Preferences.Recoveries {
				m.enqueueLocked(notificationFromNode("recovered", node, now))
			} else if node.State == "unstable" && observed.State != "unstable" &&
				m.settings.Preferences.Unstable {
				m.enqueueLocked(notificationFromNode("unstable", node, now))
			}
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
		names    map[string]bool
		affected map[string]bool
		critical bool
		verify   bool
		unstable bool
		unclear  bool
		allGood  bool
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
				names:        make(map[string]bool), affected: make(map[string]bool), allGood: true,
			}
			groups[nodeID] = group
		}
		name := strings.TrimSpace(proxy.Name)
		if name == "" {
			name = proxy.Server
		}
		group.names[name] = true
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
		Names: node.Names, Affected: node.Affected,
		PreviousAddress: node.PreviousAddress, CurrentAddress: node.CurrentAddress,
		Failures: node.Failures, LastSuccess: node.LastSuccess, LastError: node.LastError,
		CreatedAt: now.Unix(), AddressChangedAt: node.AddressChangedAt,
	}
}

func formatNotifications(items []notification) []string {
	sections := make([]string, 0, len(items))
	for _, item := range items {
		sections = append(sections, formatNotification(item))
	}
	messages := make([]string, 0, 1)
	current := ""
	for _, section := range sections {
		candidate := section
		if current != "" {
			candidate = current + "\n\n──────────\n\n" + section
		}
		if len([]rune(candidate)) > 3800 && current != "" {
			messages = append(messages, current)
			current = section
		} else {
			current = candidate
		}
	}
	if current != "" {
		messages = append(messages, current)
	}
	return messages
}

func formatNotification(item notification) string {
	if item.Kind == "network_recovered" {
		duration := time.Duration(item.CreatedAt-item.NetworkDownAt) * time.Second
		return fmt.Sprintf("🟢 iPhone connection restored\n\nThe mobile probe route was unavailable for %s. Node checks have resumed.",
			duration.Round(time.Second))
	}
	title := map[string]string{
		"critical":      "🔴 Node requires attention",
		"new_ip_failed": "🔴 New IP verification failed",
		"ip_changed":    "🟡 Node IP changed",
		"recovered":     "🟢 Node recovered",
		"unstable":      "🟠 Node is unstable",
		"reminder":      "🔴 Node is still unavailable",
	}[item.Kind]
	if title == "" {
		title = "Xray Checker alert"
	}
	lines := []string{title, "", "IP: " + displayServer(item.Server)}
	if item.Kind == "ip_changed" && (item.PreviousAddress != "" || item.CurrentAddress != "") {
		lines = append(lines, "Change: "+emptyDash(item.PreviousAddress)+" → "+emptyDash(item.CurrentAddress))
	}
	if item.Failures > 0 && (item.Kind == "critical" || item.Kind == "new_ip_failed" || item.Kind == "reminder") {
		lines = append(lines, fmt.Sprintf("Failed checks: %d", item.Failures))
	}
	if item.LastSuccess > 0 && (item.Kind == "critical" || item.Kind == "new_ip_failed" || item.Kind == "reminder") {
		lines = append(lines, "Last success: "+time.Unix(item.LastSuccess, 0).Format("02 Jan 15:04"))
	}
	names := item.Names
	if len(item.Affected) > 0 {
		names = item.Affected
	}
	if len(names) > 0 {
		lines = append(lines, "", "Related hosts:")
		limit := len(names)
		if limit > 10 {
			limit = 10
		}
		for _, name := range names[:limit] {
			lines = append(lines, "• "+name)
		}
		if len(names) > limit {
			lines = append(lines, fmt.Sprintf("• …and %d more", len(names)-limit))
		}
	}
	if item.LastError != "" && item.Kind != "ip_changed" && item.Kind != "recovered" {
		lines = append(lines, "", "Check: Apple URL Test via Proxy", "Reason: "+trimMessage(item.LastError, 180))
	}
	return strings.Join(lines, "\n")
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

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

// Ensure the compiler keeps the topology dependency explicit: alert grouping is
// intentionally based on the same ProxyConfig identifiers used by the dashboard.
var _ = (*models.ProxyConfig)(nil)
