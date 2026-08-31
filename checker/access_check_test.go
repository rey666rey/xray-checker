package checker

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"xray-checker/models"
)

type accessDialerFunc func(context.Context, string, string) (net.Conn, error)

func (f accessDialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

func sshPipeDialer() accessDialer {
	return accessDialerFunc(func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			_, _ = bufio.NewReader(server).ReadString('\n')
			_, _ = server.Write([]byte("SSH-2.0-OpenSSH_9.7\r\n"))
		}()
		return client, nil
	})
}

func httpPipeDialer(status, body string) accessDialer {
	return accessDialerFunc(func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			_, _ = http.ReadRequest(bufio.NewReader(server))
			_, _ = server.Write([]byte("HTTP/1.1 " + status + "\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\nConnection: close\r\n\r\n" + body))
		}()
		return client, nil
	})
}

func TestNormalizeAccessRequestDefaultsAndRejectsPrivateTargets(t *testing.T) {
	request, err := normalizeAccessRequest(AccessCheckRequest{IP: "203.0.113.10", Method: AccessMethodSSH})
	if err != nil {
		t.Fatal(err)
	}
	if request.Port != 22 || request.Path != "" {
		t.Fatalf("normalized request=%#v", request)
	}
	if _, err := normalizeAccessRequest(AccessCheckRequest{IP: "127.0.0.1", Port: 22, Method: AccessMethodSSH}); err == nil {
		t.Fatal("loopback target must be rejected")
	}
	if _, err := normalizeAccessRequest(AccessCheckRequest{IP: "10.0.0.1", Port: 443, Method: AccessMethodTLS}); err == nil {
		t.Fatal("private target must be rejected")
	}
}

func TestSSHAccessProbeRequiresProtocolBanner(t *testing.T) {
	result := runAccessProbe(context.Background(), sshPipeDialer(), AccessCheckRequest{
		IP: "203.0.113.10", Port: 22, Method: AccessMethodSSH,
	})
	if !result.Success || !result.TCPConnected || !result.ProtocolMatched {
		t.Fatalf("result=%#v", result)
	}
	if result.Banner != "SSH-2.0-OpenSSH_9.7" {
		t.Fatalf("banner=%q", result.Banner)
	}
}

func TestHTTPAccessProbeValidatesResponse(t *testing.T) {
	request := AccessCheckRequest{
		IP: "203.0.113.10", Port: 80, Method: AccessMethodHTTP,
		HTTPHost: "captive.apple.com", Path: "/hotspot-detect.html",
		ExpectedStatus: 200, ExpectedText: "Success",
	}
	result := runAccessProbe(context.Background(), httpPipeDialer("200 OK", "<HTML>Success</HTML>"), request)
	if !result.Success || !result.TCPConnected || !result.ProtocolMatched || result.StatusCode != 200 {
		t.Fatalf("result=%#v", result)
	}

	result = runAccessProbe(context.Background(), httpPipeDialer("302 Found", "moved"), request)
	if result.Success || !result.ProtocolMatched || result.StatusCode != 302 {
		t.Fatalf("mismatched result=%#v", result)
	}
}

func TestAccessCheckClassifiesDirectFailureAndVPNSuccessAsBlocked(t *testing.T) {
	proxy := &models.ProxyConfig{
		Index: 1, StableID: "stable-1", NodeID: "node-1", Name: "Germany", Server: "198.51.100.10", Port: 443,
	}
	checker := NewProxyChecker([]*models.ProxyConfig{proxy}, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	checker.results.Store(proxyMetricKey(proxy), proxyResult{status: true, latency: 10 * time.Millisecond, lastCheck: time.Now(), exitIP: "198.51.100.20"})
	checker.accessDialerFactory = func(route accessRoute) (accessDialer, error) {
		if route.kind == "direct" {
			return accessDialerFunc(func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("direct timeout")
			}), nil
		}
		return sshPipeDialer(), nil
	}

	run, err := checker.StartAccessCheck(AccessCheckRequest{IP: "203.0.113.20", Port: 22, Method: AccessMethodSSH})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := checker.GetAccessCheck(run.RunID)
		if ok && current.State == AccessCheckCompleted {
			if current.Verdict != AccessVerdictBlocked {
				t.Fatalf("verdict=%s result=%#v", current.Verdict, current)
			}
			if len(current.VPN) != 1 || current.VPN[0].Successes == 0 {
				t.Fatalf("VPN results=%#v", current.VPN)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("access check did not complete")
}

func TestAccessCheckHistoryPersistsAndInterruptsIncompleteRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access-checks.json")
	checker := NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	checker.accessFile = path
	checker.accessHistory = []AccessCheck{{RunID: "run-1", State: AccessCheckRunning, StartedAt: 1}}
	if err := checker.persistAccessChecks(); err != nil {
		t.Fatal(err)
	}

	restored := NewProxyChecker(nil, 10000, "", 1, "", "", 1, 1, "urltest", 1)
	if err := restored.SetAccessCheckFile(path); err != nil {
		t.Fatal(err)
	}
	history := restored.GetAccessCheckHistory()
	if len(history) != 1 || history[0].Verdict != AccessVerdictInterrupted || history[0].State != AccessCheckCompleted {
		t.Fatalf("history=%#v", history)
	}
}
