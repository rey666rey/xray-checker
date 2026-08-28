# Xray Checker — large-subscription monitoring over an iPhone network

Xray Checker tests hundreds of proxy bindings from a Russian mobile network and
shows which servers work, have become unstable, changed address, or probably
need replacement. The Mac keeps using its normal Wi-Fi connection: the mobile
route is isolated inside a dedicated Colima virtual machine.

[Русская версия](README_RU.md)

## Purpose

This repository is tailored to the operational workflow of maintaining Xray
subscriptions:

- test every binding specifically from the iPhone mobile network;
- discover new and changed subscription endpoints quickly;
- distinguish one transient network failure from a repeatable problem;
- retain the previous and current address after a replacement;
- manually recheck one binding or deeply diagnose a physical node;
- preserve results across a short iPhone disconnect or Colima recreation;
- optionally expose the current state to Prometheus.

This is not ICMP ping, a Cloudflare test, `ipify`, or a block-list lookup. The
checker makes a real HTTP request through every Xray configuration and answers a
practical question: “can this binding open the control URL from the network used
by Colima right now?”

## Check flow at a glance

1. `start.sh` attaches a dedicated Colima `iphone` profile to the iPhone USB
   interface.
2. The Go application downloads subscriptions and builds the Xray configuration.
3. Xray exposes one local SOCKS5 port per checkable binding.
4. Through every SOCKS5 port, the Go client sends two HTTP `GET` requests to
   `captive.apple.com/hotspot-detect.html`.
5. At least one valid response containing `Success` means that the binding
   works. The best time-to-first-byte is retained as latency.
6. The result becomes available to the dashboard, JSON API, repair history, and
   Prometheus collector.
7. After the full sweep, no new bulk sweep runs automatically. Per-binding
   schedules and subscription changes drive subsequent checks.

## Architecture

```text
macOS (the Mac keeps its normal Wi-Fi default route)
│
├── start.sh / stop.sh / iphone-supervisor.sh
│
└── Colima profile: iphone
    │ default route: col0 → iPhone USB → mobile network
    │
    ├── network-monitor
    │   └── direct captive.apple.com GET bound to col0
    │
    └── xray-checker (Go)
        ├── downloads subscriptions
        ├── starts and reloads Xray
        ├── SOCKS5 :10000 + index for every binding
        ├── GET: mobile network → proxy → captive.apple.com
        ├── dashboard and JSON API on 127.0.0.1:2112
        └── /metrics for an external Prometheus server
```

Only the virtual machine and its containers use the iPhone path. The Mac does
not switch all of its traffic to the mobile connection.

## Components

### Go application: `xray-checker`

The primary process is written in Go. It:

- fetches and parses subscriptions;
- builds the `Host ↔ Node` topology;
- starts Xray and safely reloads its configuration;
- schedules bulk, targeted, and manual checks;
- stores current results, repair history, and diagnoses;
- serves the HTML dashboard, JSON API, and `/metrics`.

### Xray

Xray implements VLESS, Reality, TLS, Hysteria, and the other proxy protocols.
The checker does not try to reproduce those protocols. Xray creates a local
SOCKS5 listener for each configuration, and Go sends the control HTTP request
through that listener.

### `network-monitor`

A separate container tests the Apple URL directly through `col0` every three
seconds. It writes `connected`, `recovering`, or `waiting` into a shared status
file. If the iPhone disappears, the Go process pauses new checks instead of
turning a cable disconnect into hundreds of false `Offline` results.

When a proxy request fails at the same time as the mobile route, the checker
waits for confirmation from `network-monitor`, waits for recovery, and repeats
the request without recording a false failure.

### `iphone-supervisor.sh`

This macOS LaunchAgent provides last-resort recovery. If the iPhone is attached
but `col0` has not returned for about 15 seconds, the supervisor recreates the
Colima bridge and profile. Recovery attempts have a 60-second cooldown.

Recovery mode keeps the saved result snapshot, so the dashboard returns without
another full sweep.

## Host, Node, and Binding

The checker stores a many-to-many relationship:

- **Host** is a subscription entry/inbound. Its identity includes its name,
  subscription, protocol, transport, and security, but excludes the server IP.
