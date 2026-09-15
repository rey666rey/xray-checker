package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const networkRecoveryStaleAfter = 5 * time.Minute

type NetworkRecoveryStatus struct {
	Enabled     bool   `json:"enabled"`
	Active      bool   `json:"active"`
	RequestID   string `json:"requestId,omitempty"`
	State       string `json:"state"`
	Trigger     string `json:"trigger,omitempty"`
	Message     string `json:"message,omitempty"`
	RequestedAt int64  `json:"requestedAt,omitempty"`
	StartedAt   int64  `json:"startedAt,omitempty"`
	CompletedAt int64  `json:"completedAt,omitempty"`
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
}

type networkRecoveryRequest struct {
	RequestID   string `json:"requestId"`
	Trigger     string `json:"trigger"`
	RequestedAt int64  `json:"requestedAt"`
}

// APINetworkRecoveryHandler bridges the dashboard to the macOS launch agent
// through a host-mounted request file. The HTTP response is completed before
// the supervisor stops Colima and temporarily takes the dashboard offline.
func APINetworkRecoveryHandler(requestFile, statusFile string) http.HandlerFunc {
	requestFile = strings.TrimSpace(requestFile)
	statusFile = strings.TrimSpace(statusFile)
	enabled := requestFile != "" && statusFile != ""
	var mu sync.Mutex

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, readNetworkRecoveryStatus(statusFile, enabled))
		case http.MethodPost:
			if !enabled {
				writeError(w, "Manual iPhone reconnect is not configured", http.StatusServiceUnavailable)
				return
			}
			if !validRecoveryActionRequest(r) {
				writeError(w, "Reconnect request must come from this dashboard", http.StatusForbidden)
				return
			}

			mu.Lock()
			defer mu.Unlock()
			current := readNetworkRecoveryStatus(statusFile, enabled)
			if current.Active || fileExists(requestFile) {
				writeError(w, "iPhone reconnect is already queued or running", http.StatusConflict)
				return
			}

			now := time.Now()
			request := networkRecoveryRequest{
				RequestID: fmt.Sprintf("manual-%d", now.UnixNano()),
				Trigger:   "manual", RequestedAt: now.Unix(),
			}
			status := NetworkRecoveryStatus{
				Enabled: true, Active: true, RequestID: request.RequestID,
				State: "queued", Trigger: request.Trigger,
				Message:     "Waiting for the macOS recovery supervisor",
				RequestedAt: request.RequestedAt, UpdatedAt: request.RequestedAt,
			}
			if err := writeJSONFileAtomic(statusFile, status, 0o664); err != nil {
				writeError(w, "Could not queue iPhone reconnect: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if err := writeJSONFileAtomic(requestFile, request, 0o664); err != nil {
				status.Active = false
				status.State = "failed"
				status.Message = "Could not write the recovery request"
				status.CompletedAt = time.Now().Unix()
				status.UpdatedAt = status.CompletedAt
				_ = writeJSONFileAtomic(statusFile, status, 0o664)
				writeError(w, "Could not queue iPhone reconnect: "+err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(APIResponse{Success: true, Data: status})
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func validRecoveryActionRequest(r *http.Request) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") ||
		r.Header.Get("X-Xray-Action") != "reconnect-iphone" {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, r.Host)
}

func readNetworkRecoveryStatus(path string, enabled bool) NetworkRecoveryStatus {
	status := NetworkRecoveryStatus{Enabled: enabled, State: "idle"}
	if !enabled {
		status.Message = "Manual iPhone reconnect is disabled"
		return status
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return status
	}
	if err != nil || json.Unmarshal(data, &status) != nil {
		return NetworkRecoveryStatus{
			Enabled: false, State: "unavailable",
			Message: "Recovery supervisor status is unavailable",
		}
	}
	status.Enabled = true
	status.Active = status.State == "queued" || status.State == "running"
	if status.Active && status.UpdatedAt > 0 && time.Since(time.Unix(status.UpdatedAt, 0)) > networkRecoveryStaleAfter {
		status.Active = false
		status.State = "failed"
		status.Message = "The previous reconnect did not report completion"
	}
	return status
}

func writeJSONFileAtomic(path string, value any, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".recovery-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
