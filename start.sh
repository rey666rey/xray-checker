#!/usr/bin/env bash

set -Eeuo pipefail

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly COLIMA_PROFILE="${COLIMA_PROFILE:-iphone}"
readonly IPHONE_INTERFACE="${IPHONE_INTERFACE:-en7}"
readonly DOCKER_CONTEXT="colima-${COLIMA_PROFILE}"
readonly RECOVERY_MODE="${XRAY_RECOVERY_MODE:-false}"
readonly BRIDGE_READY_TIMEOUT="${IPHONE_BRIDGE_READY_TIMEOUT:-90}"
readonly BRIDGE_READY_SUCCESSES="${IPHONE_BRIDGE_READY_SUCCESSES:-3}"
readonly BRIDGE_PROBE_URL="https://1.1.1.1/cdn-cgi/trace"
readonly CHECKER_START_ATTEMPTS=3
readonly CHECKER_START_TIMEOUT=120

supervisor_needs_restore=false

die() {
  printf 'Ошибка: %s\n' "$*" >&2
  exit 1
}

restore_supervisor() {
  local exit_code=$?

  # Avoid recursively running this handler if installation itself fails.
  trap - EXIT
  if [[ "${supervisor_needs_restore}" == "true" ]]; then
    if ! "${SCRIPT_DIR}/iphone-supervisor.sh" install; then
      printf 'Ошибка: не удалось вернуть supervisor автовосстановления.\n' >&2
      exit 1
    fi
  fi

  exit "${exit_code}"
}

wait_for_bridge_data_plane() {
  local deadline=$((SECONDS + BRIDGE_READY_TIMEOUT))
  local successes=0

  printf 'Жду стабильного доступа в интернет через col0...\n'
  while ((SECONDS < deadline)); do
    if colima ssh --profile "${COLIMA_PROFILE}" -- \
      curl --interface col0 --fail --silent --connect-timeout 2 --max-time 4 \
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

command -v colima >/dev/null 2>&1 || die "Colima не установлена."
command -v docker >/dev/null 2>&1 || die "Docker CLI не установлен."
[[ -f "${SCRIPT_DIR}/.env" ]] || die "Нет файла .env. Создайте его: cp .env.example .env"
ipconfig getifaddr "${IPHONE_INTERFACE}" >/dev/null 2>&1 ||
  die "iPhone USB не подключён: интерфейс ${IPHONE_INTERFACE} не получил IPv4-адрес."

if docker compose version >/dev/null 2>&1; then
  compose=(docker --context "${DOCKER_CONTEXT}" compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose=(docker-compose --context "${DOCKER_CONTEXT}")
else
  die "Docker Compose не установлен."
fi

if [[ "${RECOVERY_MODE}" != "true" ]] && [[ -x "${SCRIPT_DIR}/iphone-supervisor.sh" ]]; then
  supervisor_needs_restore=true
  trap restore_supervisor EXIT
  "${SCRIPT_DIR}/iphone-supervisor.sh" uninstall
fi

if colima status --profile "${COLIMA_PROFILE}" >/dev/null 2>&1; then
  printf 'Останавливаю профиль Colima %s перед переключением сети...\n' "${COLIMA_PROFILE}"
  colima stop --profile "${COLIMA_PROFILE}"
fi

if colima daemon status "${COLIMA_PROFILE}" >/dev/null 2>&1; then
  colima daemon stop "${COLIMA_PROFILE}"
fi

printf 'Запускаю bridge Colima %s через iPhone USB (%s)...\n' \
  "${COLIMA_PROFILE}" "${IPHONE_INTERFACE}"
colima daemon start "${COLIMA_PROFILE}" \
  --vmnet \
  --vmnet-mode bridged \
  --vmnet-interface "${IPHONE_INTERFACE}"
colima start \
  --profile "${COLIMA_PROFILE}" \
  --network-address \
  --network-mode bridged \
  --network-interface "${IPHONE_INTERFACE}" \
  --save-config

vm_routes="$(colima ssh --profile "${COLIMA_PROFILE}" -- ip -4 route show default)"
if ! grep -Eq '^default .* dev col0 ' <<<"${vm_routes}"; then
  colima stop --profile "${COLIMA_PROFILE}" >/dev/null 2>&1 || true
  die "Colima не получила основной bridge-маршрут col0 через iPhone; checker не запущен."
fi
if ! wait_for_bridge_data_plane; then
  die "Маршрут col0 получен, но не стал стабильно пропускать трафик за ${BRIDGE_READY_TIMEOUT} секунд."
fi

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
    printf 'Удаляю результаты прошлого запуска для новой полной проверки...\n'
    "${compose[@]}" run --rm --no-deps --entrypoint /bin/rm \
      xray-checker -f /app/data/results.json /app/data/results.json.tmp
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