- **Node** is a physical endpoint grouped by IP/hostname. `NodeID` excludes the
  port, allowing several inbound ports on one machine to share a node.
- **Binding** is one checkable `Host + Node + Port` edge.

This makes both of these normal:

- several similarly named Reality/TLS/Hysteria hosts use one node;
- one host rotates through several nodes in a server-side pool.

Health results belong to bindings because separate inbounds on the same machine
can behave differently. `StableID` prevents same-named cards from being mixed up.

The dashboard has a single `Hosts` view. It displays bindings under their
subscription entries and groups. The internal Node model remains available to
the backend: node diagnosis, grouped rechecks, alert grouping, and shared-IP
tracking depend on it even though there is no separate Nodes tab.

## Subscription discovery

Subscriptions are read once at startup to create the Xray configuration. A
refresh cycle then runs every 60 seconds.

Every cycle:

1. fetches every subscription source;
2. takes four independent snapshots back-to-back;
3. merges the endpoints observed for each host;
4. compares the resulting topology with the running set;
5. reloads Xray only when a real change exists;
6. immediately tests up to 50 new and changed bindings;
7. separately resolves hostname endpoints and reacts to DNS changes.

Multiple subscription sources are fetched concurrently. The local profile uses
JSON mode and supplies `X-Hwid`, allowing compatible panels to return full
configs and individual balancer outbounds.

If one refresh discovers more than 50 affected bindings, the remainder stays
due and is processed by the scheduler in batches of up to 50. This avoids one
large panel change overwhelming the mobile link.

### Can one request reveal every node attached to a host?

It depends on what the panel returns:

- if the JSON response contains every balancer outbound, one response is enough;
- if the subscription server randomly selects one IP and returns only that IP,
  the other members are absent from the payload and cannot be inferred in one
  request.

For a random pool, four snapshots per minute act as sampling. Every observed
`Host + Endpoint` is accumulated. An evenly distributed ten-node pool is
usually discovered in roughly 7–8 minutes, but completeness cannot be proven:
a low-weight member may take much longer to appear. A deterministic list
requires a separate panel API or database that exposes balancer membership.

An endpoint is detached after 30 successful minute cycles without a sighting.
After the first missed cycle it is marked missing and hidden from the active
dashboard list. If the exact binding returns, its history and last state are
restored and a fresh check is scheduled.

### Large-diff protection

If one response changes at least 100 existing configurations without ordinary
additions or removals, it is not applied immediately. The same mass revision
must appear in two consecutive refresh cycles. This protects the running Xray
configuration from a temporarily incomplete or corrupted panel response.

## Regular Apple URL Test

Every binding has a dedicated local Xray SOCKS5 port. The Go client disables
keep-alive and sends two independent HTTP requests.

A response is valid only when:

- the HTTP status is `2xx`;
- the body contains the expected `Success` marker;
- the request travelled through the binding's SOCKS5 port.

Interpretation:

- `2/2` is a clean success;
- `1/2` is online but `Unstable`;
- `0/2` is a failure;
- a binding that fails the first bulk pass but succeeds in the slower retry pass
  is also `Unstable`.

Latency is the best time-to-first-byte from successful requests, not an ICMP
round-trip time. Apple's location does not change the test origin: the proxy
connection begins on the iPhone mobile network.

## Full sweep

A normal `./start.sh` removes only `results.json`, builds the image, and starts a
new full check of every current binding.

The sweep works as follows:

- the first queue runs up to 30 bindings concurrently;
- each binding gets two fast Apple attempts with a four-second timeout;
- only first-pass failures enter the retry queue;
- retry uses concurrency 10 and a ten-second timeout per attempt;
- a successful retry replaces the initial failed result;
- the completed snapshot is written atomically to a Docker volume.

The dashboard and API become available while the sweep is still filling in.
After completion, targeted monitoring continues without another bulk sweep.

## Targeted schedule after the sweep

The scheduler wakes every ten seconds, selects up to 50 due bindings, and runs
them with bounded concurrency.

| Current state | Next check |
| --- | --- |
| `Healthy` / `Fixed` | 12–18 minutes, deterministically staggered by ID |
| `Unstable` | approximately 4:30–5:30 |
| first consecutive failure | 1 minute |
| second consecutive failure | 2 minutes |
| third failure, `Needs replacement` | 5 minutes |
| fourth and later failures | every 10 minutes |
| first clean check after a revision change | 45 seconds |
| partial success after a revision change | 1 minute |

