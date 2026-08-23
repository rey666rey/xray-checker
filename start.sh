#!/usr/bin/env bash

set -Eeuo pipefail

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly COLIMA_PROFILE="${COLIMA_PROFILE:-iphone}"
readonly IPHONE_INTERFACE="${IPHONE_INTERFACE:-en7}"
readonly DOCKER_CONTEXT="colima-${COLIMA_PROFILE}"

die() {
  printf 'Ошибка: %s\n' "$*" >&2
  exit 1
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

printf 'Собираю и запускаю Xray Checker...\n'
(
  cd "${SCRIPT_DIR}"
  "${compose[@]}" up -d --build
)

printf '\nXray Checker запущен: http://127.0.0.1:2112\n'
printf 'Состояние контейнера:\n'
(
  cd "${SCRIPT_DIR}"
  "${compose[@]}" ps
)
