package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type ProxyConfig struct {
	Protocol             string
	Server               string
	Port                 int
	Name                 string
	Security             string
	Type                 string
	UUID                 string
	Flow                 string
	Encryption           string
	HeaderType           string
	Path                 string
	Host                 string
	SNI                  string
	Fingerprint          string
	PublicKey            string
	ShortID              string
	Mode                 string
	Username             string
	Password             string
	Method               string
	Level                int
	AlterId              int
	VMessAid             int
	MultiMode            bool
	ServiceName          string
	IdleTimeout          int
	WindowsSize          int
	AllowInsecure        bool
	PinnedPeerCertSha256 string
	VerifyPeerCertByName string
	ALPN                 []string
	Index                int
	Settings             map[string]string
	StableID             string
	// HostID identifies one subscription host/inbound independently from the
	// endpoint selected by a panel. NodeID groups every host that currently uses
	// the same physical endpoint. A LogicalID still identifies one checkable
	// host+endpoint binding.
	HostID string
	NodeID string
	// LogicalID identifies the panel node independently from its current
	// connection revision. StableID intentionally changes when Server or another
	// connection parameter changes; LogicalID is carried over during subscription
	// diffs so repair history survives an IP replacement.
	LogicalID        string
	RawXhttpSettings string
	RawKcpSettings   string
	SubName          string
	GroupName        string

	// MetricsLabels holds operator-defined static labels parsed from a JSON
	// outbound's "metricsLabels" object. They are exported as extra Prometheus
	// labels and in the API, but are deliberately NOT part of GenerateStableID
	// (they are metadata, not connection identity).
	MetricsLabels map[string]string

	// Hysteria2 fields
	HysteriaAuth         string
	HysteriaUp           string
	HysteriaDown         string
	HysteriaPorts        string
	HysteriaHopInterval  int32
	HysteriaObfs         string
	HysteriaObfsPassword string

	// WireGuard fields (Server/Port hold the peer endpoint).
	WGPrivateKey    string
	WGPeerPublicKey string
	WGPreSharedKey  string
	WGAddresses     []string
	WGAllowedIPs    []string
	WGMTU           int
	WGKeepalive     int
	WGDNS           []string
}

// GenerateLogicalID returns a display-identity hash used only as the initial
// logical node identity. Subscription updates first match exact connection
// revisions and then this display identity, allowing the old LogicalID to be
// carried over when either the name or the endpoint changes.
func (pc *ProxyConfig) GenerateLogicalID() string {
	h := sha256.New()
	write := func(value string) {
		fmt.Fprintf(h, "%d:%s;", len(value), value)
	}
	write(strings.ToLower(strings.TrimSpace(pc.Protocol)))
	write(strings.TrimSpace(pc.SubName))
	write(strings.TrimSpace(pc.GroupName))
	write(strings.TrimSpace(pc.Name))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// GenerateHostID identifies a subscription entry while deliberately excluding
// Server. Panels may return a different member of a host's node pool on every
// request, so the endpoint cannot be part of the host identity. Transport and
// security are included to keep same-named Reality/TLS/Hysteria inbounds apart.
func (pc *ProxyConfig) GenerateHostID() string {
	h := sha256.New()
	write := func(value string) {
		fmt.Fprintf(h, "%d:%s;", len(value), value)
	}
	write(strings.ToLower(strings.TrimSpace(pc.Protocol)))
	write(strings.TrimSpace(pc.SubName))
	write(strings.TrimSpace(pc.GroupName))
	write(strings.TrimSpace(pc.Name))
	write(strings.ToLower(strings.TrimSpace(pc.Security)))
	write(strings.ToLower(strings.TrimSpace(pc.Type)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// GenerateNodeID groups bindings by physical endpoint for the repair view. The
// port is excluded intentionally: several inbounds on one machine can listen on
// different ports and should still be shown under the same node/IP.
func (pc *ProxyConfig) GenerateNodeID() string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(pc.Server))))
	return hex.EncodeToString(sum[:])[:16]
}