A previously healthy node is therefore usually discovered in less than half of
the roughly 15-minute period and in at most about 18 minutes. `Needs replacement`
requires the two follow-up confirmations, adding approximately three minutes.

## Monitor states

| State | Meaning |
| --- | --- |
| `Unknown` | the binding has no result yet |
| `Healthy` | the latest regular check completed cleanly |
| `Unstable` | only some attempts passed or the retry pass was required |
| `Suspected` | one or two consecutive failed rounds; still being confirmed |
| `Needs replacement` | at least three consecutive failed rounds |
| `IP changed` | the endpoint/credential/transport revision or resolved DNS changed |
| `Verifying new IP` | the replacement configuration is being confirmed |
| `Fixed` | two clean successes followed a configuration change |
| `New IP failed` | the replacement configuration failed repeatedly |
| `Missing` | the binding was absent from the latest successful subscription cycle |

`IP changed` is a historical label: a revision can change because of a port,
credential, Reality/TLS option, or transport even when the IP stays the same.

## Manual Re-check

`Re-check now` starts a real check immediately and does not wait behind a bulk
sweep. The API returns only after the result is ready, so `Rechecking…` describes
actual work instead of a queued job.

Concurrent requests for the same binding are serialized. A background check and
a manual check cannot race and overwrite each other's result.

At Node level, every attached inbound can be rechecked together. A single
targeted run is capped at 50 bindings.

## Deep Node diagnosis

`Diagnose` is a separate manual procedure. It does not overwrite regular health
and never runs automatically.

Stages:

1. verify the iPhone route and direct Apple control request;
2. make three direct attempts to the endpoint ports;
3. probe configured TLS/Reality SNI handshakes;
4. make three real Apple GETs through each Xray binding.

Only one deep diagnosis can run at a time. It takes priority over new background
batches. The last ten runs per Node are retained; a report for an older
configuration is marked `Stale`.

| Verdict | Meaning |
| --- | --- |
| `Healthy` | every binding passed all three tunnel attempts |
| `Degraded` | at least one binding works, but not every binding achieved `3/3` |
| `Port filtered` | the control network works, but every TCP endpoint attempt failed |
| `Handshake failed` | TCP is reachable, but TLS/Reality handshakes did not complete |
| `Tunnel failed` | the endpoint is reachable, but no Xray binding carried the request |
| `Inconclusive` | the route/control failed, the run was interrupted, or no reliable classification is possible |

`Degraded` is not equivalent to blocked. It means partial operation or
instability; inspect the individual binding attempt counters.

## Dashboard

Open <http://127.0.0.1:2112>.

- `Auto` refreshes API data every five seconds; it does not launch checks.
- Reload the page once after HTML/CSS/JavaScript changes.
- The iPhone pill reports `Connected`, `Recovering`, `Waiting`, or
  `Monitor offline`.
- Clicking a host name copies the complete name instead of opening `/config`.
- `Copy IP` copies the address without its port.
- Cards show local last-check time as `HH:MM`.
- `IP changed` uses a full-width wrapping row for the old and new addresses.
- `Hosts` is the only dashboard view and displays subscription entries.

Compose binds the port to `127.0.0.1`, so the dashboard is not exposed to other
devices on the LAN by default.

### Telegram alerts

The paper-plane button in the top-right corner opens the Telegram setup wizard:

1. create a bot with BotFather and paste its API token;
2. validate the token;
3. send `/start` to the bot and let the checker discover the private chat or
   group;
4. save the recipient and send a test message.

The stored token is never returned to the browser, placed in `localStorage`, or
included in logs. It is written separately with mode `0600` in the persistent
data volume. The UI can enable alerts for confirmed failures, IP changes,
recoveries, failed replacement IPs, unstable nodes, and iPhone route recovery.

Notifications are transition-based and grouped by physical endpoint. A single
message lists all affected subscription hosts/inbounds on that IP. First and
second ordinary failures remain silent; the critical notification follows the
existing three-failure `needs_replacement` threshold. Pending messages and
deduplication state survive checker restarts.

