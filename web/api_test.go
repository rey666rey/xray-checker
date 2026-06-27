package web

import (
	"testing"

	"xray-checker/config"
)

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
