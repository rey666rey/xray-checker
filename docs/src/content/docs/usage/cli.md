---
title: CLI
description: CLI usage of Xray Checker
---

### Basic Command Line Usage

The CLI interface provides complete control over Xray Checker's functionality through command-line arguments.

### Installation

Download the latest binary from releases:

```bash
# For Linux amd64
curl -Lo xray-checker https://github.com/kutovoys/xray-checker/releases/latest/download/xray-checker_linux_amd64
chmod +x xray-checker

# For Linux arm64
curl -Lo xray-checker https://github.com/kutovoys/xray-checker/releases/latest/download/xray-checker_linux_arm64
chmod +x xray-checker
```

### Basic Usage

Minimum required configuration:

```bash
./xray-checker --subscription-url="https://your-subscription-url/sub"
```

### Full Configuration Example

```bash
./xray-checker \
  --subscription-url="https://your-subscription-url/sub" \
  --subscription-update=true \
  --subscription-update-interval=300 \
  --proxy-check-interval=300 \
  --proxy-timeout=5 \
  --proxy-check-method=ip \
  --proxy-ip-check-url="https://api.ipify.org?format=text" \
  --proxy-status-check-url="http://cp.cloudflare.com/generate_204" \
  --simulate-latency=true \
  --xray-start-port=10000 \
  --xray-log-level=none \
  --metrics-port=2112 \
  --metrics-protected=true \
  --metrics-username=custom_user \
  --metrics-password=custom_pass \
  --metrics-instance=node-1 \
  --metrics-push-url="https://push.example.com" \
  --metrics-base-url="/xray/monitor" \
  --run-once=false
```

### Common CLI Operations

Check version:

```bash
./xray-checker --version
```

Run single check cycle:

```bash
./xray-checker --subscription-url="https://your-sub-url" --run-once
```

Enable metrics authentication:

```bash
./xray-checker \
  --subscription-url="https://your-sub-url" \
  --metrics-protected=true \
  --metrics-username=user \
  --metrics-password=pass
```

Change ports:

```bash
./xray-checker \
  --subscription-url="https://your-sub-url" \
  --metrics-port=3000 \
  --xray-start-port=20000
```