Alerts use compact Russian-language HTML cards. IP addresses are rendered as
copy-friendly monospace text, technical failures are reduced to short causes,
and IP replacement has distinct `IP changed`, `New IP works`, and `New IP
failed` outcomes. Multiple events inside the batch window become one digest,
grouped by critical, unstable, changed, and recovered nodes. Detailed cards show
at most three related host names and collapse the remainder into a `+ N more`
counter.

Telegram delivery defaults to `Auto via healthy Xray nodes`. The alert worker
reads the latest completed checker snapshot, picks up to three different
physical nodes, and sends the Bot API request through their already-running
local SOCKS inbounds. The last successful route is preferred; a failed route is
temporarily cooled down and the next node is tried. When an alert concerns a
node, that node is excluded from its own delivery candidates.

This delivery path is isolated from health checking: it does not start checks,
change results, acquire a checker worker slot, or wait in the check queue. If no
healthy route is available, the message remains in the persistent queue until a
later retry. The UI also offers `Direct` and a separate custom HTTP(S)/SOCKS5
proxy. Custom proxy credentials are stored in a separate `0600` file and are
never returned to the browser.

## Go and Prometheus

Prometheus does not perform proxy checks. The Go application checks proxies,
keeps the latest result, and exposes that snapshot in Prometheus format at
`/metrics`.

Primary gauges:

```prometheus
xray_proxy_status{protocol="vless",address="185.186.78.215:443",name="Sweden",stable_id="..."} 1
xray_proxy_latency_ms{protocol="vless",address="185.186.78.215:443",name="Sweden",stable_id="..."} 1187
```

- `xray_proxy_status`: `1` online, `0` offline;
- `xray_proxy_latency_ms`: latest latency, `0` on failure;
- labels include `protocol`, `address`, `name`, `sub_name`, `stable_id`,
  `group_name`, optional `instance`, and safe custom `metricsLabels`.

On every scrape, Go renders metrics from the current binding list. A scrape does
not ping a server or change its schedule. If Prometheus scrapes every 15 seconds,
it receives the same stored value between real checks.

This Compose profile does not include a Prometheus or Grafana server. The
checker only provides a compatible endpoint. An external Prometheus can pull
from `127.0.0.1:2112/metrics`. The code also supports `METRICS_PUSH_URL`, but the
local profile does not configure it.

The dashboard is independent from Prometheus and reads the JSON API directly.

## Persistence

The named `xray-results` volume is mounted at `/app/data`.

| File | Contents | Normal `./start.sh` |
| --- | --- | --- |
| `results.json` | last online/latency/error per binding and completed-sweep flag | removed to force a new full sweep |
| `node-history.json` | repair states, revisions, and the latest 40 events | preserved |
| `node-diagnostics.json` | latest ten manual diagnoses per Node | preserved |
| `telegram-settings.json` | non-secret Telegram recipient and preferences | preserved |
| `telegram-settings.token` | Telegram bot token, mode `0600` | preserved |
| `telegram-settings.proxy` | optional custom Telegram proxy, mode `0600` | preserved |
| `telegram-settings.state.json` | deduplication and pending alert queue | preserved |

Snapshots use a temporary file and atomic rename. Stopping Compose without
deleting named volumes does not erase the history.

## Requirements

- Apple Silicon Mac;
- trusted iPhone USB connection on macOS;
- iPhone Personal Hotspot enabled;
- Colima;
- Docker CLI;
- Docker Compose plugin or standalone `docker-compose`;
- subscription URLs and an `X-Hwid` value.

The local Compose profile targets `linux/arm64`.

## Initial setup

```bash
cp .env.example .env
```

Fill in `.env`:

```dotenv
SUBSCRIPTION_URL="https://example.com/one#ONE,https://example.com/two#TWO"
HWID="your-device-id"
```

The fragment after `#` becomes the dashboard subscription name and is not sent
to the server. Keep the value quoted; otherwise `.env` treats `#` as a comment.

`.env` is ignored by Git. Never commit real subscription URLs or HWIDs.

## Find the iPhone USB interface

Attach and unlock the iPhone, enable Personal Hotspot, then run:

```bash
networksetup -listallhardwareports
```

Find the `iPhone USB` block and its `Device`, commonly `en7`. Override the
default when your interface has another name.

