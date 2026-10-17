#!/usr/bin/env bash

set -Eeuo pipefail

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly NETWORK_SOURCE_PYTHON="${NETWORK_SOURCE_PYTHON:-$(command -v python3 || printf /usr/bin/python3)}"
readonly COLIMA_PROFILE="${COLIMA_PROFILE:-iphone}"
readonly DOCKER_CONTEXT="colima-${COLIMA_PROFILE}"
readonly RECOVERY_MODE="${XRAY_RECOVERY_MODE:-false}"
readonly PRESERVE_RESULTS="${XRAY_PRESERVE_RESULTS:-false}"
readonly BRIDGE_READY_TIMEOUT="${IPHONE_BRIDGE_READY_TIMEOUT:-90}"
readonly BRIDGE_READY_SUCCESSES="${IPHONE_BRIDGE_READY_SUCCESSES:-3}"
readonly BRIDGE_PROBE_URL="https://1.1.1.1/cdn-cgi/trace"
readonly CHECKER_START_ATTEMPTS=3
readonly CHECKER_START_TIMEOUT=120
readonly DOCKER_BUILD_CACHE_LIMIT="${DOCKER_BUILD_CACHE_LIMIT:-1GB}"
readonly START_LOCK_DIR="${SCRIPT_DIR}/.runtime/start.lock"

supervisor_needs_restore=false
start_lock_acquired=false

die() {
  printf 'Ошибка: %s\n' "$*" >&2
  exit 1
}

cleanup_on_exit() {
  local exit_code=$?

  # Avoid recursively running this handler if cleanup itself fails.
  trap - EXIT
  if [[ "${supervisor_needs_restore}" == "true" ]]; then
    if ! "${SCRIPT_DIR}/iphone-supervisor.sh" install; then
      printf 'Ошибка: не удалось вернуть supervisor автовосстановления.\n' >&2
      exit_code=1
    fi
  fi
  if [[ "${start_lock_acquired}" == "true" ]]; then
    rm -f -- "${START_LOCK_DIR}/pid"
    rmdir "${START_LOCK_DIR}" 2>/dev/null || true
  fi

  exit "${exit_code}"
}

acquire_start_lock() {
  local owner_pid=""

  if ! mkdir "${START_LOCK_DIR}" 2>/dev/null; then
    owner_pid="$(sed -n '1p' "${START_LOCK_DIR}/pid" 2>/dev/null || true)"
    if [[ "${owner_pid}" =~ ^[0-9]+$ ]] && kill -0 "${owner_pid}" 2>/dev/null; then
      die "Другой запуск или восстановление уже выполняется (PID ${owner_pid})."
    fi
    rm -f -- "${START_LOCK_DIR}/pid"
    rmdir "${START_LOCK_DIR}" 2>/dev/null ||
      die "Не удалось удалить устаревшую блокировку ${START_LOCK_DIR}."
    mkdir "${START_LOCK_DIR}" || die "Не удалось создать блокировку запуска."
  fi
  printf '%s\n' "$$" >"${START_LOCK_DIR}/pid"
  start_lock_acquired=true
  trap cleanup_on_exit EXIT
}

wait_for_bridge_data_plane() {
  local deadline=$((SECONDS + BRIDGE_READY_TIMEOUT))
  local successes=0

  printf 'Жду стабильного доступа в интернет через col0...\n'
  while ((SECONDS < deadline)); do
    if colima ssh --profile "${COLIMA_PROFILE}" -- \
      curl -4 --noproxy '*' --interface col0 --fail --silent --connect-timeout 2 --max-time 4 \
        "${BRIDGE_PROBE_URL}" 2>/dev/null | grep -Fq 'ip='; then
      successes=$((successes + 1))
      if ((successes >= BRIDGE_READY_SUCCESSES)); then
        return 0
      fi
    else
      successes=0
    fi
    sleep 2
  done

  return 1
}

wait_for_checker_services() {
  local attempt

  for ((attempt = 1; attempt <= CHECKER_START_ATTEMPTS; attempt++)); do
    if "${compose[@]}" up -d --wait --wait-timeout "${CHECKER_START_TIMEOUT}" \
      network-monitor xray-checker; then
      return 0
    fi
    if ((attempt < CHECKER_START_ATTEMPTS)); then
      printf 'Сеть ещё нестабильна; повторяю запуск checker (%s/%s)...\n' \
        "$((attempt + 1))" "${CHECKER_START_ATTEMPTS}"
      sleep 10
    fi
  done

  return 1
}

reclaim_docker_space() {
  printf 'Очищаю неиспользуемые Docker-образы и ограничиваю build-кеш...\n'

  # A regular build leaves dangling layers behind. Neither command removes
  # containers, named volumes, or the checker result/history files.
  if ! docker --context "${DOCKER_CONTEXT}" image prune --force >/dev/null; then
    printf 'Предупреждение: не удалось удалить dangling Docker-образы.\n' >&2
  fi
  if ! docker --context "${DOCKER_CONTEXT}" builder prune --force \
    --keep-storage "${DOCKER_BUILD_CACHE_LIMIT}" >/dev/null; then
    printf 'Предупреждение: не удалось ограничить Docker build-кеш.\n' >&2
  fi
}

command -v colima >/dev/null 2>&1 || die "Colima не установлена."
command -v docker >/dev/null 2>&1 || die "Docker CLI не установлен."
[[ -f "${SCRIPT_DIR}/.env" ]] || die "Нет файла .env. Создайте его: cp .env.example .env"
mkdir -p "${SCRIPT_DIR}/.runtime/control"
chmod 0777 "${SCRIPT_DIR}/.runtime/control" 2>/dev/null || true
acquire_start_lock
if [[ -n "${NETWORK_SOURCE_ID:-}" ]]; then
  source_id="${NETWORK_SOURCE_ID}"
  source_interface="${NETWORK_HOST_INTERFACE:?NETWORK_HOST_INTERFACE is required}"
  source_epoch="${NETWORK_SOURCE_EPOCH:?NETWORK_SOURCE_EPOCH is required}"
