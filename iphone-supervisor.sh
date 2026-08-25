#!/usr/bin/env bash

set -uo pipefail

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly COLIMA_PROFILE="${COLIMA_PROFILE:-iphone}"
readonly IPHONE_INTERFACE="${IPHONE_INTERFACE:-en7}"
readonly LABEL="com.xray-checker.iphone-supervisor"
readonly RUNTIME_DIR="${SCRIPT_DIR}/.runtime"
readonly PLIST_PATH="${HOME}/Library/LaunchAgents/${LABEL}.plist"
readonly CHECK_INTERVAL="${IPHONE_RECOVERY_CHECK_INTERVAL:-3}"
readonly FAILURE_THRESHOLD="${IPHONE_RECOVERY_FAILURE_THRESHOLD:-5}"
readonly RECOVERY_COOLDOWN="${IPHONE_RECOVERY_COOLDOWN:-60}"

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

run_supervisor() {
  local failures=0
  local last_recovery=0
  local now=0

  log "iPhone recovery supervisor started (interface=${IPHONE_INTERFACE}, profile=${COLIMA_PROFILE})"
  while true; do
    if ! iphone_is_attached; then
      failures=0
      sleep "${CHECK_INTERVAL}"
      continue
    fi

    if mobile_route_is_ready; then
      failures=0
      sleep "${CHECK_INTERVAL}"
      continue
    fi

    failures=$((failures + 1))
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
    log "iPhone is attached but col0 is unavailable; recreating the Colima bridge"
    if XRAY_RECOVERY_MODE=true "${SCRIPT_DIR}/start.sh"; then
      log "Colima bridge recovery completed"
    else
      log "Colima bridge recovery failed; another attempt will be made after cooldown"
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