## Start

Normal start with a new full sweep:

```bash
./start.sh
```

Use another iPhone interface:

```bash
IPHONE_INTERFACE=en8 ./start.sh
```

`start.sh`:

1. validates `.env`, Colima, Docker, Compose, and the iPhone IPv4 address;
2. stops the old `iphone` profile;
3. starts bridged vmnet on the selected USB interface;
4. verifies that the VM default route uses `col0`;
5. builds the image;
6. removes only the previous `results.json`;
7. starts `network-monitor` and `xray-checker`;
8. installs the recovery LaunchAgent.

Dashboard: <http://127.0.0.1:2112>

## Restart and recovery semantics

Different operations intentionally behave differently:

| Operation | Full sweep |
| --- | --- |
| `./start.sh` | yes; deletes `results.json` |
| automatic recovery after the iPhone returns | no; restores the snapshot |
| `docker restart xray-checker-xray-checker-1` | no; uses the volume |
| `docker-compose ... up -d --build --force-recreate` | no when a complete matching snapshot exists |
| add/change a subscription binding | checks only affected bindings |

If a saved snapshot is incomplete or the current binding set has changed, the
checker resumes only the missing portion instead of repeating completed work.

## Stop

Stop containers, Colima, the vmnet daemon, and the LaunchAgent:

```bash
./stop.sh
```

Named volumes remain intact.

Stop only the Go checker:

```bash
docker-compose --context colima-iphone stop xray-checker
```

## Observe the running system

```bash
docker-compose --context colima-iphone ps
docker-compose --context colima-iphone logs -f xray-checker
docker-compose --context colima-iphone logs -f network-monitor
curl -fsS http://127.0.0.1:2112/health
curl -fsS http://127.0.0.1:2112/api/v1/network
curl -fsS http://127.0.0.1:2112/api/v1/status
```

With the Compose plugin, use:

```bash
docker --context colima-iphone compose ps
```

## Active local settings

Values are defined in [`compose.yaml`](compose.yaml).

| Variable | Value | Purpose |
| --- | --- | --- |
| `SUBSCRIPTION_UPDATE` | `true` | enable subscription refresh |
| `SUBSCRIPTION_UPDATE_INTERVAL` | `60` | seconds between cycles |
| `SUBSCRIPTION_POOL_SAMPLES` | `4` | independent snapshots per cycle |
| `SUBSCRIPTION_JSON_FORMAT` | `true` | request full JSON configs |
| `PROXY_INITIAL_CHECK_ONLY` | `true` | one full sweep, then targeted scheduling |
| `PROXY_CHECK_CONCURRENCY` | `30` | first-pass concurrency |
| `PROXY_RETRY_CONCURRENCY` | `10` | retry/targeted concurrency |
| `PROXY_TIMEOUT` | `4` | seconds per fast attempt |
| `PROXY_RETRY_TIMEOUT` | `10` | seconds per retry attempt |
| `PROXY_CHECK_METHOD` | `urltest` | Apple URL Test through Xray |
| `PROXY_URL_TEST_ATTEMPTS` | `2` | requests per health check |
| `NETWORK_STATUS_MAX_AGE` | `15` | sidecar is stale after 15 seconds |
| `RESULTS_FILE` | `/app/data/results.json` | result snapshot |
| `NODE_HISTORY_FILE` | `/app/data/node-history.json` | repair history |
| `NODE_DIAGNOSIS_FILE` | `/app/data/node-diagnostics.json` | Diagnose history |
| `ALERTS_SETTINGS_FILE` | `/app/data/telegram-settings.json` | Telegram settings, token, and delivery state base path |
| `TELEGRAM_PROXY_URL` | empty | optional dashboard-hidden custom HTTP(S)/SOCKS5 Telegram route |
| `WEB_SHOW_DETAILS` | `true` | expose private endpoint details in the local UI |

`PROXY_CHECK_INTERVAL=600` belongs to the legacy repeating-full-cycle mode. With
`PROXY_INITIAL_CHECK_ONLY=true`, it does not trigger another bulk sweep.

## API and service URLs

