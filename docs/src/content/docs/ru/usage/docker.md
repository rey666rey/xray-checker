---
title: Docker
description: Запуск Xray Checker с помощью Docker и Docker Compose
---

### Базовое использование Docker

Загрузка последнего образа:

```bash
docker pull kutovoys/xray-checker
```

Запуск с минимальной конфигурацией:

```bash
docker run -d \
  -e SUBSCRIPTION_URL=https://your-subscription-url/sub \
  -p 2112:2112 \
  kutovoys/xray-checker
```

### Полная конфигурация Docker

```bash
docker run -d \
  -e SUBSCRIPTION_URL=https://your-subscription-url/sub \
  -e SUBSCRIPTION_UPDATE=true \
  -e SUBSCRIPTION_UPDATE_INTERVAL=300 \
  -e PROXY_CHECK_INTERVAL=300 \
  -e PROXY_CHECK_METHOD=ip \
  -e PROXY_TIMEOUT=30 \
  -e PROXY_IP_CHECK_URL=https://api.ipify.org?format=text \
  -e PROXY_STATUS_CHECK_URL=http://cp.cloudflare.com/generate_204 \
  -e SIMULATE_LATENCY=true \
  -e XRAY_START_PORT=10000 \
  -e XRAY_LOG_LEVEL=none \
  -e METRICS_PORT=2112 \
  -e METRICS_PROTECTED=true \
  -e METRICS_USERNAME=custom_user \
  -e METRICS_PASSWORD=custom_pass \
  -e METRICS_INSTANCE=node-1 \
  -e METRICS_PUSH_URL=https://push.example.com \
  -e METRICS_BASE_URL=/xray/monitor
  -e RUN_ONCE=false \
  -p 2112:2112 \
  kutovoys/xray-checker
```

### Docker Compose

Базовый docker-compose.yml:

```yaml
services:
  xray-checker:
    image: kutovoys/xray-checker
    environment:
      - SUBSCRIPTION_URL=https://your-subscription-url/sub
    ports:
      - "2112:2112"
```

Полный docker-compose.yml:

```yaml
services:
  xray-checker:
    image: kutovoys/xray-checker
    environment:
      - SUBSCRIPTION_URL=https://your-subscription-url/sub
      - SUBSCRIPTION_UPDATE=true
      - SUBSCRIPTION_UPDATE_INTERVAL=300
      - PROXY_CHECK_INTERVAL=300
      - PROXY_CHECK_METHOD=ip
      - PROXY_TIMEOUT=30
      - PROXY_IP_CHECK_URL=https://api.ipify.org?format=text
      - PROXY_STATUS_CHECK_URL=http://cp.cloudflare.com/generate_204
      - SIMULATE_LATENCY=true
      - XRAY_START_PORT=10000
      - XRAY_LOG_LEVEL=none
      - METRICS_PORT=2112
      - METRICS_PROTECTED=true
      - METRICS_USERNAME=custom_user
      - METRICS_PASSWORD=custom_pass
      - METRICS_INSTANCE=node-1
      - METRICS_PUSH_URL=https://push.example.com
      - METRICS_BASE_URL=/xray/monitor
      - RUN_ONCE=false
    ports:
      - "2112:2112"
    restart: unless-stopped
```

### Настройка сети Docker

Пользовательская настройка сети:

```yaml
services:
  xray-checker:
    networks:
      - monitoring
    ports:
      - "2112:2112"

networks:
  monitoring:
    name: monitoring-network
```

### Проверка работоспособности Docker

Добавление проверки работоспособности в docker-compose.yml:

```yaml
services:
  xray-checker:
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:2112/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s
```
