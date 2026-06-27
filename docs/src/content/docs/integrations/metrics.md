---
title: Metrics
description: Metrics options and examples
---

Xray Checker provides two Prometheus metrics for monitoring proxy status and performance. For detailed setup instructions, see [Prometheus integration](/integrations/prometheus).

For metric visualization, we recommend using [Grafana](/integrations/grafana).

### xray_proxy_status

Status metric indicating proxy availability:

- Type: Gauge
- Values: 1 (working) or 0 (failed)
- Labels:
  - `protocol`: Proxy protocol (vless/vmess/trojan/shadowsocks/hysteria/socks/http)
  - `address`: Server address and port
  - `name`: Proxy configuration name
  - `stable_id`: Stable per-proxy identifier; keeps each series distinct even when names collide, and stays the same across restarts/reorders
  - `sub_name`: Subscription name (parsed from URL fragment or profile-title header)
  - `group_name`: Balancer/group name for grouped proxies (empty for ungrouped)
  - `instance`: Instance name (if configured)

:::tip
See [advanced configuration](/configuration/advanced-conf#instance-labeling) for instance labeling setup.
:::

Example:

```text
# HELP xray_proxy_status Status of proxy connection (1: success, 0: failure)
# TYPE xray_proxy_status gauge
xray_proxy_status{protocol="vless",address="example.com:443",name="proxy1",stable_id="a1b2c3d4e5f67890",sub_name="Premium VPN",group_name="",instance="dc1"} 1
```

### xray_proxy_latency_ms

Latency metric showing connection response time:

- Type: Gauge
- Values: Milliseconds (0 if failed)
- Labels: Same as xray_proxy_status

Example:

```text
# HELP xray_proxy_latency_ms Latency of proxy connection in milliseconds
# TYPE xray_proxy_latency_ms gauge
xray_proxy_latency_ms{protocol="vless",address="example.com:443",name="proxy1",stable_id="a1b2c3d4e5f67890",sub_name="Premium VPN",group_name="",instance="dc1"} 156
```