// GenerateBindingID identifies one checkable host+endpoint edge. Unlike the
// legacy display-only LogicalID it is deterministic when an endpoint disappears
// and later returns, so its archived repair history can be recovered safely.
func (pc *ProxyConfig) GenerateBindingID() string {
	hostID := pc.HostID
	if hostID == "" {
		hostID = pc.GenerateHostID()
	}
	value := hostID + "\x00" + strings.ToLower(strings.TrimSpace(pc.Server)) + "\x00" + strconv.Itoa(pc.Port)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

// AssignTopologyIDs fills the many-to-many host/node identifiers. It is safe to
// call repeatedly as subscription updates carry existing IDs forward.
func AssignTopologyIDs(proxies []*ProxyConfig) {
	for _, proxy := range proxies {
		if proxy.HostID == "" {
			proxy.HostID = proxy.GenerateHostID()
		}
		proxy.NodeID = proxy.GenerateNodeID()
	}
}

// GenerateRevisionID hashes the complete private connection configuration. It is
// used internally for subscription diffs and is never exposed as a public ID.
// Unlike StableID it deliberately includes credentials, so a rotated password or
// UUID is treated as a new revision and verified immediately.
func (pc *ProxyConfig) GenerateRevisionID() string {
	clone := pc.revisionComparable()
	data, _ := json.Marshal(clone)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (pc *ProxyConfig) revisionComparable() ProxyConfig {
	clone := *pc
	clone.Name = ""
	clone.SubName = ""
	clone.GroupName = ""
	clone.Index = 0
	clone.StableID = ""
	clone.LogicalID = ""
	clone.HostID = ""
	clone.NodeID = ""
	clone.MetricsLabels = nil
	// This panel rotates SNI choices on every otherwise-identical subscription
	// response for hundreds of nodes. Treating that volatile choice as a repair
	// revision would restart Xray and recheck most of the fleet every minute.
	// Fresh process starts still consume the latest SNI; live repair monitoring is
	// intentionally driven by the endpoint and the remaining connection fields.
	clone.SNI = ""
	clone.RawXhttpSettings = canonicalRawJSON(clone.RawXhttpSettings)
	clone.RawKcpSettings = canonicalRawJSON(clone.RawKcpSettings)
	clone.ALPN = sortedStrings(clone.ALPN)
	clone.WGAddresses = sortedStrings(clone.WGAddresses)
	clone.WGAllowedIPs = sortedStrings(clone.WGAllowedIPs)
	clone.WGDNS = sortedStrings(clone.WGDNS)
	return clone
}

// RevisionDiffFields returns only Go field names, never their values. It is safe
// for diagnostics because credentials and endpoints cannot appear in the output.
func RevisionDiffFields(left, right *ProxyConfig) []string {
	leftValue := reflect.ValueOf(left.revisionComparable())
	rightValue := reflect.ValueOf(right.revisionComparable())
	typeInfo := leftValue.Type()
	fields := make([]string, 0)
	for index := 0; index < leftValue.NumField(); index++ {
		if !reflect.DeepEqual(leftValue.Field(index).Interface(), rightValue.Field(index).Interface()) {
			fields = append(fields, typeInfo.Field(index).Name)
		}
	}
	return fields
}

// AssignLogicalIDs fills missing logical IDs and disambiguates duplicate display
// identities deterministically. Existing IDs are preserved during config updates.
func AssignLogicalIDs(proxies []*ProxyConfig) {
	groups := make(map[string][]*ProxyConfig)
	used := make(map[string]bool)
	for _, proxy := range proxies {
		if proxy.LogicalID != "" {
			used[proxy.LogicalID] = true
			continue
		}
		base := proxy.GenerateLogicalID()
		groups[base] = append(groups[base], proxy)
	}
	for base, group := range groups {
		sort.SliceStable(group, func(i, j int) bool {
			left := fmt.Sprintf("%s:%d\x00%s", group[i].Server, group[i].Port, group[i].GenerateStableID())
			right := fmt.Sprintf("%s:%d\x00%s", group[j].Server, group[j].Port, group[j].GenerateStableID())
			return left < right
		})
		nextSuffix := 0
		for _, proxy := range group {
			for {
				candidate := base
				if nextSuffix > 0 {
					sum := sha256.Sum256([]byte(base + "::logical::" + strconv.Itoa(nextSuffix)))
					candidate = hex.EncodeToString(sum[:])[:16]
				}
				nextSuffix++
				if used[candidate] {
					continue
				}
				proxy.LogicalID = candidate
				used[candidate] = true
				break
			}
		}
	}
}

func (pc *ProxyConfig) Validate() error {
	if pc.Protocol == "" {
		return fmt.Errorf("protocol is required")
	}
	if pc.Server == "" {
		return fmt.Errorf("server is required")
	}
	if pc.Port <= 0 || pc.Port > 65535 {
		return fmt.Errorf("invalid port number: %d", pc.Port)
	}

	switch pc.Protocol {
	case "vless", "vmess":
		if pc.UUID == "" {
			return fmt.Errorf("UUID is required for %s", pc.Protocol)
		}
	case "trojan":
		if pc.Password == "" {
			return fmt.Errorf("password is required for Trojan")
		}
	case "shadowsocks":
		if pc.Password == "" || pc.Method == "" {
			return fmt.Errorf("password and method are required for Shadowsocks")
		}
	case "hysteria":
		if pc.HysteriaAuth == "" {
			return fmt.Errorf("auth is required for Hysteria2")
		}
	case "socks", "http":
		// Forward proxies need only server/port (checked above); credentials
		// are optional.
	case "wireguard":
		if pc.WGPrivateKey == "" || pc.WGPeerPublicKey == "" {
			return fmt.Errorf("private key and peer public key are required for WireGuard")
		}
	default:
		return fmt.Errorf("unsupported protocol: %s", pc.Protocol)
	}

	return nil
}

// GenerateStableID returns a 16-hex content hash that identifies a proxy by its
// connection parameters. Every field that affects the actual connection is included
// so endpoints differing only in transport details (path, host, fp, shortId, flow,
// alpn, ...) get distinct IDs. Name/SubName are deliberately excluded so that
// renaming a server in the panel does NOT change its ID; same-connection configs
// that differ only by name are separated afterwards by AssignStableIDs.
func (pc *ProxyConfig) GenerateStableID() string {
	h := sha256.New()
	// length-framed components so values containing the delimiter stay unambiguous
	write := func(key, val string) {
		fmt.Fprintf(h, "%s=%d:%s;", key, len(val), val)
	}

	write("protocol", pc.Protocol)
	write("server", pc.Server)
	write("port", strconv.Itoa(pc.Port))

	// Low-entropy, human-chosen secrets (trojan/shadowsocks password, hysteria auth)
	// are deliberately NOT hashed: stableID is a PUBLIC identifier (API, badges) and a
	// 64-bit truncated hash of a weak password alongside otherwise-known fields could
	// be brute-forced offline. The UUID is kept: it is a high-entropy (122-bit) random
	// identifier, so its hash is not brute-forceable, and it is a meaningful
	// discriminator when one endpoint serves several users/routes that differ only by
	// UUID (e.g. vless route-id setups). The AssignStableIDs tiebreaker separates any
	// configs that still collide after excluding the low-entropy secrets.
	switch pc.Protocol {
	case "vless", "vmess":
		write("uuid", pc.UUID)
		if pc.Protocol == "vmess" {
			write("alterId", strconv.Itoa(pc.GetAlterId()))
		}
	case "shadowsocks":
		write("method", pc.Method)
	case "hysteria":
		write("ports", pc.HysteriaPorts)
		write("obfs", pc.HysteriaObfs)
	case "wireguard":
		// Peer public key and tunnel address are public and distinguish configs;
		// the private key (a secret) is deliberately excluded.
		write("wgpub", pc.WGPeerPublicKey)
		write("wgaddr", strings.Join(pc.WGAddresses, ","))
	}

	write("encryption", pc.Encryption)
	write("flow", pc.Flow)
	write("security", pc.Security)
	write("sni", pc.SNI)
	write("fp", pc.Fingerprint)
	write("pbk", pc.PublicKey)
	write("sid", pc.ShortID)

	alpn := append([]string(nil), pc.ALPN...)
	sort.Strings(alpn)
	write("alpn", strings.Join(alpn, ","))

	write("net", pc.Type)
	write("headerType", pc.HeaderType)
	write("host", pc.Host)
	write("path", pc.Path)
	write("serviceName", pc.ServiceName)
	write("mode", pc.Mode)
	write("rawXhttp", canonicalRawJSON(pc.RawXhttpSettings))
	write("rawKcp", canonicalRawJSON(pc.RawKcpSettings))

	return hex.EncodeToString(h.Sum(nil))[:16]
}

func canonicalRawJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(encoded)
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

// AssignStableIDs sets the final StableID for every proxy in the set. Proxies with
// identical connection parameters (same GenerateStableID) are separated with a
// deterministic suffix derived from a stable ordering (Name, then SubName, then
// Index), so the resulting IDs are unique and stable across subscription reordering.
// The first member of each colliding group keeps the bare hash, so single configs
// (the common case) are unaffected.
func AssignStableIDs(proxies []*ProxyConfig) {
	groups := make(map[string][]*ProxyConfig)
	order := make([]string, 0)
	for _, p := range proxies {
		base := p.GenerateStableID()
		if _, seen := groups[base]; !seen {
			order = append(order, base)
		}
		groups[base] = append(groups[base], p)
	}

	for _, base := range order {
		group := groups[base]
		if len(group) == 1 {
			group[0].StableID = base
			continue
		}
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Name != group[j].Name {
				return group[i].Name < group[j].Name
			}
			if group[i].SubName != group[j].SubName {
				return group[i].SubName < group[j].SubName
			}
			return group[i].Index < group[j].Index
		})
		for n, p := range group {
			if n == 0 {
				p.StableID = base
				continue
			}
			sum := sha256.Sum256([]byte(base + "::" + strconv.Itoa(n)))
			p.StableID = hex.EncodeToString(sum[:])[:16]
		}
	}
}