else
  IFS=$'\t' read -r source_id source_interface source_epoch < <("${NETWORK_SOURCE_PYTHON}" "${SCRIPT_DIR}/network-source-control.py" active)
  if [[ "${source_id}" == "iphone" && -n "${IPHONE_INTERFACE:-}" ]]; then source_interface="${IPHONE_INTERFACE}"; fi
fi
[[ "${source_id}" == "iphone" || "${source_id}" == "router" ]] || { printf 'Invalid network source\n' >&2; exit 1; }
[[ "${source_interface}" =~ ^en[0-9]{1,3}$ && "${source_epoch}" =~ ^[a-f0-9]{32}$ ]] || { printf 'Invalid network interface/session\n' >&2; exit 1; }
ipconfig getifaddr "${source_interface}" >/dev/null 2>&1 || die "Интерфейс ${source_interface} не получил IPv4-адрес."
if [[ -z "${NETWORK_SOURCE_ID:-}" ]]; then
  source_epoch="$("${NETWORK_SOURCE_PYTHON}" "${SCRIPT_DIR}/network-source-control.py" new-epoch)"
fi
readonly IPHONE_INTERFACE="${source_interface}"
export NETWORK_SOURCE_ID="${source_id}" NETWORK_HOST_INTERFACE="${source_interface}" NETWORK_SOURCE_EPOCH="${source_epoch}"
export NETWORK_SOURCE_LABEL="LTE router"
[[ "${source_id}" != "iphone" ]] || export NETWORK_SOURCE_LABEL="iPhone USB"
ipconfig getifaddr "${IPHONE_INTERFACE}" >/dev/null 2>&1 ||
  die "${NETWORK_SOURCE_LABEL}: интерфейс ${IPHONE_INTERFACE} не получил IPv4-адрес."

if docker compose version >/dev/null 2>&1; then
  compose=(docker --context "${DOCKER_CONTEXT}" compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose=(docker-compose --context "${DOCKER_CONTEXT}")
else
  die "Docker Compose не установлен."
fi

if [[ "${RECOVERY_MODE}" != "true" ]] && [[ -x "${SCRIPT_DIR}/iphone-supervisor.sh" ]]; then
  supervisor_needs_restore=true
  "${SCRIPT_DIR}/iphone-supervisor.sh" uninstall
fi

if colima status --profile "${COLIMA_PROFILE}" >/dev/null 2>&1; then
  printf 'Останавливаю профиль Colima %s перед переключением сети...\n' "${COLIMA_PROFILE}"
  if ! colima stop --profile "${COLIMA_PROFILE}"; then
    printf 'Обычная остановка не сработала; принудительно останавливаю зависшую VM...\n'
    colima stop --force --profile "${COLIMA_PROFILE}"
  fi
else
  # A VM can be marked running while its guest agent and SSH endpoint are dead.
  # Clear that stale state before starting a new vmnet bridge.
  colima stop --force --profile "${COLIMA_PROFILE}" >/dev/null 2>&1 || true
fi

# daemon status can also be stale while socket_vmnet is still alive. Stopping an
# absent daemon is harmless and prevents `daemon start` from being ignored.
colima daemon stop "${COLIMA_PROFILE}" >/dev/null 2>&1 || true

printf 'Запускаю bridge Colima %s через %s (%s)...\n' \
  "${COLIMA_PROFILE}" "${NETWORK_SOURCE_LABEL}" "${IPHONE_INTERFACE}"
colima daemon start "${COLIMA_PROFILE}" \
  --vmnet \
  --vmnet-mode bridged \
  --vmnet-interface "${IPHONE_INTERFACE}"
colima start \
  --profile "${COLIMA_PROFILE}" \
  --network-address \
  --network-mode bridged \
  --network-interface "${IPHONE_INTERFACE}" \
  --network-preferred-route \
  --dns "${NETWORK_DNS_SERVER:-1.1.1.1}" \
  --save-config

vm_routes="$(colima ssh --profile "${COLIMA_PROFILE}" -- ip -4 route show default)"
if ! grep -Eq '^default .* dev col0( |$)' <<<"${vm_routes}"; then
  colima stop --profile "${COLIMA_PROFILE}" >/dev/null 2>&1 || true
  die "Colima не получила основной bridge-маршрут col0; checker не запущен."
fi
if ! wait_for_bridge_data_plane; then
  die "Маршрут col0 получен, но не стал стабильно пропускать трафик за ${BRIDGE_READY_TIMEOUT} секунд."
fi

if [[ "${RECOVERY_MODE}" != "true" ]]; then reclaim_docker_space; fi

printf 'Запускаю Xray Checker с монитором сети...\n'
(
  cd "${SCRIPT_DIR}"
  if [[ "${RECOVERY_MODE}" == "true" ]]; then
    "${compose[@]}" up -d network-monitor xray-checker
    wait_for_checker_services
    "${compose[@]}" up -d --no-deps web-forwarder
  else
    printf 'Собираю образ...\n'
    "${compose[@]}" build
    printf 'Прошлые результаты сохранены отдельно; новый сеанс не наследует их доступность.\n'
    "${compose[@]}" up -d --force-recreate network-monitor xray-checker
    wait_for_checker_services
    "${compose[@]}" up -d --force-recreate --no-deps web-forwarder
  fi
)

printf '\nXray Checker запущен: http://127.0.0.1:2112\n'
printf 'Состояние контейнера:\n'
(
  cd "${SCRIPT_DIR}"
  "${compose[@]}" ps
)
