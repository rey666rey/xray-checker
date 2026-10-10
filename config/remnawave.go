package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var remnawavePanelIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// RemnawavePanel binds one checker subscription name to one Remnawave API.
// APIToken is deliberately excluded from JSON so it cannot leak through an API
// response or an accidental config dump.
type RemnawavePanel struct {
	ID           string `json:"id"`
	Subscription string `json:"subscription"`
	APIURL       string `json:"-"`
	APIToken     string `json:"-"`
}

func (p RemnawavePanel) Configured() bool {
	return p.APIURL != "" && p.APIToken != ""
}

func parseRemnawavePanels(getenv func(string) string) ([]RemnawavePanel, error) {
	panelList := strings.TrimSpace(getenv("REMNAWAVE_PANELS"))
	if panelList == "" {
		return nil, nil
	}

	panels := make([]RemnawavePanel, 0)
	seenIDs := make(map[string]struct{})
	seenSubscriptions := make(map[string]string)

	for _, rawID := range strings.Split(panelList, ",") {
		id := strings.ToLower(strings.TrimSpace(rawID))
		if id == "" {
			return nil, fmt.Errorf("REMNAWAVE_PANELS contains an empty panel ID")
		}
		if !remnawavePanelIDPattern.MatchString(id) {
			return nil, fmt.Errorf("invalid Remnawave panel ID %q: use lowercase letters, digits, and underscores", id)
		}
		if _, exists := seenIDs[id]; exists {
			return nil, fmt.Errorf("duplicate Remnawave panel ID %q", id)
		}
		seenIDs[id] = struct{}{}

		prefix := "REMNAWAVE_" + strings.ToUpper(id) + "_"
		subscription := strings.TrimSpace(getenv(prefix + "SUBSCRIPTION"))
		if subscription == "" {
			return nil, fmt.Errorf("%sSUBSCRIPTION is required", prefix)
		}
		if existingID, exists := seenSubscriptions[strings.ToLower(subscription)]; exists {
			return nil, fmt.Errorf("subscription %q is assigned to both %q and %q", subscription, existingID, id)
		}
		seenSubscriptions[strings.ToLower(subscription)] = id

		apiURL := strings.TrimSpace(getenv(prefix + "API_URL"))
		apiToken := strings.TrimSpace(getenv(prefix + "API_TOKEN"))
		if (apiURL == "") != (apiToken == "") {
			return nil, fmt.Errorf("%sAPI_URL and %sAPI_TOKEN must be set together", prefix, prefix)
		}
		if apiURL != "" {
			parsed, err := url.ParseRequestURI(apiURL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return nil, fmt.Errorf("%sAPI_URL must be an absolute HTTP or HTTPS URL", prefix)
			}
			if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return nil, fmt.Errorf("%sAPI_URL must not contain credentials, a query, or a fragment", prefix)
			}
			apiURL = strings.TrimRight(apiURL, "/")
		}

		panels = append(panels, RemnawavePanel{
			ID:           id,
			Subscription: subscription,
			APIURL:       apiURL,
			APIToken:     apiToken,
		})
	}

	return panels, nil
}
