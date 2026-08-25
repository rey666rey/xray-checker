# Xray Checker — bulk proxy testing through an iPhone connection

This repository is configured for a specific job: quickly test a large Xray
subscription from a Russian mobile network while the Mac itself remains on
Wi-Fi.

The checker runs inside a dedicated Colima VM whose preferred route is bridged
to an iPhone connected over USB. It loads every proxy from the configured
subscriptions, tests them in bounded batches, retries first-pass failures, and
keeps the completed snapshot available in a local dashboard.

[Русская версия](README_RU.md)

## What the check means

This is not an ICMP ping.

For each VLESS, VMess, Trojan, or Shadowsocks node, Xray exposes a local SOCKS5
port. The checker sends an HTTP `GET` to `api.ipify.org` through that port and
measures time to first response byte. A node is online when the request succeeds
and the returned public IP differs from the checker's direct public IP.

The connection to every proxy server originates from the iPhone's mobile
network. `api.ipify.org` is only the destination used to confirm that traffic
actually passed through the proxy; it does not determine the source network of
the test.

The default run is designed for large subscriptions:

- all nodes are checked once, in batches of 20;
- only failed nodes are retried, in batches of 10;
- any successful retry replaces the first failed result;
- the final snapshot stays in memory without scheduled full rechecks;
- restarting the checker starts a new full run.

An offline result can mean that the proxy is unavailable from the current
mobile network, the request timed out, the proxy could not reach the test URL,
or the mobile connection was interrupted during the check.

## Network layout

```text
Mac dashboard: 127.0.0.1:2112
        │
        └── Colima profile: iphone
                │ preferred route: col0
                └── iPhone USB tethering
                        │
                        └── Russian mobile network → proxy server → api.ipify.org
```

Only the Colima VM and its containers use the iPhone route. The Mac's default
route can remain on Wi-Fi.

## Requirements

- an Apple Silicon Mac running macOS;
- Colima and the Docker CLI;
- Docker Compose, either the Docker plugin or `docker-compose`;
- an iPhone connected over USB and trusted by the Mac;
- Personal Hotspot enabled on the iPhone;
- subscription URL(s) and the HWID expected by the subscription service.

The included Compose build targets `linux/arm64`.

The command examples below use the standalone `docker-compose` binary. If only
the Docker Compose plugin is installed, replace
`docker-compose --context colima-iphone` with
`docker --context colima-iphone compose`.

## First-time setup

Create the local environment file:

```bash
cp .env.example .env
```

Edit `.env` and replace both placeholders:

```dotenv
SUBSCRIPTION_URL="https://example.com/subscription#GROUP"
HWID="your-device-id"
```

Multiple subscriptions are comma-separated:

```dotenv
SUBSCRIPTION_URL="https://example.com/one#ONE,https://example.com/two#TWO"
```

The fragment after `#` becomes the group name in the dashboard and is not sent
to the subscription server. Keep the complete value quoted, otherwise `.env`
will treat `#` as a comment.

`.env` is ignored by Git. Do not commit real subscription URLs or HWIDs.

## Find the iPhone interface

Connect and unlock the iPhone, enable Personal Hotspot, then run:

```bash
networksetup -listallhardwareports
```

Find the iPhone USB entry and note its `Device`, commonly `en7`. The start
script uses `en7` by default.

## Start

With the iPhone connected:

```bash
./start.sh
```

For a different USB interface:

```bash
IPHONE_INTERFACE=en8 ./start.sh
```

The script:

1. verifies that `.env`, Colima, Docker, Compose, and the iPhone interface exist;
2. stops the existing `iphone` Colima profile if necessary;
3. starts a bridged vmnet daemon bound to the iPhone USB interface;
4. starts Colima and verifies that its preferred IPv4 route uses `col0`;
5. builds and starts Xray Checker plus its route-status sidecar in the
   `colima-iphone` Docker context;
6. installs a macOS LaunchAgent that recreates the Colima bridge if an attached
   iPhone does not recover normally.

macOS may request administrator approval when the vmnet component is configured
for the first time.

Open the dashboard after startup:

<http://127.0.0.1:2112>

The page becomes available while the initial run is still filling in results.

Every regular `./start.sh` invocation removes the previous run's snapshot and
checks every node from every subscription again. Completed results remain fixed
while the container is active and are persisted only so an iPhone connection
recovery can restore them safely.

## Watch the run

Container state:

```bash
docker-compose --context colima-iphone ps
```

Live logs:

```bash
docker-compose --context colima-iphone logs -f xray-checker
```

Health check:

```bash
curl -fsS http://127.0.0.1:2112/health
```

The `Auto` button in the top-right corner only refreshes values already exposed
by the API. It does not start another proxy test. In the default one-pass mode,
the displayed results remain unchanged after the run completes.

The pill beside `Auto` shows the live iPhone route state and mobile public IP:

- green — the mobile route is ready;
- yellow — the USB interface is present and the route is recovering;
- red — the checker is waiting for the iPhone;
- `Monitor offline` — the sidecar stopped updating its status file.

The monitor sidecar can also be inspected directly:

```bash
docker-compose --context colima-iphone logs -f network-monitor
```

## Start a new full check

A regular start always begins a new full check:

```bash
./start.sh
```

A direct `docker restart` and automatic iPhone recovery instead restore the
saved snapshot and do not repeat all 844 checks.

After changing source code, `.env`, or `compose.yaml`, rebuild it:

```bash
docker-compose --context colima-iphone up -d --build --force-recreate
```

