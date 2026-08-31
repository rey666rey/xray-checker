package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"xray-checker/checker"
)

const accessCheckRequestLimit = 16 * 1024

// APIAccessChecksHandler starts one direct-vs-VPN access check, returns its
// current state, and lists recent checks. It is registered only on the private
// API mux by main.
func APIAccessChecksHandler(proxyChecker *checker.ProxyChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/access-checks" {
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, proxyChecker.GetAccessCheckHistory())
			case http.MethodPost:
				var request checker.AccessCheckRequest
				decoder := json.NewDecoder(io.LimitReader(r.Body, accessCheckRequestLimit))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&request); err != nil {
					writeError(w, "Invalid access-check request: "+err.Error(), http.StatusBadRequest)
					return
				}
				if err := ensureJSONEOF(decoder); err != nil {
					writeError(w, "Invalid access-check request: "+err.Error(), http.StatusBadRequest)
					return
				}
				run, err := proxyChecker.StartAccessCheck(request)
				if err != nil {
					status := http.StatusBadRequest
					if errors.Is(err, checker.ErrAccessCheckBusy) {
						status = http.StatusConflict
					}
					writeError(w, err.Error(), status)
					return
				}
				writeJSON(w, run)
			default:
				writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}

		prefix := "/api/v1/access-checks/"
		if !strings.HasPrefix(r.URL.Path, prefix) || r.Method != http.MethodGet {
			writeError(w, "Invalid access-check action", http.StatusBadRequest)
			return
		}
		runID := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
		if runID == "" || strings.Contains(runID, "/") {
			writeError(w, "Access-check ID is required", http.StatusBadRequest)
			return
		}
		run, ok := proxyChecker.GetAccessCheck(runID)
		if !ok {
			writeError(w, "Access check not found", http.StatusNotFound)
			return
		}
		writeJSON(w, run)
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain a single JSON object")
		}
		return err
	}
	return nil
}
