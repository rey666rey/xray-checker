package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"xray-checker/networksource"
)

// Serialize legacy reconnect and source-switch admissions within this server.
var networkControlMu sync.Mutex

func APINetworkSourcesHandler(dir, recoveryRequest, recoveryStatus string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if dir == "" {
			if r.Method == http.MethodPost {
				writeError(w, "Network source supervisor is not configured", http.StatusServiceUnavailable)
			} else {
				writeJSON(w, networksource.Snapshot{Message: "Network source supervisor is not configured", Job: networksource.Job{State: "idle"}})
			}
			return
		}
		networkControlMu.Lock()
		defer networkControlMu.Unlock()
		snapshot, err := networksource.Read(dir)
		if r.Method == http.MethodGet {
			if err != nil {
				writeJSON(w, networksource.Snapshot{Message: "Waiting for the host network source supervisor", Job: networksource.Job{State: "idle"}})
				return
			}
			if !snapshot.Fresh(time.Now()) {
				snapshot.Enabled = false
				snapshot.Message = "Host network source supervisor is not responding"
			}
			for _, name := range []string{networksource.RequestName, networksource.ProcessingName} {
				data, readErr := os.ReadFile(filepath.Join(dir, name))
				if readErr != nil || len(data) > 2048 {
					continue
				}
				var pending networksource.Request
				if json.Unmarshal(data, &pending) == nil && pending.ID != "" && pending.ID != snapshot.Job.ID {
					snapshot.Job = networksource.Job{ID: pending.ID, Action: pending.Action, SourceID: pending.SourceID, State: "queued", Message: "Waiting for the host supervisor", UpdatedAt: pending.RequestedAt}
				}
			}
			writeJSON(w, snapshot)
			return
		}
		if !validNetworkActionRequest(r, "network-source") {
			writeError(w, "Source action must come from this dashboard", http.StatusForbidden)
			return
		}
		if err != nil || !snapshot.Fresh(time.Now()) {
			writeError(w, "Network source supervisor is unavailable", http.StatusServiceUnavailable)
			return
		}
		if snapshot.Job.Active() || networksource.Exists(dir, networksource.RequestName) || networksource.Exists(dir, networksource.ProcessingName) || fileExists(recoveryRequest) || readNetworkRecoveryStatus(recoveryStatus, recoveryStatus != "").Active {
			writeError(w, "Another network action is in progress", http.StatusConflict)
			return
		}
		var input struct {
			Action        string `json:"action"`
			SourceID      string `json:"sourceId"`
			Interface     string `json:"interface"`
			ExpectedEpoch string `json:"expectedEpoch"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeError(w, "Invalid source action", http.StatusBadRequest)
			return
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			writeError(w, "Expected one JSON object", http.StatusBadRequest)
			return
		}
		if (input.Action != "configure" && input.Action != "validate" && input.Action != "activate") || (input.SourceID != "iphone" && input.SourceID != "router") || !networksource.InterfaceName.MatchString(input.Interface) {
			writeError(w, "Unknown source, action or interface", http.StatusBadRequest)
			return
		}
		if input.ExpectedEpoch != snapshot.Epoch {
			writeError(w, "Source context changed; refresh before retrying", http.StatusConflict)
			return
		}
		known := false
		for _, item := range snapshot.Interfaces {
			if item.Name == input.Interface {
				known = true
			}
		}
		if !known {
			writeError(w, "Interface is not in the host inventory", http.StatusBadRequest)
			return
		}
		if input.Action == "configure" && input.SourceID == snapshot.ActiveSourceID {
			writeError(w, "Use activation to change the active interface", http.StatusConflict)
			return
		}
		request := networksource.Request{ID: fmt.Sprintf("source-%d", time.Now().UnixNano()), Action: input.Action, SourceID: input.SourceID, Interface: input.Interface, ExpectedEpoch: input.ExpectedEpoch, RequestedAt: time.Now().Unix()}
		// Publish a complete request atomically without replacing an existing job.
		path := filepath.Join(dir, networksource.RequestName)
		file, err := os.CreateTemp(dir, ".request-")
		if err != nil {
			writeError(w, "Could not queue source action", http.StatusConflict)
			return
		}
		defer os.Remove(file.Name())
		data, _ := json.Marshal(request)
		_, err = file.Write(append(data, '\n'))
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			writeError(w, "Could not write source action", http.StatusInternalServerError)
			return
		}
		if err = os.Link(file.Name(), path); err != nil {
			writeError(w, "Another source request is pending", http.StatusConflict)
			return
		}
		snapshot.Job = networksource.Job{ID: request.ID, Action: request.Action, SourceID: request.SourceID, State: "queued", Message: "Waiting for the host supervisor", UpdatedAt: request.RequestedAt}
		// The durable request is authoritative; only the host publishes status.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(APIResponse{Success: true, Data: snapshot})
	}
}

func validNetworkActionRequest(r *http.Request, action string) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") || r.Header.Get("X-Xray-Action") != action {
		return false
	}
	return validSameOrigin(r)
}

func validSameOrigin(r *http.Request) bool {
	// Reuse the legacy origin validation without relaxing its custom-header gate.
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	clone.Header.Set("X-Xray-Action", "reconnect-iphone")
	return validRecoveryActionRequest(clone)
}
