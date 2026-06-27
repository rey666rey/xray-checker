package xray

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"xray-checker/models"

	"github.com/xtls/xray-core/infra/conf/serial"
)

func TestExtractFailingOutboundTag(t *testing.T) {
	cases := map[string]string{
		"infra/conf: failed to build outbound config with tag BAD-xhttp_1 > infra/conf: unsupported mode: x": "BAD-xhttp_1",
		"failed to build outbound config with tag My Server | node_7 > some reason":                          "My Server | node_7",
		"some unrelated error":   "",
		"with tag trailing-only": "trailing-only",
	}
	for in, want := range cases {
		if got := extractFailingOutboundTag(in); got != want {
			t.Errorf("extractFailingOutboundTag(%q) = %q, want %q", in, got, want)
		}
	}
}

// A single unbuildable proxy must be excluded (with the rest kept) rather than
// aborting the whole config.
func TestGenerateValidatedConfigPrunesUnbuildable(t *testing.T) {
	good := &models.ProxyConfig{
		Protocol: "vless", Server: "good.example.com", Port: 443, Name: "good",
		UUID: "00000000-0000-0000-0000-000000000000", Type: "tcp", Security: "none", Index: 0,
	}
	bad := &models.ProxyConfig{
		Protocol: "vless", Server: "bad.example.com", Port: 443, Name: "bad",
		UUID: "00000000-0000-0000-0000-000000000000", Type: "xhttp", Security: "tls", SNI: "bad.example.com",
		RawXhttpSettings: `{"mode":"bogus-mode","path":"/"}`, Index: 1,
	}

	f := filepath.Join(t.TempDir(), "cfg.json")
	g := NewConfigGenerator()
	survivors, err := g.GenerateValidatedConfig([]*models.ProxyConfig{good, bad}, 20000, f, "none")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(survivors) != 1 || survivors[0].Name != "good" {
		t.Fatalf("expected only 'good' to survive, got %d proxies", len(survivors))
	}
	data, _ := os.ReadFile(f)
	if err := validateConfigBuild(data); err != nil {
		t.Fatalf("written config must be buildable, got: %v", err)
	}
}

// buildsWithXrayCore feeds a generated config through the exact decode+build path
// that xray/runner.go uses at startup. This validates the JSON against xray-core's
// real schema, catching key/placement mistakes that json.Unmarshal would otherwise
// silently ignore at runtime.
func buildsWithXrayCore(t *testing.T, proxies []*models.ProxyConfig) []byte {
	t.Helper()
	g := NewConfigGenerator()
	configBytes, err := g.GenerateConfig(proxies, 10000, "none")
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}
	xrayConfig, err := serial.DecodeJSONConfig(bytes.NewReader(configBytes))
	if err != nil {
		t.Fatalf("xray-core rejected generated config (decode): %v\nconfig:\n%s", err, configBytes)
	}
	if _, err := xrayConfig.Build(); err != nil {
		t.Fatalf("xray-core rejected generated config (build): %v\nconfig:\n%s", err, configBytes)
	}
	return configBytes
}

// streamSettingsOf extracts the streamSettings map of the first proxy outbound
// (skipping the freedom/blackhole/dns outbounds appended by the generator).
func streamSettingsOf(t *testing.T, configBytes []byte) map[string]json.RawMessage {
	t.Helper()
	var parsed struct {
		Outbounds []struct {
			Protocol       string                     `json:"protocol"`
			StreamSettings map[string]json.RawMessage `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(configBytes, &parsed); err != nil {
		t.Fatalf("failed to parse generated config: %v", err)
	}
	for _, ob := range parsed.Outbounds {
		if ob.StreamSettings != nil {
			return ob.StreamSettings
		}
	}
	t.Fatalf("no outbound with streamSettings found")
	return nil
}

func TestGenerateHysteriaConfigWithObfsAndPortHopping(t *testing.T) {
	proxy := &models.ProxyConfig{
		Protocol:             "hysteria",
		Server:               "example.com",
		Port:                 443,
		Name:                 "hy2-advanced",
		SNI:                  "example.com",
		Security:             "tls",
		HysteriaAuth:         "secret-auth",
		HysteriaObfs:         "salamander",
		HysteriaObfsPassword: "obfs-pass",
		HysteriaPorts:        "20000-50000",
		HysteriaHopInterval:  30,
		Index:                0,
	}

	configBytes := buildsWithXrayCore(t, []*models.ProxyConfig{proxy})
	ss := streamSettingsOf(t, configBytes)

	// finalmask must be at the streamSettings top level, NOT under sockopt.
	if _, ok := ss["finalmask"]; !ok {
		t.Errorf("expected top-level streamSettings.finalmask, got keys: %v", keysOf(ss))
	}
	if _, ok := ss["sockopt"]; ok {
		t.Errorf("finalmask must not be placed under sockopt")
	}

	// Verify port-hopping ports and salamander obfs survived into the schema.
	var fm struct {
		QuicParams *struct {
			UdpHop *struct {
				Ports json.RawMessage `json:"ports"`
			} `json:"udpHop"`
		} `json:"quicParams"`
		Udp []struct {
			Type string `json:"type"`
		} `json:"udp"`
	}
	if err := json.Unmarshal(ss["finalmask"], &fm); err != nil {
		t.Fatalf("failed to parse finalmask: %v", err)
	}
	if fm.QuicParams == nil || fm.QuicParams.UdpHop == nil || len(fm.QuicParams.UdpHop.Ports) == 0 {
		t.Errorf("expected finalmask.quicParams.udpHop.ports to be set")
	}
	if len(fm.Udp) == 0 || fm.Udp[0].Type != "salamander" {
		t.Errorf("expected finalmask.udp[0].type == salamander, got %+v", fm.Udp)
	}
}

func TestGenerateBasicHysteriaConfig(t *testing.T) {
	proxy := &models.ProxyConfig{
		Protocol:     "hysteria",
		Server:       "example.com",
		Port:         443,
		Name:         "hy2-basic",
		SNI:          "example.com",
		Security:     "tls",
		HysteriaAuth: "secret-auth",
		Index:        0,
	}
	configBytes := buildsWithXrayCore(t, []*models.ProxyConfig{proxy})
	ss := streamSettingsOf(t, configBytes)
	// No obfs/port-hopping -> no finalmask emitted.
	if _, ok := ss["finalmask"]; ok {
		t.Errorf("basic hysteria should not emit finalmask")
	}
}

func TestGenerateVlessConfigStillBuilds(t *testing.T) {
	// Regression guard for the non-hysteria path after the dependency bump.
	proxy := &models.ProxyConfig{
		Protocol:  "vless",
		Server:    "example.com",
		Port:      443,
		Name:      "vless-reality",
		UUID:      "00000000-0000-0000-0000-000000000000",
		Flow:      "xtls-rprx-vision",
		Type:      "tcp",
		Security:  "reality",
		SNI:       "example.com",
		PublicKey: "jnsDTya4elAlV-czGFJbvOHJFdXWn7MGGwmKzZ_hoTQ",
		ShortID:   "64d5300f209d1abb",
		Index:     0,
	}
	buildsWithXrayCore(t, []*models.ProxyConfig{proxy})
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
