package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"xray-checker/alerts"
)

type telegramTokenRequest struct {
	Token          string `json:"token"`
	DeliveryMode   string `json:"deliveryMode"`
	CustomProxyURL string `json:"customProxyUrl"`
}

func TelegramAlertsHandler(manager *alerts.Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !telegramRequestAllowed(r) {
			writeError(w, "Telegram settings are available only from the local dashboard", http.StatusForbidden)
			return
		}

		path := strings.TrimSuffix(r.URL.Path, "/")
		const base = "/api/v1/alerts/telegram"
		switch {
		case path == base && r.Method == http.MethodGet:
			writeJSON(w, manager.Status())
		case path == base && r.Method == http.MethodPut:
			var request alerts.SaveSettingsRequest
			if !decodeAlertRequest(w, r, &request) {
				return
			}
			ctx, cancel := contextWithAlertTimeout(r, 35*time.Second)
			defer cancel()
			status, err := manager.Save(ctx, request)
			if err != nil {
				writeError(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, status)
		case path == base && r.Method == http.MethodDelete:
			if err := manager.Disconnect(); err != nil {
				writeError(w, err.Error(), http.StatusConflict)
				return
			}
			writeJSON(w, manager.Status())
		case path == base+"/verify" && r.Method == http.MethodPost:
			var request telegramTokenRequest
			if !decodeAlertRequest(w, r, &request) {
				return
			}
			ctx, cancel := contextWithAlertTimeout(r, 35*time.Second)
			defer cancel()
			bot, err := manager.VerifyToken(ctx, request.Token, request.DeliveryMode, request.CustomProxyURL)
			if err != nil {
				writeError(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, bot)
		case path == base+"/discover-chat" && r.Method == http.MethodPost:
			var request telegramTokenRequest
			if !decodeAlertRequest(w, r, &request) {
				return
			}
			ctx, cancel := contextWithAlertTimeout(r, 35*time.Second)
			defer cancel()
			chats, err := manager.DiscoverChats(ctx, request.Token, request.DeliveryMode, request.CustomProxyURL)
			if err != nil {
				writeError(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, chats)
		case path == base+"/test" && r.Method == http.MethodPost:
			ctx, cancel := contextWithAlertTimeout(r, 35*time.Second)
			defer cancel()
			if err := manager.SendTest(ctx); err != nil {
				writeError(w, err.Error(), http.StatusBadGateway)
				return
			}
			writeJSON(w, manager.Status())
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE, POST")
			writeError(w, "Unsupported Telegram alerts operation", http.StatusMethodNotAllowed)
		}
	})
}

func decodeAlertRequest(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		writeError(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, "Invalid JSON request", http.StatusBadRequest)
		return false
	}
	return true
}

func telegramRequestAllowed(r *http.Request) bool {
	host := r.Host
	if parsedHost, _, err := net.SplitHostPort(r.Host); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return false
		}
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func contextWithAlertTimeout(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	return ctx, cancel
}
