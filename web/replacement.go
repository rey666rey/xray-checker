package web

import (
	"context"
	"net/http"
	"strings"
)

const (
	ReplacementVerified        = "verified"
	ReplacementCurrentVerified = "current_verified"
	ReplacementUnstable        = "unstable"
	ReplacementFailed          = "failed"
	ReplacementNotReceived     = "not_received"
	ReplacementAmbiguous       = "ambiguous"
)

type ReplacementVerification struct {
	State               string `json:"state"`
	PreviousStableID    string `json:"previousStableId,omitempty"`
	StableID            string `json:"stableId,omitempty"`
	PreviousAddress     string `json:"previousAddress,omitempty"`
	CurrentAddress      string `json:"currentAddress,omitempty"`
	ReplacementDetected bool   `json:"replacementDetected"`
	Online              bool   `json:"online"`
	Unstable            bool   `json:"unstable"`
	MonitorState        string `json:"monitorState,omitempty"`
	Candidates          int    `json:"candidates,omitempty"`
	Message             string `json:"message"`
}

type ReplacementVerifier func(context.Context, string) (ReplacementVerification, error)

// APIReplacementVerificationHandler synchronizes subscriptions and verifies the
// active replacement corresponding to the card the user clicked.
func APIReplacementVerificationHandler(verify ReplacementVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/v1/replacements/"
		remainder := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
		parts := strings.Split(remainder, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] != "verify" {
			writeError(w, "Invalid replacement action", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if verify == nil {
			writeError(w, "Subscription refresh is disabled", http.StatusServiceUnavailable)
			return
		}

		result, err := verify(r.Context(), parts[0])
		if err != nil {
			writeError(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, result)
	}
}