Unplugging the iPhone or toggling Wi-Fi on it no longer requires a manual
restart. Proxy requests pause first. If vmnet reuses its lease normally, the
dashboard stays available throughout. If the old `col0` bridge is stuck, the
macOS supervisor detects the attached phone after about 15 seconds and recreates
Colima automatically. In that fallback case the dashboard can be unavailable
briefly, then returns with the saved results instead of starting a new sweep.

## Stop everything

```bash
./stop.sh
```

This unloads the macOS recovery supervisor, stops the connection monitor,
removes the Compose containers, stops the `iphone` Colima VM, and stops its
bridge daemon. The named `xray-geo` and `xray-results` volumes are retained.

To stop only the checker while leaving Colima running:

```bash
docker-compose --context colima-iphone stop xray-checker
```

## Current checking configuration

The local defaults live in [`compose.yaml`](compose.yaml):

| Setting | Value | Purpose |
| --- | --- | --- |
| `SUBSCRIPTION_UPDATE` | `false` | Keep the initially loaded proxy set |
| `SUBSCRIPTION_JSON_FORMAT` | `true` | Request complete JSON proxy configs |
| `PROXY_CHECK_CONCURRENCY` | `30` | Limit the fast first-pass batch size |
| `PROXY_CHECK_INTERVAL` | `600` | UI Auto-refresh interval; check interval when one-pass mode is disabled |
| `PROXY_INITIAL_CHECK_ONLY` | `true` | Run one full check after startup |
| `PROXY_CHECK_METHOD` | `urltest` | Run two fast proxied GETs and retain the best result |
| `PROXY_URL_TEST_URL` | `http://captive.apple.com/hotspot-detect.html` | Primary tiny Apple page |
| `PROXY_URL_TEST_ATTEMPTS` | `2` | Number of fast attempts |
| `PROXY_IP_CHECK_URL` | `https://api.ipify.org?format=text` | Fallback only when Apple fails |
| `PROXY_TIMEOUT` | `4` | Fast-attempt timeout in seconds |
| `PROXY_RETRY_TIMEOUT` | `10` | Failed-node retry timeout |
| `PROXY_RETRY_CONCURRENCY` | `10` | Failed-node retry concurrency |
| `NETWORK_STATUS_FILE` | `/app/runtime/network-status.json` | Pause checks when the host monitor reports an outage |
| `NETWORK_STATUS_MAX_AGE` | `15` | Treat a silent monitor as offline after 15 seconds |
| `RESULTS_FILE` | `/app/data/results.json` | Persist a completed sweep across checker/Colima restarts |

If both fast Apple requests fail, the checker tries `ipify`. A node that succeeds
only through this fallback or the retry pass is shown in yellow as `unstable`.
Remaining failures are checked once more in a separate slower batch.

If the mobile link produces many `EOF` or timeout errors, lower concurrency. If
the run is stable but too slow, increase it gradually. A larger value creates
more simultaneous Xray connections and more load on the iPhone's radio and NAT.

All supported environment variables are documented in
[`docs/src/content/docs/configuration/envs.md`](docs/src/content/docs/configuration/envs.md).

## Useful endpoints

| URL | Description |
| --- | --- |
| `/` | Local dashboard |
| `/health` | Process health; returns `OK` |
| `/metrics` | Prometheus metrics |
| `/api/v1/public/proxies` | Current proxy snapshot used by Auto refresh |
| `/api/v1/status` | Aggregate checker status |
| `/api/v1/config` | Active non-secret configuration |
| `/api/v1/network` | iPhone route state and current mobile IP |
| `/api/v1/docs` | Interactive OpenAPI documentation |

The Compose port is bound to `127.0.0.1`, so the dashboard is not exposed to
other devices on the local network.

## Troubleshooting

### The script says the iPhone has no IPv4 address

Unlock the iPhone, confirm that the Mac is trusted, enable Personal Hotspot, and
reconnect the USB cable. Check the interface name again and pass it with
`IPHONE_INTERFACE` if it changed.

### The dashboard says Waiting or Recovering

No proxy checks are discarded while the mobile route is unavailable. Reconnect
and unlock the iPhone. If normal vmnet recovery fails, the macOS supervisor
restarts Colima automatically. Its log is `.runtime/iphone-supervisor.log`. If
the state still does not turn green, inspect the `network-monitor` logs and
confirm the interface name.

### Colima starts but the checker does not

Inspect the VM routes:

```bash
colima ssh --profile iphone -- ip -4 route show default
```

The preferred route must contain `dev col0`. `start.sh` intentionally refuses
to launch the checker when that route is missing, because the test would
otherwise use the wrong network.

### Confirm that Mac and checker use different exits

```bash
curl -fsS https://api.ipify.org
docker-compose --context colima-iphone exec xray-checker \
  curl -fsS https://api.ipify.org
```

The first address should be the Mac's Wi-Fi exit; the second should be the
iPhone mobile exit.

### Results disappear

Results are held in memory. Restarting or recreating the checker intentionally
clears them and starts a new full pass.

### Many nodes are offline at once

Check that the iPhone stayed connected for the entire run and review the logs.
If failures are mostly timeouts or connection resets, lower
`PROXY_CHECK_CONCURRENCY` or raise `PROXY_TIMEOUT`, rebuild the container, and
run again.

## Development

Run the Go test suite:

```bash
go test ./...
```

Validate the operational scripts and Compose file:

```bash
bash -n start.sh stop.sh
docker-compose --context colima-iphone config --quiet
```

## Project origin

This repository is based on
[`kutovoys/xray-checker`](https://github.com/kutovoys/xray-checker) and retains
its license. The local changes focus on stable identities for duplicate names,
bounded bulk checks, failed-node retries, one-pass snapshots, and an isolated
iPhone-routed Colima setup.
