#!/usr/bin/env bash

set -uo pipefail

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly COLIMA_PROFILE="${COLIMA_PROFILE:-iphone}"
readonly IPHONE_INTERFACE="${IPHONE_INTERFACE:-en7}"
readonly LABEL="com.xray-checker.iphone-supervisor"
readonly RUNTIME_DIR="${SCRIPT_DIR}/.runtime"
readonly RECOVERY_CONTROL_DIR="${IPHONE_RECOVERY_CONTROL_DIR:-${RUNTIME_DIR}/control}"
readonly RECOVERY_REQUEST_FILE="${RECOVERY_CONTROL_DIR}/iphone-recovery.request"
readonly RECOVERY_PROCESSING_FILE="${RECOVERY_CONTROL_DIR}/iphone-recovery.processing"
readonly RECOVERY_STATUS_FILE="${RECOVERY_CONTROL_DIR}/iphone-recovery-status.json"
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

prepare_recovery_control() {
  mkdir -p "${RECOVERY_CONTROL_DIR}"
  # The checker runs as an unprivileged Linux user through a Colima bind mount.
  # Directory write access lets it publish requests atomically without running
  # the main container as root.
  chmod 0777 "${RECOVERY_CONTROL_DIR}" 2>/dev/null || true
}

write_recovery_status() {
  local request_id="$1"
  local trigger="$2"
  local state="$3"
  local active="$4"
  local requested_at="$5"
  local started_at="$6"
  local completed_at="$7"
  local message="$8"
  local updated_at
  local temporary

  updated_at="$(date +%s)"
  temporary="${RECOVERY_STATUS_FILE}.tmp.$$"
  printf '{"enabled":true,"active":%s,"requestId":"%s","state":"%s","trigger":"%s","message":"%s","requestedAt":%s,"startedAt":%s,"completedAt":%s,"updatedAt":%s}\n' \
    "${active}" "${request_id}" "${state}" "${trigger}" "${message}" \
    "${requested_at}" "${started_at}" "${completed_at}" "${updated_at}" >"${temporary}"
  chmod 0666 "${temporary}" 2>/dev/null || true
  mv -f "${temporary}" "${RECOVERY_STATUS_FILE}"
}

read_request_field() {
  local path="$1"
  local field="$2"
  sed -n "s/.*\"${field}\":\"\([^\"]*\)\".*/\1/p" "${path}" 2>/dev/null | head -n 1
}

read_request_number() {
  local path="$1"
  local field="$2"
  sed -n "s/.*\"${field}\":\([0-9][0-9]*\).*/\1/p" "${path}" 2>/dev/null | head -n 1
}

perform_bridge_recovery() {
  local request_id="$1"
  local trigger="$2"
  local requested_at="$3"
  local started_at
  local completed_at
  local pending_id
  local pending_requested_at
  local outcome=1

  started_at="$(date +%s)"
  write_recovery_status "${request_id}" "${trigger}" "running" true \
    "${requested_at}" "${started_at}" 0 "Reconnecting the Colima iPhone bridge"
  log "${trigger} iPhone bridge reconnect started (request=${request_id})"

  if XRAY_RECOVERY_MODE=true "${SCRIPT_DIR}/start.sh"; then
    outcome=0
  fi

  # A click received while an automatic recovery was already stopping Colima
  # is satisfied by that same recovery instead of causing a second restart.
  if [[ -f "${RECOVERY_REQUEST_FILE}" ]]; then
    pending_id="$(read_request_field "${RECOVERY_REQUEST_FILE}" requestId)"
    pending_requested_at="$(read_request_number "${RECOVERY_REQUEST_FILE}" requestedAt)"
    rm -f -- "${RECOVERY_REQUEST_FILE}"
    if [[ -n "${pending_id}" ]]; then
      request_id="${pending_id}"
      trigger="manual"
      requested_at="${pending_requested_at:-${requested_at}}"
    fi
  fi
  rm -f -- "${RECOVERY_PROCESSING_FILE}"

  completed_at="$(date +%s)"
  if ((outcome == 0)); then
    write_recovery_status "${request_id}" "${trigger}" "succeeded" false \
      "${requested_at}" "${started_at}" "${completed_at}" "iPhone bridge reconnected"
    log "Colima bridge recovery completed"
    return 0
  fi

  write_recovery_status "${request_id}" "${trigger}" "failed" false \
    "${requested_at}" "${started_at}" "${completed_at}" "iPhone bridge reconnect failed"
  log "Colima bridge recovery failed"
  return 1
}

process_manual_recovery() {
  local request_id
  local requested_at

  [[ -f "${RECOVERY_REQUEST_FILE}" ]] || return 1
  if ! mv "${RECOVERY_REQUEST_FILE}" "${RECOVERY_PROCESSING_FILE}" 2>/dev/null; then
    return 1
  fi
  request_id="$(read_request_field "${RECOVERY_PROCESSING_FILE}" requestId)"
  requested_at="$(read_request_number "${RECOVERY_PROCESSING_FILE}" requestedAt)"
  request_id="${request_id:-manual-$(date +%s)}"
  requested_at="${requested_at:-$(date +%s)}"
  perform_bridge_recovery "${request_id}" manual "${requested_at}" || true
  return 0
}

request_manual_recovery() {
  local request_id
  local requested_at
  local temporary

  prepare_recovery_control
  if [[ -f "${RECOVERY_REQUEST_FILE}" || -f "${RECOVERY_PROCESSING_FILE}" ]]; then
    log "iPhone bridge reconnect is already queued or running"
    return 1
  fi
  requested_at="$(date +%s)"
  request_id="manual-${requested_at}-$$"
  write_recovery_status "${request_id}" manual queued true \
    "${requested_at}" 0 0 "Waiting for the macOS recovery supervisor"
  temporary="${RECOVERY_REQUEST_FILE}.tmp.$$"
  printf '{"requestId":"%s","trigger":"manual","requestedAt":%s}\n' \
    "${request_id}" "${requested_at}" >"${temporary}"
  chmod 0666 "${temporary}" 2>/dev/null || true
  mv -f "${temporary}" "${RECOVERY_REQUEST_FILE}"
  log "Manual iPhone bridge reconnect queued (request=${request_id})"
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

  prepare_recovery_control
  if [[ -f "${RECOVERY_PROCESSING_FILE}" ]]; then
    rm -f -- "${RECOVERY_PROCESSING_FILE}"
    write_recovery_status "interrupted-$(date +%s)" supervisor failed false 0 0 "$(date +%s)" \
      "Previous reconnect was interrupted"
  fi
  log "iPhone recovery supervisor started (interface=${IPHONE_INTERFACE}, profile=${COLIMA_PROFILE})"
  while true; do
    if process_manual_recovery; then
      last_recovery="$(date +%s)"
      last_state="manual_recovery_completed"
      sleep "${CHECK_INTERVAL}"
      continue
    fi

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
    if perform_bridge_recovery "automatic-${now}" automatic "${now}"; then
      last_state="recovered"
    else
      log "Another automatic attempt will be made after cooldown"
      last_state="recovery_failed"
    fi
    sleep "${CHECK_INTERVAL}"
  done
}

install_agent() {
  prepare_recovery_control
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
  recover|reconnect)
    request_manual_recovery
    ;;
  *)
    printf 'Usage: %s {install|uninstall|status|run|recover}\n' "$0" >&2
    exit 2
    ;;
esac
