package config

import "testing"

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
