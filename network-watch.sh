#!/bin/sh

set -u

NETWORK_INTERFACE="${NETWORK_INTERFACE:-col0}"
NETWORK_SOURCE_ID="${NETWORK_SOURCE_ID:-iphone}"
NETWORK_SOURCE_LABEL="${NETWORK_SOURCE_LABEL:-iPhone USB}"
NETWORK_SOURCE_EPOCH="${NETWORK_SOURCE_EPOCH:-legacy}"
# These fields enter JSON; only locally defined source identifiers/labels pass.
case "${NETWORK_SOURCE_ID}" in iphone) NETWORK_SOURCE_LABEL="iPhone USB";; router) NETWORK_SOURCE_LABEL="LTE router";; *) exit 2;; esac
case "${NETWORK_SOURCE_EPOCH}" in *[!a-f0-9]*|'') [ "${NETWORK_SOURCE_EPOCH}" = legacy ] || exit 2;; esac
NETWORK_CHECK_URL="${NETWORK_CHECK_URL:-https://1.1.1.1/cdn-cgi/trace}"
NETWORK_CHECK_EXPECTED="${NETWORK_CHECK_EXPECTED:-ip=}"
NETWORK_CHECK_INTERVAL="${NETWORK_CHECK_INTERVAL:-5}"
NETWORK_CHECK_TIMEOUT="${NETWORK_CHECK_TIMEOUT:-5}"
NETWORK_IP_CHECK_URL="${NETWORK_IP_CHECK_URL:-}"
NETWORK_IP_CHECK_INTERVAL="${NETWORK_IP_CHECK_INTERVAL:-60}"
NETWORK_STATUS_FILE="${NETWORK_STATUS_FILE:-/app/runtime/network-status.json}"

status_dir="$(dirname "${NETWORK_STATUS_FILE}")"
last_state=""
failures=0
public_ip=""
last_ip_check=0

write_status() {
  state="$1"
  reported_public_ip="${2:-}"
  message="$3"
  temporary="${NETWORK_STATUS_FILE}.tmp.$$"

  mkdir -p "${status_dir}"
  printf '{"state":"%s","sourceId":"%s","label":"%s","epoch":"%s","interface":"%s","publicIp":"%s","message":"%s","updatedAt":%s}\n' \
    "${state}" "${NETWORK_SOURCE_ID}" "${NETWORK_SOURCE_LABEL}" "${NETWORK_SOURCE_EPOCH}" "${NETWORK_INTERFACE}" "${reported_public_ip}" "${message}" "$(date +%s)" \
    >"${temporary}"
  mv -f "${temporary}" "${NETWORK_STATUS_FILE}"

  if [ "${state}" != "${last_state}" ]; then
    printf '%s network=%s interface=%s public_ip=%s message=%s\n' \
      "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${state}" "${NETWORK_INTERFACE}" \
      "${reported_public_ip:-none}" "${message}"
    last_state="${state}"
  fi
}

stop_monitor() {
  write_status "stopped" "" "Measurement route monitor is stopped"
  exit 0
}

trap stop_monitor INT TERM

write_status "recovering" "" "Checking the selected measurement route"

while true; do
  if [ ! -e "/sys/class/net/${NETWORK_INTERFACE}" ]; then
    failures=$((failures + 1))
    write_status "waiting" "" "Waiting for the selected network interface"
    sleep "${NETWORK_CHECK_INTERVAL}"
    continue
  fi

  response="$(curl -4 --noproxy '*' --interface "${NETWORK_INTERFACE}" --fail --silent \
    --show-error --max-time "${NETWORK_CHECK_TIMEOUT}" "${NETWORK_CHECK_URL}" 2>/dev/null || true)"

  if [ -n "${response}" ] && printf '%s' "${response}" | grep -Fq "${NETWORK_CHECK_EXPECTED}"; then
    failures=0
    observed_ip="$(printf '%s\n' "${response}" | awk -F= '$1 == "ip" {print $2; exit}')"
    if printf '%s' "${observed_ip}" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'; then public_ip="${observed_ip}"; fi
    now="$(date +%s)"
    if [ -n "${NETWORK_IP_CHECK_URL}" ] && \
      { [ -z "${public_ip}" ] || [ $((now - last_ip_check)) -ge "${NETWORK_IP_CHECK_INTERVAL}" ]; }; then
      candidate="$(curl -4 --noproxy '*' --interface "${NETWORK_INTERFACE}" --fail --silent \
        --show-error --max-time "${NETWORK_CHECK_TIMEOUT}" "${NETWORK_IP_CHECK_URL}" 2>/dev/null || true)"
      if [ ${#candidate} -le 64 ] && printf '%s' "${candidate}" | grep -Eq '^[0-9A-Fa-f:.]+$'; then
        public_ip="${candidate}"
      fi
      last_ip_check="${now}"
    fi
    write_status "connected" "${public_ip}" "Selected measurement route is active"
  else
    failures=$((failures + 1))
    if [ "${failures}" -ge 3 ]; then
      write_status "waiting" "" "Waiting for the selected connection or mobile internet"
    else
      write_status "recovering" "" "Waiting for the selected measurement route to recover"
    fi
  fi

  sleep "${NETWORK_CHECK_INTERVAL}"
done
