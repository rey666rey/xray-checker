---
title: CLI
description: Использование CLI в Xray Checker
---

### Базовое использование командной строки

CLI интерфейс предоставляет полный контроль над функциональностью Xray Checker через аргументы командной строки.

### Установка

Скачайте последнюю версию бинарного файла из релизов:

```bash
# Для Linux amd64
curl -Lo xray-checker https://github.com/kutovoys/xray-checker/releases/latest/download/xray-checker_linux_amd64
chmod +x xray-checker

# Для Linux arm64
curl -Lo xray-checker https://github.com/kutovoys/xray-checker/releases/latest/download/xray-checker_linux_arm64
chmod +x xray-checker
```

### Базовое использование

Минимально необходимая конфигурация:

```bash
./xray-checker --subscription-url="https://your-subscription-url/sub"
```

### Пример полной конфигурации

```bash
./xray-checker \
  --subscription-url="https://your-subscription-url/sub" \
  --subscription-update=true \
  --subscription-update-interval=300 \
  --proxy-check-interval=300 \
  --proxy-timeout=5 \
  --proxy-check-method=ip \
  --proxy-ip-check-url="https://api.ipify.org?format=text" \
  --proxy-status-check-url="http://cp.cloudflare.com/generate_204" \
  --simulate-latency=true \
  --xray-start-port=10000 \
  --xray-log-level=none \
  --metrics-port=2112 \
  --metrics-protected=true \
  --metrics-username=custom_user \
  --metrics-password=custom_pass \
  --metrics-instance=node-1 \
  --metrics-push-url="https://push.example.com" \
  --metrics-base-url="/xray/monitor" \
  --run-once=false
```

### Основные операции CLI

Проверка версии:

```bash
./xray-checker --version
```

Запуск одного цикла проверки:

```bash
./xray-checker --subscription-url="https://your-sub-url" --run-once
```

Включение аутентификации метрик:

```bash
./xray-checker \
  --subscription-url="https://your-sub-url" \
  --metrics-protected=true \
  --metrics-username=user \
  --metrics-password=pass
```

Изменение портов:

```bash
./xray-checker \
  --subscription-url="https://your-sub-url" \
  --metrics-port=3000 \
  --xray-start-port=20000
```
