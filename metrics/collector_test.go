package metrics

import (
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

type fakeSource struct{ pms []ProxyMetric }

func (f fakeSource) MetricsSnapshot() []ProxyMetric { return f.pms }

// gatherSeries registers the collector and returns, per metric family name, one
// sorted "name=value,name=value" string per series.
func gatherSeries(t *testing.T, c prometheus.Collector) map[string][]string {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := make(map[string][]string)
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			var parts []string
			for _, l := range m.GetLabel() {
				parts = append(parts, l.GetName()+"="+l.GetValue())
			}
			sort.Strings(parts)
			out[mf.GetName()] = append(out[mf.GetName()], strings.Join(parts, ","))
		}
	}
	return out
}

func TestCollectorCustomLabels(t *testing.T) {
	src := fakeSource{pms: []ProxyMetric{
		{
			Protocol: "trojan", Address: "1.1.1.1:443", Name: "A", SubName: "s", StableID: "id1",
			CustomLabels: map[string]string{"location": "NL", "hoster": "FreeVDS"},
			Online:       true, LatencyMs: 227,
		},
		{
			Protocol: "vless", Address: "2.2.2.2:443", Name: "B", SubName: "s", StableID: "id2", GroupName: "g",
			Online: false, LatencyMs: 0,
		},
	}}
	c := NewCollector("", src)

	got := gatherSeries(t, c)
	if len(got["xray_proxy_status"]) != 2 || len(got["xray_proxy_latency_ms"]) != 2 {
		t.Fatalf("expected 2 series per family, got %v", got)
	}

	joined := strings.Join(got["xray_proxy_status"], "\n")
	if !strings.Contains(joined, "hoster=FreeVDS") || !strings.Contains(joined, "location=NL") {
		t.Errorf("custom labels missing from series:\n%s", joined)
	}
	// A series with custom labels (id1) and one without (id2) coexist in one family.
	if !strings.Contains(joined, "stable_id=id1") || !strings.Contains(joined, "stable_id=id2") {
		t.Errorf("expected both proxies present:\n%s", joined)
	}
}

func TestCollectorInstanceAndSanitizeAndReserved(t *testing.T) {
	src := fakeSource{pms: []ProxyMetric{
		{
			Protocol: "trojan", Address: "1.1.1.1:443", Name: "A", SubName: "s", StableID: "id1",
			CustomLabels: map[string]string{
				"data center": "dc1",   // space -> underscore
				"1region":     "eu",    // leading digit -> _1region
				"protocol":    "x",     // reserved -> skipped
				"instance":    "spoof", // reserved -> skipped
			},
			Online: true, LatencyMs: 1,
		},
	}}
	c := NewCollector("node-1", src)

	got := gatherSeries(t, c)
	if len(got["xray_proxy_status"]) != 1 {
		t.Fatalf("expected 1 series, got %v", got)
	}
	s := got["xray_proxy_status"][0]

	if !strings.Contains(s, "instance=node-1") {
		t.Errorf("instance label missing: %s", s)
	}
	if !strings.Contains(s, "data_center=dc1") {
		t.Errorf("space key should be sanitized to data_center: %s", s)
	}
	if !strings.Contains(s, "_1region=eu") {
		t.Errorf("leading-digit key should become _1region: %s", s)
	}
	// reserved keys keep their real values, the custom override is dropped
	if !strings.Contains(s, "protocol=trojan") || strings.Contains(s, "protocol=x") {
		t.Errorf("reserved protocol must not be overridden: %s", s)
	}
	if strings.Contains(s, "instance=spoof") {
		t.Errorf("reserved instance must not be overridden: %s", s)
	}
}

func TestSanitizeLabelName(t *testing.T) {
	cases := map[string]string{
		"location":    "location",
		"data center": "data_center",
		"1region":     "_1region",
		"a-b.c":       "a_b_c",
		"":            "",
		"123":         "_123",
	}
	for in, want := range cases {
		if got := sanitizeLabelName(in); got != want {
			t.Errorf("sanitizeLabelName(%q)=%q, want %q", in, got, want)
		}
	}
}