| Method / URL | Purpose |
| --- | --- |
| `GET /` | dashboard |
| `GET /health` | HTTP process liveness |
| `GET /metrics` | current Prometheus metrics |
| `GET /api/v1/proxies` | private binding list and results |
| `GET /api/v1/public/proxies` | reduced public snapshot |
| `GET /api/v1/proxies/{stableID}` | one binding |
| `POST /api/v1/proxies/{stableID}/recheck` | synchronous priority recheck |
| `GET /api/v1/nodes` | nodes and attached bindings |
| `POST /api/v1/nodes/{nodeID}/recheck` | recheck every binding on a node |
| `POST /api/v1/nodes/{nodeID}/diagnose` | begin deep diagnosis |
| `GET /api/v1/nodes/{nodeID}/diagnosis` | diagnosis history |
| `GET /api/v1/status` | aggregate counters |
| `GET /api/v1/config` | active non-secret configuration |
| `GET /api/v1/network` | iPhone route state |
| `GET /api/v1/docs` | Swagger UI |
| `GET /api/v1/openapi.yaml` | OpenAPI specification |

## Troubleshooting

### `iPhone USB is not connected`

Unlock the iPhone, trust the Mac, enable Personal Hotspot, reconnect the cable,
and verify the interface with `networksetup -listallhardwareports`.

### Dashboard shows `Waiting` or `Recovering`

Proxy checks are paused until the route is ready. Inspect:

```bash
docker-compose --context colima-iphone logs --tail 100 network-monitor
tail -f .runtime/iphone-supervisor.log
colima ssh --profile iphone -- ip -4 route show default
```

The default route must contain `dev col0`.

### The iPhone returned but the route did not

The supervisor waits about 15 seconds before recreating Colima. If recovery did
not run:

```bash
./iphone-supervisor.sh status
./start.sh
```

A manual `./start.sh` intentionally begins a new full sweep.

### A working node is shown as failed

One successful client connection does not prove that the exact binding and
Apple URL worked at the time of the background check. Use `Re-check now` and
inspect its timestamp. The checker confirms failures after one and two minutes,
and continues checking `Needs replacement` every ten minutes.

An exit-side WARP configuration does not affect the current Apple-only health
test because exit IPs are not compared.

### Many nodes became offline at once

Check `/api/v1/network` first. If the iPhone stayed `Connected`, inspect logs.
Many `EOF` or timeout errors may indicate mobile-link pressure. Lower concurrency
or temporarily restore a longer healthy-check window.

### `Degraded`

At least one Xray binding carried a real request, but not every binding achieved
`3/3`. This is not the same as blocked; inspect the Bindings section in Diagnose.

### `Port filtered`

The control network works, but no TCP attempt reached the endpoint. This is a
strong filtering/unreachable signal, but a firewall, stopped process, or
UDP-only protocol can produce the same observation.

### A new subscription IP takes too long to appear

Full JSON should expose it in the next minute cycle. With server-side
randomization, the checker only sees the selected endpoint. Four samples per
minute improve discovery but cannot guarantee a complete pool by a deadline.

## Development and verification

```bash
go test ./...
bash -n start.sh stop.sh iphone-supervisor.sh
sh -n network-watch.sh
docker-compose --context colima-iphone config --quiet
```

Primary packages:

- [`checker/`](checker/) — checks, scheduler, history, diagnosis, endpoint pool;
- [`subscription/`](subscription/) — subscription fetching and parsing;
- [`xray/`](xray/) — Xray configuration and lifecycle;
- [`web/`](web/) — dashboard, API, and OpenAPI;
- [`metrics/`](metrics/) — Prometheus collector and push support;
- [`models/`](models/) — Host/Node/Binding identity.

## Security

- never commit `.env`;
- the private API contains endpoints and generated configurations;
- keep the port bound to loopback unless authentication is added;
- use Basic Auth or a trusted reverse proxy for public mode;
- do not paste subscription URLs, HWIDs, or full diagnostic payloads into public
  issues.

## Origin and license

This repository is based on
[`kutovoys/xray-checker`](https://github.com/kutovoys/xray-checker). The local
branch adds the isolated iPhone/Colima route, Apple URL Test, resilient recovery,
endpoint-pool accumulation, targeted repair monitoring, and deep diagnosis.
See [`LICENSE`](LICENSE) for licensing terms.
