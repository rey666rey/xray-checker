---
title: Метрики
description: Параметры и примеры метрик
---

Xray Checker предоставляет две метрики Prometheus для мониторинга состояния и производительности прокси. Подробнее о настройке интеграции читайте в разделе [Prometheus](/ru/integrations/prometheus).

Для визуализации метрик рекомендуется использовать [Grafana](/ru/integrations/grafana).

### xray_proxy_status

Метрика состояния, показывающая доступность прокси:

- Тип: Gauge
- Значения: 1 (работает) или 0 (не работает)
- Метки:
  - `protocol`: Протокол прокси (vless/vmess/trojan/shadowsocks/hysteria/socks/http/wireguard)
  - `address`: Адрес и порт сервера
  - `name`: Имя конфигурации прокси
  - `stable_id`: Стабильный идентификатор для каждого прокси; сохраняет уникальность каждой серии даже при совпадении имён и остаётся неизменным при перезапусках/переупорядочивании
  - `sub_name`: Имя подписки (из фрагмента URL или заголовка profile-title)
  - `group_name`: Имя балансировщика/группы для сгруппированных прокси (пусто для несгруппированных)
  - `instance`: Имя экземпляра (если настроено)

:::tip
Загляните в [расширенную конфигурацию](/ru/configuration/advanced-conf#маркировка-экземпляров) для настройки меток экземпляров.
:::

Пример:

```text
# HELP xray_proxy_status Статус прокси-соединения (1: успешно, 0: неудача)
# TYPE xray_proxy_status gauge
xray_proxy_status{protocol="vless",address="example.com:443",name="proxy1",stable_id="a1b2c3d4e5f67890",sub_name="Premium VPN",group_name="",instance="dc1"} 1
```

### xray_proxy_latency_ms

Метрика задержки, показывающая время отклика соединения:

- Тип: Gauge
- Значения: Миллисекунды (0 при неудаче)
- Метки: Те же, что и у xray_proxy_status

Пример:

```text
# HELP xray_proxy_latency_ms Задержка прокси-соединения в миллисекундах
# TYPE xray_proxy_latency_ms gauge
xray_proxy_latency_ms{protocol="vless",address="example.com:443",name="proxy1",stable_id="a1b2c3d4e5f67890",sub_name="Premium VPN",group_name="",instance="dc1"} 156
```

### Кастомные лейблы

Outbound в [JSON-подписке](/ru/configuration/subscription#9-кастомные-лейблы-метрик) может содержать объект `metricsLabels`. Его записи добавляются как дополнительные лейблы на обеих метриках этого прокси, рядом со встроенными лейблами выше:

```text
xray_proxy_status{protocol="trojan",address="1.1.1.1:443",name="proxy1",stable_id="a1b2c3d4e5f67890",sub_name="",group_name="",location="Netherlands, Amsterdam",hoster="FreeVDS"} 1
```

Разные прокси могут нести разные ключи лейблов; прокси без `metricsLabels` сохраняет только встроенный набор. Ключи приводятся к корректным именам Prometheus и не могут переопределять встроенные лейблы. Метрики формируются из текущего набора прокси на каждый scrape, поэтому кастомные лейблы можно добавлять, менять и удалять при обновлении подписки без сброса других серий в 0.
