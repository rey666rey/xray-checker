#!/bin/sh

set -u

NETWORK_INTERFACE="${NETWORK_INTERFACE:-col0}"
NETWORK_CHECK_URL="${NETWORK_CHECK_URL:-http://captive.apple.com/hotspot-detect.html}"
NETWORK_CHECK_EXPECTED="${NETWORK_CHECK_EXPECTED:-Success}"
NETWORK_CHECK_INTERVAL="${NETWORK_CHECK_INTERVAL:-5}"
NETWORK_STATUS_FILE="${NETWORK_STATUS_FILE:-/app/runtime/network-status.json}"

status_dir="$(dirname "${NETWORK_STATUS_FILE}")"
last_state=""
failures=0

write_status() {
  state="$1"
  public_ip="${2:-}"
  message="$3"
  temporary="${NETWORK_STATUS_FILE}.tmp.$$"

  mkdir -p "${status_dir}"
  printf '{"state":"%s","interface":"%s","publicIp":"%s","message":"%s","updatedAt":%s}\n' \
    "${state}" "${NETWORK_INTERFACE}" "${public_ip}" "${message}" "$(date +%s)" \
    >"${temporary}"
  mv -f "${temporary}" "${NETWORK_STATUS_FILE}"

  if [ "${state}" != "${last_state}" ]; then
    printf '%s network=%s interface=%s public_ip=%s message=%s\n' \
      "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${state}" "${NETWORK_INTERFACE}" \
      "${public_ip:-none}" "${message}"
    last_state="${state}"
  fi
}

stop_monitor() {
  write_status "stopped" "" "iPhone route monitor is stopped"
  exit 0
}

trap stop_monitor INT TERM

write_status "recovering" "" "Checking the iPhone mobile route"

while true; do
  if [ ! -e "/sys/class/net/${NETWORK_INTERFACE}" ]; then
    failures=$((failures + 1))
    write_status "waiting" "" "Waiting for the iPhone network interface"
    sleep "${NETWORK_CHECK_INTERVAL}"
    continue
  fi

  response="$(curl --interface "${NETWORK_INTERFACE}" --fail --silent \
    --show-error --max-time 8 "${NETWORK_CHECK_URL}" 2>/dev/null || true)"

  if [ -n "${response}" ] && printf '%s' "${response}" | grep -Fq "${NETWORK_CHECK_EXPECTED}"; then
    failures=0
    write_status "connected" "" "iPhone mobile route is active"
  else
    failures=$((failures + 1))
    if [ "${failures}" -ge 3 ]; then
      write_status "waiting" "" "Waiting for iPhone USB or mobile internet"
    else
      write_status "recovering" "" "Waiting for the iPhone mobile route to recover"
    fi
  fi

  sleep "${NETWORK_CHECK_INTERVAL}"
done
