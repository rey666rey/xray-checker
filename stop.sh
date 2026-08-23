#!/usr/bin/env bash

set -uo pipefail

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly COLIMA_PROFILE="${COLIMA_PROFILE:-iphone}"
readonly DOCKER_CONTEXT="colima-${COLIMA_PROFILE}"

warn() {
  printf 'Предупреждение: %s\n' "$*" >&2
}

if ! command -v colima >/dev/null 2>&1; then
  printf 'Ошибка: Colima не установлена.\n' >&2
  exit 1
fi

result=0

if colima status --profile "${COLIMA_PROFILE}" >/dev/null 2>&1; then
  compose=()
  if ! command -v docker >/dev/null 2>&1; then
    warn "Docker CLI не установлен; перехожу к остановке Colima."
    result=1
  elif docker compose version >/dev/null 2>&1; then
    compose=(docker --context "${DOCKER_CONTEXT}" compose)
  elif command -v docker-compose >/dev/null 2>&1; then
    compose=(docker-compose --context "${DOCKER_CONTEXT}")
  fi

  if ((${#compose[@]} == 0)); then
    warn "Docker Compose не установлен; перехожу к остановке Colima."
    result=1
  else
    printf 'Останавливаю и удаляю контейнеры Xray Checker...\n'
    if ! (
      cd "${SCRIPT_DIR}" || exit 1
      SUBSCRIPTION_URL="${SUBSCRIPTION_URL:-unused}" \
        HWID="${HWID:-unused}" \
        "${compose[@]}" down --remove-orphans
    ); then
      warn "Docker Compose не смог полностью удалить контейнеры."
      result=1
    fi
  fi

  printf 'Останавливаю профиль Colima %s...\n' "${COLIMA_PROFILE}"
  if ! colima stop --profile "${COLIMA_PROFILE}"; then
    warn "не удалось остановить профиль Colima ${COLIMA_PROFILE}."
    result=1
  fi
else
  printf 'Профиль Colima %s уже остановлен.\n' "${COLIMA_PROFILE}"
fi

if colima daemon status "${COLIMA_PROFILE}" >/dev/null 2>&1; then
  printf 'Останавливаю bridge-daemon Colima %s...\n' "${COLIMA_PROFILE}"
  if ! colima daemon stop "${COLIMA_PROFILE}"; then
    warn "не удалось остановить bridge-daemon ${COLIMA_PROFILE}."
    result=1
  fi
fi

if ((result == 0)); then
  printf 'Xray Checker и Colima полностью остановлены.\n'
fi

exit "${result}"
