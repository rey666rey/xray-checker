package subscription

import (
	"testing"

	"xray-checker/models"
)

func node(name, server string, port int) *models.ProxyConfig {
	return &models.ProxyConfig{Name: name, Server: server, Port: port, Protocol: "vless"}
}

func TestNameGroupedProxies_SingleNodeTakesRemarks(t *testing.T) {
	g := []*models.ProxyConfig{node("original-tag", "s1", 443)}
	nameGroupedProxies("🇳🇱 Netherlands", g)
	if g[0].Name != "🇳🇱 Netherlands" {
		t.Errorf("single node should take the group remarks, got %q", g[0].Name)
	}
}

func TestNameGroupedProxies_SingleNodeKeepsTagWhenNoRemarks(t *testing.T) {
	g := []*models.ProxyConfig{node("original-tag", "s1", 443)}
	nameGroupedProxies("", g)
	if g[0].Name != "original-tag" {
		t.Errorf("single node with no remarks should keep its tag, got %q", g[0].Name)
	}
}

func TestNameGroupedProxies_MultiNodeCombinesRemarksAndTag(t *testing.T) {
	g := []*models.ProxyConfig{
		node("NL", "nl.example.com", 443),
		node("DE", "de.example.com", 443),
	}
	nameGroupedProxies("Auto", g)
	want := map[string]bool{"Auto | NL": true, "Auto | DE": true}
	for _, pc := range g {
		if !want[pc.Name] {
			t.Errorf("unexpected node name %q", pc.Name)
		}
	}
}

func TestNameGroupedProxies_DisambiguatesDuplicateTags(t *testing.T) {
	g := []*models.ProxyConfig{
		node("NL", "nl-1.example.com", 443),
		node("NL", "nl-2.example.com", 443),
	}
	nameGroupedProxies("Auto", g)
	if g[0].Name == g[1].Name {
		t.Fatalf("nodes with duplicate tags must get distinct names, both %q", g[0].Name)
	}
	for _, pc := range g {
		// duplicated tag -> server:port appended for disambiguation
		if pc.Name != "Auto | NL (nl-1.example.com:443)" && pc.Name != "Auto | NL (nl-2.example.com:443)" {
			t.Errorf("expected disambiguated name, got %q", pc.Name)
		}
	}
}

func TestNameGroupedProxies_MultiNodeFallsBackToServerWhenNoTag(t *testing.T) {
	g := []*models.ProxyConfig{
		node("", "a.example.com", 443),
		node("", "b.example.com", 443),
	}
	nameGroupedProxies("Auto", g)
	want := map[string]bool{"Auto | a.example.com:443": true, "Auto | b.example.com:443": true}
	for _, pc := range g {
		if !want[pc.Name] {
			t.Errorf("expected server-based node name, got %q", pc.Name)
		}
	}
}

func TestParseProxyURI(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantNil  bool
		protocol string
		server   string
		port     int
		user     string
		pass     string
		security string
		sni      string
		pinned   string
		dispName string
	}{
		{name: "socks5 plain creds", line: "socks5://user:pass@1.2.3.4:1080#my-socks",
			protocol: "socks", server: "1.2.3.4", port: 1080, user: "user", pass: "pass", dispName: "my-socks"},
		{name: "socks base64 creds, no fragment", line: "socks://dXNlcjpwYXNz@1.2.3.4:1080",
			protocol: "socks", server: "1.2.3.4", port: 1080, user: "user", pass: "pass", dispName: "1.2.3.4:1080"},
		{name: "socks5h no creds", line: "socks5h://1.2.3.4:1080",
			protocol: "socks", server: "1.2.3.4", port: 1080, dispName: "1.2.3.4:1080"},
		{name: "http with creds", line: "http://user:pass@proxy.example.com:8080#h",
			protocol: "http", server: "proxy.example.com", port: 8080, user: "user", pass: "pass", dispName: "h"},
		{name: "https tls + pinned cert + sni", line: "https://1.2.3.4:8443?pinnedPeerCertSha256=aabbcc&sni=cdn.example.com#tls",
			protocol: "http", server: "1.2.3.4", port: 8443, security: "tls", sni: "cdn.example.com", pinned: "aabbcc", dispName: "tls"},
		{name: "https tls default sni = host", line: "https://1.2.3.4:8443",
			protocol: "http", server: "1.2.3.4", port: 8443, security: "tls", sni: "1.2.3.4", dispName: "1.2.3.4:8443"},
		{name: "vless not consumed", line: "vless://uuid@h:443?type=tcp#x", wantNil: true},
		{name: "web url with path not consumed", line: "https://example.com:443/sub/abc", wantNil: true},
		{name: "missing port not consumed", line: "socks5://1.2.3.4", wantNil: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pc := parseProxyURI(c.line)
			if c.wantNil {
				if pc != nil {
					t.Fatalf("expected nil, got %+v", pc)
				}
				return
			}
			if pc == nil {
				t.Fatalf("expected a config, got nil")
			}
			if pc.Protocol != c.protocol || pc.Server != c.server || pc.Port != c.port {
				t.Errorf("got protocol=%q server=%q port=%d", pc.Protocol, pc.Server, pc.Port)
			}
			if pc.Username != c.user || pc.Password != c.pass {
				t.Errorf("creds got user=%q pass=%q want user=%q pass=%q", pc.Username, pc.Password, c.user, c.pass)
			}
			if pc.Security != c.security || pc.SNI != c.sni || pc.PinnedPeerCertSha256 != c.pinned {
				t.Errorf("tls got security=%q sni=%q pinned=%q", pc.Security, pc.SNI, pc.PinnedPeerCertSha256)
			}
			if pc.Name != c.dispName {
				t.Errorf("name got %q want %q", pc.Name, c.dispName)
			}
			if pc.Type != "tcp" {
				t.Errorf("type got %q want tcp", pc.Type)
			}
		})
	}
}

func TestExtractDirectProxyLines(t *testing.T) {
	p := NewParser()
	blob := "vless://uuid@vlesshost:443?type=tcp#vless\nsocks5://user:pass@1.2.3.4:1080#s\nhttps://1.2.3.4:8443#tls\n"
	configs, remaining := p.extractDirectProxyLines([]byte(blob))
	if len(configs) != 2 {
		t.Fatalf("expected 2 direct configs, got %d", len(configs))
	}
	rem := string(remaining)
	if !contains(rem, "vless://") {
		t.Errorf("vless line should remain for the libXray path, remaining=%q", rem)
	}
	if contains(rem, "socks5://") || contains(rem, "https://1.2.3.4:8443") {
		t.Errorf("direct proxy lines should be removed from remaining, remaining=%q", rem)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
