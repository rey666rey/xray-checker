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
