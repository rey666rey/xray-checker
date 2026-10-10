package config

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestValidateRequiresLoopbackXrayInboundHost(t *testing.T) {
	valid := CLI{}
	valid.Xray.InboundHost = "127.0.0.2"
	if err := valid.Validate(); err != nil {
		t.Fatalf("loopback address rejected: %v", err)
	}

	for _, host := range []string{"", "0.0.0.0", "192.0.2.10", "localhost"} {
		invalid := CLI{}
		invalid.Xray.InboundHost = host
		if err := invalid.Validate(); err == nil {
			t.Errorf("inbound host %q unexpectedly accepted", host)
		}
	}
}

func TestParseExcludeNamePrefixesFromEnvironment(t *testing.T) {
	previousArgs := os.Args
	previousConfig := CLIConfig
	t.Cleanup(func() {
		os.Args = previousArgs
		CLIConfig = previousConfig
	})

	os.Args = []string{"xray-checker"}
	CLIConfig = CLI{}
	t.Setenv("SUBSCRIPTION_URL", "https://example.com/subscription")
	t.Setenv("PROXY_EXCLUDE_NAME_PREFIXES", "LTE,TEST")
	if err := Parse("test"); err != nil {
		t.Fatal(err)
	}

	if want := []string{"LTE", "TEST"}; !reflect.DeepEqual(CLIConfig.Proxy.ExcludeNamePrefixes, want) {
		t.Fatalf("exclude prefixes = %#v, want %#v", CLIConfig.Proxy.ExcludeNamePrefixes, want)
	}
}

func TestParseRemnawavePanels(t *testing.T) {
	env := map[string]string{
		"REMNAWAVE_PANELS":                "rw220,rwleto,rwmaster,rwdog",
		"REMNAWAVE_RW220_SUBSCRIPTION":    "220V",
		"REMNAWAVE_RW220_API_URL":         "https://rw220.example.com/",
		"REMNAWAVE_RW220_API_TOKEN":       "secret-220",
		"REMNAWAVE_RWLETO_SUBSCRIPTION":   "LETO",
		"REMNAWAVE_RWMASTER_SUBSCRIPTION": "MASTER",
		"REMNAWAVE_RWDOG_SUBSCRIPTION":    "DOG",
	}

	panels, err := parseRemnawavePanels(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if len(panels) != 4 {
		t.Fatalf("panel count = %d, want 4", len(panels))
	}
	if panels[0].ID != "rw220" || panels[0].Subscription != "220V" || panels[0].APIURL != "https://rw220.example.com" || !panels[0].Configured() {
		t.Fatalf("unexpected configured panel: %#v", panels[0])
	}
	if panels[1].Configured() {
		t.Fatalf("panel with empty URL and token must remain inactive: %#v", panels[1])
	}

	encoded, err := json.Marshal(panels[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-220") || strings.Contains(string(encoded), "rw220.example.com") {
		t.Fatalf("Remnawave credentials leaked to JSON: %s", encoded)
	}
}

func TestParseRemnawavePanelsRejectsPartialCredentialsWithoutLeakingToken(t *testing.T) {
	env := map[string]string{
		"REMNAWAVE_PANELS":             "rw220",
		"REMNAWAVE_RW220_SUBSCRIPTION": "220V",
		"REMNAWAVE_RW220_API_TOKEN":    "do-not-print-this-token",
	}

	_, err := parseRemnawavePanels(func(key string) string { return env[key] })
	if err == nil {
		t.Fatal("partial Remnawave credentials unexpectedly accepted")
	}
	if strings.Contains(err.Error(), env["REMNAWAVE_RW220_API_TOKEN"]) {
		t.Fatalf("validation error leaked the API token: %v", err)
	}
}

func TestParseRemnawavePanelsRejectsDuplicateSubscription(t *testing.T) {
	env := map[string]string{
		"REMNAWAVE_PANELS":              "first,second",
		"REMNAWAVE_FIRST_SUBSCRIPTION":  "220V",
		"REMNAWAVE_SECOND_SUBSCRIPTION": "220v",
	}

	if _, err := parseRemnawavePanels(func(key string) string { return env[key] }); err == nil {
		t.Fatal("duplicate subscription mapping unexpectedly accepted")
	}
}
