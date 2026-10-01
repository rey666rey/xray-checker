package config

import (
	"os"
	"reflect"
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
	Parse("test")

	if want := []string{"LTE", "TEST"}; !reflect.DeepEqual(CLIConfig.Proxy.ExcludeNamePrefixes, want) {
		t.Fatalf("exclude prefixes = %#v, want %#v", CLIConfig.Proxy.ExcludeNamePrefixes, want)
	}
}