func (pc *ProxyConfig) GetTransportType() string {
	if pc.Type == "" {
		return "tcp"
	}
	return pc.Type
}

func (pc *ProxyConfig) GetSecurityType() string {
	if pc.Security == "" {
		return "none"
	}
	return pc.Security
}

func (pc *ProxyConfig) GetAlterId() int {
	if pc.AlterId == 0 {
		return pc.VMessAid
	}
	return pc.AlterId
}

func (pc *ProxyConfig) GetVMessSecurity() string {
	if pc.Security == "" {
		return "auto"
	}
	return pc.Security
}

func (pc *ProxyConfig) GetUserLevel() int {
	if pc.Level == 0 {
		return 0
	}
	return pc.Level
}

func (pc *ProxyConfig) GetServiceName() string {
	if pc.ServiceName == "" {
		return ""
	}
	return pc.ServiceName
}

func (pc *ProxyConfig) DebugString() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("  [%d] %s\n", pc.Index, pc.Name))
	sb.WriteString(fmt.Sprintf("      Protocol: %s\n", pc.Protocol))
	sb.WriteString(fmt.Sprintf("      Server:   %s:%d\n", pc.Server, pc.Port))

	switch pc.Protocol {
	case "vless", "vmess":
		sb.WriteString(fmt.Sprintf("      UUID:     %s\n", pc.UUID))
		if pc.Protocol == "vmess" {
			sb.WriteString(fmt.Sprintf("      AlterId:  %d\n", pc.GetAlterId()))
		}
		if pc.Flow != "" {
			sb.WriteString(fmt.Sprintf("      Flow:     %s\n", pc.Flow))
		}
		if pc.Encryption != "" {
			sb.WriteString(fmt.Sprintf("      Encryption: %s\n", pc.Encryption))
		}
	case "trojan":
		sb.WriteString(fmt.Sprintf("      Password: %s\n", maskSecret(pc.Password)))
		if pc.Flow != "" {
			sb.WriteString(fmt.Sprintf("      Flow:     %s\n", pc.Flow))
		}
	case "shadowsocks":
		sb.WriteString(fmt.Sprintf("      Method:   %s\n", pc.Method))
		sb.WriteString(fmt.Sprintf("      Password: %s\n", maskSecret(pc.Password)))
	case "hysteria":
		sb.WriteString(fmt.Sprintf("      Auth:     %s\n", maskSecret(pc.HysteriaAuth)))
		if pc.HysteriaUp != "" {
			sb.WriteString(fmt.Sprintf("      Up:       %s\n", pc.HysteriaUp))
		}
		if pc.HysteriaDown != "" {
			sb.WriteString(fmt.Sprintf("      Down:     %s\n", pc.HysteriaDown))
		}
		if pc.HysteriaPorts != "" {
			sb.WriteString(fmt.Sprintf("      Ports:    %s\n", pc.HysteriaPorts))
		}
		if pc.HysteriaObfs != "" {
			sb.WriteString(fmt.Sprintf("      Obfs:     %s\n", pc.HysteriaObfs))
		}
	}

	transport := pc.GetTransportType()
	sb.WriteString(fmt.Sprintf("      Transport: %s\n", transport))

	if transport == "ws" || transport == "httpupgrade" || transport == "splithttp" || transport == "xhttp" || transport == "h2" || transport == "http" {
		if pc.Path != "" {
			sb.WriteString(fmt.Sprintf("      Path:     %s\n", pc.Path))
		}
		if pc.Host != "" {
			sb.WriteString(fmt.Sprintf("      Host:     %s\n", pc.Host))
		}
		if pc.Mode != "" {
			sb.WriteString(fmt.Sprintf("      Mode:     %s\n", pc.Mode))
		}
		if pc.RawXhttpSettings != "" {
			sb.WriteString("      RawSettings: (present)\n")
		}
	}

	if transport == "grpc" {
		sb.WriteString(fmt.Sprintf("      ServiceName: %s\n", pc.GetServiceName()))
		if pc.MultiMode {
			sb.WriteString("      MultiMode:   true\n")
		}
	}

	if transport == "tcp" && pc.HeaderType != "" && pc.HeaderType != "none" {
		sb.WriteString(fmt.Sprintf("      HeaderType: %s\n", pc.HeaderType))
		if pc.HeaderType == "http" {
			if pc.Host != "" {
				sb.WriteString(fmt.Sprintf("      Host:     %s\n", pc.Host))
			}
			if pc.Path != "" {
				sb.WriteString(fmt.Sprintf("      Path:     %s\n", pc.Path))
			}
		}
	}

	security := pc.GetSecurityType()
	sb.WriteString(fmt.Sprintf("      Security: %s\n", security))

	if security == "tls" {
		if pc.SNI != "" {
			sb.WriteString(fmt.Sprintf("      SNI:      %s\n", pc.SNI))
		}
		if pc.Fingerprint != "" {
			sb.WriteString(fmt.Sprintf("      Fingerprint: %s\n", pc.Fingerprint))
		}
		if len(pc.ALPN) > 0 {
			sb.WriteString(fmt.Sprintf("      ALPN:     %s\n", strings.Join(pc.ALPN, ",")))
		}
		if pc.AllowInsecure {
			sb.WriteString("      AllowInsecure: true\n")
		}
	}

	if security == "reality" {
		if pc.SNI != "" {
			sb.WriteString(fmt.Sprintf("      SNI:       %s\n", pc.SNI))
		}
		if pc.Fingerprint != "" {
			sb.WriteString(fmt.Sprintf("      Fingerprint: %s\n", pc.Fingerprint))
		}
		if pc.PublicKey != "" {
			sb.WriteString(fmt.Sprintf("      PublicKey: %s\n", pc.PublicKey))
		}
		if pc.ShortID != "" {
			sb.WriteString(fmt.Sprintf("      ShortID:   %s\n", pc.ShortID))
		}
	}

	sb.WriteString(fmt.Sprintf("      StableID: %s\n", pc.StableID))

	return sb.String()
}

func maskSecret(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}
