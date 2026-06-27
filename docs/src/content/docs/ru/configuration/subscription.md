---
title: Формат подписки
description: Варианты и примеры формата подписки
---

Xray Checker поддерживает пять различных форматов для конфигурации прокси. Для настройки используйте [переменную окружения](/ru/configuration/envs#subscription_url) `SUBSCRIPTION_URL`.

Подробнее о методах проверки прокси читайте в разделе [методы проверки](/ru/configuration/check-methods).

### 1. URL подписки (По умолчанию)

Стандартный URL подписки, возвращающий Base64-кодированный список прокси-ссылок.

Пример:

```bash
SUBSCRIPTION_URL=https://example.com/subscription
```

Требования:

- HTTPS URL
- Возвращает Base64-кодированное содержимое
- Содержимое - это прокси-URL, разделенные переносом строки
- Поддерживает стандартные заголовки User-Agent

Отправляемые заголовки:

```
Accept: */*
User-Agent: Xray-Checker
```

### 2. Строка Base64

Прямая Base64-кодированная строка, содержащая ссылки конфигурации прокси.

Пример:

```bash
SUBSCRIPTION_URL=dmxlc3M6Ly91dWlkQGV4YW1wbGUuY29tOjQ0MyVlbmNyeXB0aW9uPW5vbmUmc2VjdXJpdHk9dGxzI3Byb3h5MQ==
```

Формат содержимого (до кодирования):

```
vless://uuid@example.com:443?encryption=none&security=tls#proxy1
trojan://password@example.com:443?security=tls#proxy2
vmess://base64encodedconfig
ss://base64encodedconfig
hysteria2://password@example.com:443?sni=example.com#proxy5
```

:::note[Поддерживаемые протоколы]
Поддерживаются VLESS, VMess, Trojan, Shadowsocks и **Hysteria2**, включая такие транспорты, как Reality, xhttp, gRPC, WebSocket и mKCP. Также можно проверять обычные **форвард-прокси SOCKS / HTTP / HTTPS** — см. [SOCKS, HTTP и HTTPS прокси](#7-socks-http-и-https-прокси) ниже.
:::

### 3. JSON-файл V2Ray

Один JSON-файл конфигурации в формате V2Ray/Xray.

Пример:

```bash
SUBSCRIPTION_URL=file:///path/to/config.json
```

Формат файла:

```json
{
  "outbounds": [
    {
      "protocol": "vless",
      "settings": {
        "vnext": [
          {
            "address": "example.com",
            "port": 443,
            "users": [
              {
                "id": "uuid",
                "encryption": "none"
              }
            ]
          }
        ]
      },
      "streamSettings": {
        "network": "tcp",
        "security": "tls"
      }
    }
  ]
}
```

### 4. Xray JSON-массив (Мульти-конфиг)

JSON-массив, содержащий несколько конфигураций Xray с именами. Этот формат удобен при экспорте конфигураций из GUI-клиентов или управлении несколькими именованными конфигурациями в одном файле.

Пример:

```bash
SUBSCRIPTION_URL=file:///path/to/configs.json
```

Формат файла:

```json
[
  {
    "remarks": "US Server 1",
    "outbounds": [
      {
        "protocol": "vless",
        "settings": {
          "vnext": [
            {
              "address": "us1.example.com",
              "port": 443,
              "users": [{ "id": "uuid-1", "encryption": "none" }]
            }
          ]
        },
        "streamSettings": { "network": "tcp", "security": "tls" }
      }
    ]
  },
  {
    "remarks": "EU Server 1",
    "outbounds": [
      {
        "protocol": "trojan",
        "settings": {
          "servers": [
            {
              "address": "eu1.example.com",
              "port": 443,
              "password": "password123"
            }
          ]
        },
        "streamSettings": { "network": "tcp", "security": "tls" }
      }
    ]
  }
]
```

Поле `remarks` из каждой конфигурации будет использоваться как имя прокси в панели управления.

### 5. Папка с конфигурациями

Директория, содержащая несколько JSON-файлов конфигурации V2Ray/Xray.

Пример:

```bash
SUBSCRIPTION_URL=folder:///path/to/configs
```

Требования:

- Директория должна содержать .json файлы
- Каждый файл следует формату JSON V2Ray
- Файлы обрабатываются в алфавитном порядке
- Некорректные файлы пропускаются с предупреждением

### 6. JSON-подписка (балансировщики)

Некоторые панели (например, Remnawave) отдают полный JSON-конфиг Xray вместо Base64-списка ссылок и объединяют несколько серверов в один **балансировщик** (`balancer` / `leastPing`). При стандартном формате Base64 такая группа сворачивается в одну запись, поэтому невозможно понять, какой именно узел недоступен.

Включите [`SUBSCRIPTION_JSON_FORMAT`](/ru/configuration/envs#subscription_json_format), чтобы запросить JSON-форму и развернуть каждый outbound внутри балансировщика в **отдельно проверяемый прокси**:

```bash
SUBSCRIPTION_URL=https://panel.example.com/sub
SUBSCRIPTION_JSON_FORMAT=true
```

Узлы внутри группы именуются как `<group> | <node>` и имеют общий `group_name` (используется [сгруппированным дашбордом](/ru/configuration/status-page) и [меткой метрики `group_name`](/ru/integrations/metrics)). При включённом формате JSON запрос отправляется с похожим на приложение `User-Agent`; переопределите его через `SUBSCRIPTION_USER_AGENT`, если ваша панель ожидает определённый клиент.

### 7. SOCKS, HTTP и HTTPS прокси

Помимо протоколов Xray, можно проверять работоспособность обычных форвард-прокси. Добавляйте их как строки подписки (из любого источника — URL, `base64://`, `file://` или inline):

```
socks://base64(user:pass)@host:port#name
socks5://user:pass@host:port#name
http://user:pass@host:port#name
https://user:pass@host:port#name
```

- `socks://`, `socks5://` и `socks5h://` соответствуют SOCKS outbound. Учётные данные могут быть как обычными `user:pass`, так и (для `socks://`) стандартным Base64-кодированным токеном `user:pass`.
- `http://` — обычный HTTP CONNECT прокси.
- `https://` — HTTP-прокси, доступный через TLS.
- Фрагмент `#name` задаёт отображаемое имя (по умолчанию `host:port`).

Для `https://` прокси с самоподписанным или приватным сертификатом закрепите его вместо отключения проверки (xray-core больше не поддерживает `allowInsecure`):

```
https://user:pass@host:port?pinnedPeerCertSha256=<sha256-hex>#name
https://user:pass@host:port?sni=real.example.com&verifyPeerCertByName=real.example.com#name
```

| Параметр запроса | Псевдоним | Описание |
|-------------|-------|-------------|
| `pinnedPeerCertSha256` | `pcs` | Принять сертификат пира, чей SHA-256 (hex, двоеточия допускаются) совпадает — для самоподписанных/внутренних сертификатов |
| `verifyPeerCertByName` | `vcn` | Проверять сертификат по этому имени вместо хоста |
| `sni` | — | TLS Server Name (по умолчанию совпадает с хостом) |

## Кастомные заголовки запросов

Панели, закрывающие подписку токеном или ожидающие определённого клиента, можно удовлетворить с помощью кастомного `User-Agent` и произвольных заголовков:

```bash
SUBSCRIPTION_USER_AGENT="Happ/1.0"
SUBSCRIPTION_HEADERS="X-Token: abc, X-Region: eu"
```

См. [`SUBSCRIPTION_USER_AGENT`](/ru/configuration/envs#subscription_user_agent) и [`SUBSCRIPTION_HEADERS`](/ru/configuration/envs#subscription_headers).
