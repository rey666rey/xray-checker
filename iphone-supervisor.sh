#!/usr/bin/env bash

set -uo pipefail

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly COLIMA_PROFILE="${COLIMA_PROFILE:-iphone}"
readonly IPHONE_INTERFACE="${IPHONE_INTERFACE:-en7}"
readonly LABEL="com.xray-checker.iphone-supervisor"
readonly RUNTIME_DIR="${SCRIPT_DIR}/.runtime"
readonly PLIST_PATH="${HOME}/Library/LaunchAgents/${LABEL}.plist"
readonly CHECK_INTERVAL="${IPHONE_RECOVERY_CHECK_INTERVAL:-3}"
readonly FAILURE_THRESHOLD="${IPHONE_RECOVERY_FAILURE_THRESHOLD:-3}"
readonly RECOVERY_COOLDOWN="${IPHONE_RECOVERY_COOLDOWN:-60}"
readonly PROBE_URL="${IPHONE_RECOVERY_PROBE_URL:-https://1.1.1.1/cdn-cgi/trace}"
readonly PROBE_EXPECTED="${IPHONE_RECOVERY_PROBE_EXPECTED:-ip=}"
readonly PROBE_TIMEOUT="${IPHONE_RECOVERY_PROBE_TIMEOUT:-3}"

log() {
  printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

iphone_is_attached() {
  /usr/sbin/ipconfig getifaddr "${IPHONE_INTERFACE}" >/dev/null 2>&1
}

mobile_route_is_ready() {
  /usr/bin/curl -fsS --connect-timeout 1 --max-time 2 \
    http://127.0.0.1:2112/api/v1/network 2>/dev/null |
    /usr/bin/grep -q '"state":"connected"'
}

iphone_data_plane_is_ready() {
  /usr/bin/curl --interface "${IPHONE_INTERFACE}" --fail --silent \
    --connect-timeout 1 --max-time "${PROBE_TIMEOUT}" "${PROBE_URL}" 2>/dev/null |
    /usr/bin/grep -Fq "${PROBE_EXPECTED}"
}

colima_data_plane_is_ready() {
  /opt/homebrew/bin/colima status --profile "${COLIMA_PROFILE}" >/dev/null 2>&1 &&
    /opt/homebrew/bin/colima ssh --profile "${COLIMA_PROFILE}" -- \
      curl --interface col0 --fail --silent --connect-timeout 1 \
        --max-time "${PROBE_TIMEOUT}" "${PROBE_URL}" 2>/dev/null |
      /usr/bin/grep -Fq "${PROBE_EXPECTED}"
}

run_supervisor() {
  local failures=0
  local last_recovery=0
  local last_state=""
  local now=0

  log "iPhone recovery supervisor started (interface=${IPHONE_INTERFACE}, profile=${COLIMA_PROFILE})"
  while true; do
    if ! iphone_is_attached; then
      failures=0
      if [[ "${last_state}" != "detached" ]]; then
        log "iPhone interface ${IPHONE_INTERFACE} has no IPv4 address; waiting"
        last_state="detached"
      fi
      sleep "${CHECK_INTERVAL}"
      continue
    fi

    if mobile_route_is_ready; then
      failures=0
      if [[ "${last_state}" != "connected" ]]; then
        log "iPhone route and checker are connected"
        last_state="connected"
      fi
      sleep "${CHECK_INTERVAL}"
      continue
    fi

    # Do not rebuild Colima when the phone itself has no usable mobile route.
    # Recreating the bridge cannot fix that and would only prolong the outage.
    if ! iphone_data_plane_is_ready; then
      failures=0
      if [[ "${last_state}" != "iphone_unavailable" ]]; then
        log "iPhone is attached but its mobile data path is unavailable; waiting"
        last_state="iphone_unavailable"
      fi
      sleep "${CHECK_INTERVAL}"
      continue
    fi

    # A planned checker/container restart makes the local API unavailable even
    # though the VM data plane is healthy. Docker's restart policy handles it.
    if colima_data_plane_is_ready; then
      failures=0
      if [[ "${last_state}" != "checker_unavailable" ]]; then
        log "col0 data plane is healthy; waiting for the checker API"
        last_state="checker_unavailable"
      fi
      sleep "${CHECK_INTERVAL}"
      continue
    fi

    failures=$((failures + 1))
    if [[ "${last_state}" != "bridge_unavailable" ]]; then
      log "iPhone data works on macOS but not through col0; confirming bridge failure"
      last_state="bridge_unavailable"
    fi
    if ((failures < FAILURE_THRESHOLD)); then
      sleep "${CHECK_INTERVAL}"
      continue
    fi

    now="$(date +%s)"
    if ((now - last_recovery < RECOVERY_COOLDOWN)); then
      sleep "${CHECK_INTERVAL}"
      continue
    fi

    last_recovery="${now}"
    failures=0
    log "col0 data plane failed ${FAILURE_THRESHOLD} consecutive checks; recreating the Colima bridge"
    if XRAY_RECOVERY_MODE=true "${SCRIPT_DIR}/start.sh"; then
      log "Colima bridge recovery completed"
      last_state="recovered"
    else
      log "Colima bridge recovery failed; another attempt will be made after cooldown"
      last_state="recovery_failed"
    fi
    sleep "${CHECK_INTERVAL}"
  done
}

install_agent() {
  mkdir -p "${RUNTIME_DIR}" "$(dirname "${PLIST_PATH}")"

  # The repository path is fixed for this installation. XML-special characters
  # are escaped so launchd also works when the project directory contains them.
  local escaped_script_dir
  local escaped_script
  escaped_script_dir="$(printf '%s' "${SCRIPT_DIR}" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g')"
  escaped_script="$(printf '%s' "${SCRIPT_DIR}/iphone-supervisor.sh" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g')"

  tee "${PLIST_PATH}" >/dev/null <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>${escaped_script}</string>
    <string>run</string>
  </array>
  <key>WorkingDirectory</key>
  <string>${escaped_script_dir}</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    <key>COLIMA_PROFILE</key>
    <string>${COLIMA_PROFILE}</string>
    <key>IPHONE_INTERFACE</key>
    <string>${IPHONE_INTERFACE}</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>ThrottleInterval</key>
  <integer>10</integer>
  <key>StandardOutPath</key>
  <string>${escaped_script_dir}/.runtime/iphone-supervisor.log</string>
  <key>StandardErrorPath</key>
  <string>${escaped_script_dir}/.runtime/iphone-supervisor.log</string>
</dict>
</plist>
EOF

  /bin/launchctl bootout "gui/${UID}/${LABEL}" >/dev/null 2>&1 || true
  /bin/launchctl bootstrap "gui/${UID}" "${PLIST_PATH}"
  log "Automatic iPhone recovery enabled"
}

uninstall_agent() {
  /bin/launchctl bootout "gui/${UID}/${LABEL}" >/dev/null 2>&1 || true
  if [[ -f "${PLIST_PATH}" ]]; then
    rm -f -- "${PLIST_PATH}"
  fi
  log "Automatic iPhone recovery disabled"
}

case "${1:-}" in
  run)
    run_supervisor
    ;;
  install)
    install_agent
    ;;
  uninstall)
    uninstall_agent
    ;;
  status)
    /bin/launchctl print "gui/${UID}/${LABEL}"
    ;;
  *)
    printf 'Usage: %s {install|uninstall|status|run}\n' "$0" >&2
    exit 2
    ;;
esac
