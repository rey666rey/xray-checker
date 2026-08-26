package web

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xray-checker/checker"
	"xray-checker/config"
	"xray-checker/models"
	"xray-checker/xray"
)

func TestRenderIndexIncludesSubscriptionName(t *testing.T) {
	var out bytes.Buffer
	err := RenderIndex(&out, PageData{
		Endpoints: []EndpointInfo{{
			Name:    "node-1",
			SubName: "LETO",
		}},
	})
	if err != nil {
		t.Fatalf("RenderIndex() error = %v", err)
	}
	if !strings.Contains(out.String(), `subName: "LETO"`) {
		t.Fatal("rendered dashboard does not include the subscription name")
	}
	if !strings.Contains(out.String(), `diagnoseNode(item.block.nodeId`) ||
		!strings.Contains(out.String(), `diagnoseNode(item.proxy.nodeId`) ||
		!strings.Contains(out.String(), `Node diagnosis`) {
		t.Fatal("rendered private dashboard does not include node diagnosis controls")
	}
}

func TestAPINodesGroupsSeveralHostsOnOneEndpoint(t *testing.T) {
	proxies := []*models.ProxyConfig{
		{Protocol: "vless", Security: "reality", Name: "Germany (s1)", Server: "192.0.2.10", Port: 443, UUID: "00000000-0000-4000-8000-000000000001"},
		{Protocol: "vless", Security: "tls", Name: "Germany (s1 v2)", Server: "192.0.2.10", Port: 8443, UUID: "00000000-0000-4000-8000-000000000002"},
		{Protocol: "hysteria", Name: "Germany (s1 v3)", Server: "192.0.2.10", Port: 8444, HysteriaAuth: "secret"},
	}
	xray.PrepareProxyConfigs(proxies)
	proxyChecker := checker.NewProxyChecker(proxies, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	proxyChecker.SetEndpointPool(checker.NewEndpointPool(proxies))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	APINodesHandler(proxyChecker, 10000).ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data []NodeGroupInfo `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].TotalBindings != 3 || response.Data[0].HostCount != 3 {
		t.Fatalf("node groups=%#v, want one node with three bindings", response.Data)
	}
}

func TestAPINodesReturnsLatestDiagnosis(t *testing.T) {
	proxies := []*models.ProxyConfig{{
		Protocol: "vless", Security: "reality", Name: "Spain", Server: "192.0.2.20",
		Port: 443, UUID: "00000000-0000-4000-8000-000000000001",
	}}
	xray.PrepareProxyConfigs(proxies)
	proxyChecker := checker.NewProxyChecker(proxies, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	path := filepath.Join(t.TempDir(), "diagnoses.json")
	payload := `{"version":1,"nodes":{"` + proxies[0].NodeID + `":[{"runId":"run-1","nodeId":"` + proxies[0].NodeID + `","server":"192.0.2.20","revision":"old","probeId":"local:en0","state":"completed","verdict":"healthy","startedAt":1,"completedAt":2,"control":{"online":true}}]}}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := proxyChecker.SetDiagnosisFile(path); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	APINodesHandler(proxyChecker, 10000).ServeHTTP(recorder, request)
	var response struct {
		Data []NodeGroupInfo `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].Diagnosis == nil {
		t.Fatalf("response=%#v, want diagnosis", response.Data)
	}
	if response.Data[0].Diagnosis.Verdict != checker.DiagnosisHealthy || !response.Data[0].Diagnosis.Stale {
		t.Fatalf("diagnosis=%#v", response.Data[0].Diagnosis)
	}
}

func TestAPINodeDiagnosisHistoryEndpoint(t *testing.T) {
	proxies := []*models.ProxyConfig{{
		Protocol: "vless", Name: "Node", Server: "192.0.2.30", Port: 443,
		UUID: "00000000-0000-4000-8000-000000000001",
	}}
	xray.PrepareProxyConfigs(proxies)
	proxyChecker := checker.NewProxyChecker(proxies, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/v1/nodes/"+proxies[0].NodeID+"/diagnosis", nil)
	APINodesHandler(proxyChecker, 10000).ServeHTTP(recorder, request)
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `"data":[]`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMaskMiddle(t *testing.T) {
	cases := map[string]string{
		"":                                     "",
		"short":                                "****", // <= 8 chars fully masked
		"12345678":                             "****", // exactly 8 still fully masked
		"123456789":                            "1234...6789",
		"d342d11e-d424-4583-b36e-524ab1f0afa4": "d342...afa4",
	}
	for in, want := range cases {
		if got := maskMiddle(in); got != want {
			t.Errorf("maskMiddle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeGeneratedConfigMasksSecretsKeepsPublic(t *testing.T) {
	// Mirrors the shape of a generated vless+reality+hysteria outbound.
	outbound := map[string]interface{}{
		"tag":      "node_0",
		"protocol": "vless",
		"settings": map[string]interface{}{
			"vnext": []map[string]interface{}{
				{
					"address": "example.com",
					"port":    443,
					"users": []map[string]interface{}{
						{"id": "d342d11e-d424-4583-b36e-524ab1f0afa4", "encryption": "none"},
					},
				},
			},
		},
		"streamSettings": map[string]interface{}{
			"realitySettings": map[string]interface{}{
				"publicKey": "Vft7...PuBLiCkeYmaterialShouldStay",
				"shortId":   "0123abcd",
			},
			"hysteriaSettings": map[string]interface{}{
				"auth": "super-secret-hysteria-auth-token",
			},
			"kcpSettings": map[string]interface{}{
				"seed": "my-kcp-seed-value",
			},
		},
	}

	got := sanitizeGeneratedConfig(outbound)
	if got == nil {
		t.Fatal("sanitizeGeneratedConfig returned nil")
	}

	user := got["settings"].(map[string]interface{})["vnext"].([]interface{})[0].(map[string]interface{})["users"].([]interface{})[0].(map[string]interface{})
	if user["id"] != "d342...afa4" {
		t.Errorf("uuid not masked: %v", user["id"])
	}

	stream := got["streamSettings"].(map[string]interface{})
	if stream["realitySettings"].(map[string]interface{})["publicKey"] == "****" {
		t.Error("publicKey must NOT be masked (it is public material)")
	}
	if stream["hysteriaSettings"].(map[string]interface{})["auth"] != "supe...oken" {
		t.Errorf("hysteria auth not masked: %v", stream["hysteriaSettings"].(map[string]interface{})["auth"])
	}
	if stream["kcpSettings"].(map[string]interface{})["seed"] != "my-k...alue" {
		t.Errorf("kcp seed not masked: %v", stream["kcpSettings"].(map[string]interface{})["seed"])
	}
	// Non-secret fields are preserved untouched.
	if got["tag"] != "node_0" || got["protocol"] != "vless" {
		t.Errorf("non-secret fields altered: tag=%v protocol=%v", got["tag"], got["protocol"])
	}
}

func TestShouldShowServerDetails(t *testing.T) {
	cases := []struct {
		name    string
		show    bool
		public  bool
		trusted bool
		want    bool
	}{
		{"off by default", false, false, false, false},
		{"on, private", true, false, false, true},
		{"on, public, untrusted -> hidden", true, true, false, false},
		{"on, public, trusted -> shown", true, true, true, true},
		{"off, public, trusted -> still off", false, true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			config.CLIConfig.Web.ShowServerDetails = c.show
			config.CLIConfig.Web.Public = c.public
			config.CLIConfig.Web.TrustedExternalAuth = c.trusted
			if got := shouldShowServerDetails(); got != c.want {
				t.Errorf("shouldShowServerDetails() = %v, want %v", got, c.want)
			}
		})
	}
	// reset
	config.CLIConfig.Web.ShowServerDetails = false
	config.CLIConfig.Web.Public = false
	config.CLIConfig.Web.TrustedExternalAuth = false
}
