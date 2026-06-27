---
title: فرمت اشتراک
description: گزینه‌ها و مثال‌های فرمت اشتراک
---

Xray Checker از پنج فرمت مختلف برای پیکربندی پروکسی پشتیبانی می‌کند. از [متغیر محیطی](/fa/configuration/envs#subscription_url) `SUBSCRIPTION_URL` برای تنظیم روش بررسی استفاده کنید.

برای اطلاعات درباره نحوه تأیید پروکسی‌ها، [روش‌های بررسی](/fa/configuration/check-methods) را ببینید.

### ۱. آدرس اشتراک (پیش‌فرض)

آدرس اشتراک استاندارد که لیست کدگذاری شده Base64 از لینک‌های پروکسی را برمی‌گرداند.

مثال:

```bash
SUBSCRIPTION_URL=https://example.com/subscription
```

الزامات:

- آدرس HTTPS
- محتوای کدگذاری شده Base64 برگرداند
- محتوا آدرس‌های پروکسی با خط جدید (کاراکتر newline) از همدیگر جدا شده باشند
- از هدرهای استاندارد User-Agent پشتیبانی کند

هدرهای ارسالی:

```
Accept: */*
User-Agent: Xray-Checker
```

### ۲. رشته Base64

رشته مستقیم کدگذاری شده Base64 حاوی لینک‌های پیکربندی پروکسی.

مثال:

```bash
SUBSCRIPTION_URL=dmxlc3M6Ly91dWlkQGV4YW1wbGUuY29tOjQ0MyVlbmNyeXB0aW9uPW5vbmUmc2VjdXJpdHk9dGxzI3Byb3h5MQ==
```

فرمت محتوا (قبل از کدگذاری):

```
vless://uuid@example.com:443?encryption=none&security=tls#proxy1
trojan://password@example.com:443?security=tls#proxy2
vmess://base64encodedconfig
ss://base64encodedconfig
hysteria2://password@example.com:443?sni=example.com#proxy5
```

:::note[پروتکل‌های پشتیبانی‌شده]
VLESS، VMess، Trojan، Shadowsocks و **Hysteria2** پشتیبانی می‌شوند، از جمله ترنسپورت‌هایی مانند Reality، xhttp، gRPC، WebSocket و mKCP. همچنین **پروکسی‌های فورواردِ ساده SOCKS / HTTP / HTTPS** نیز قابل بررسی هستند — به بخش [پروکسی‌های SOCKS، HTTP و HTTPS](#۷-پروکسیهای-socks-http-و-https) در ادامه مراجعه کنید.
:::

### ۳. فایل JSON V2Ray

فایل پیکربندی JSON تکی در فرمت V2Ray/Xray.

مثال:

```bash
SUBSCRIPTION_URL=file:///path/to/config.json
```

فرمت فایل:

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

### ۴. آرایه JSON Xray (چند پیکربندی)

آرایه JSON حاوی چندین پیکربندی Xray با remarks. این فرمت برای صادر کردن پیکربندی‌ها از کلاینت‌های GUI یا مدیریت چندین پیکربندی نام‌گذاری شده در یک فایل مفید است.

مثال:

```bash
SUBSCRIPTION_URL=file:///path/to/configs.json
```

فرمت فایل:

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

فیلد `remarks` از هر پیکربندی به عنوان نام پروکسی در داشبورد استفاده خواهد شد.

### ۵. پوشه پیکربندی

دایرکتوری حاوی چندین فایل پیکربندی JSON V2Ray/Xray.

مثال:

```bash
SUBSCRIPTION_URL=folder:///path/to/configs
```

الزامات:

- دایرکتوری باید حاوی فایل‌های .json باشد
- هر فایل از فرمت JSON V2Ray پیروی می‌کند
- فایل‌ها به ترتیب الفبایی پردازش می‌شوند
- فایل‌های نامعتبر با هشدار نادیده گرفته می‌شوند

### ۶. اشتراک JSON (متعادل‌کننده‌ها)

بعضی پنل‌ها (مثلاً Remnawave) به‌جای لیست لینک Base64، یک پیکربندی کامل JSON مربوط به Xray ارائه می‌دهند و چند سرور را زیر یک **متعادل‌کننده** (`balancer` / `leastPing`) گروه‌بندی می‌کنند. با فرمت پیش‌فرض Base64 چنین گروهی در یک ورودی واحد جمع می‌شود، بنابراین نمی‌توان تشخیص داد کدام نود از کار افتاده است.

برای درخواست فرمت JSON و بسط دادن هر outbound داخل یک متعادل‌کننده به یک **پروکسی که جداگانه بررسی می‌شود**، گزینه [`SUBSCRIPTION_JSON_FORMAT`](/fa/configuration/envs#subscription_json_format) را فعال کنید:

```bash
SUBSCRIPTION_URL=https://panel.example.com/sub
SUBSCRIPTION_JSON_FORMAT=true
```

نودهای داخل یک گروه با الگوی `<group> | <node>` نام‌گذاری می‌شوند و یک `group_name` مشترک دارند (که توسط [داشبورد گروه‌بندی‌شده](/fa/configuration/status-page) و [برچسب متریک `group_name`](/fa/integrations/metrics) استفاده می‌شود). وقتی فرمت JSON فعال باشد، درخواست با یک `User-Agent` شبیه به اپلیکیشن ارسال می‌شود؛ اگر پنل شما کلاینت خاصی انتظار دارد، آن را با `SUBSCRIPTION_USER_AGENT` بازنویسی کنید.

### ۷. پروکسی‌های SOCKS، HTTP و HTTPS

علاوه بر پروتکل‌های Xray، پروکسی‌های فورواردِ ساده نیز می‌توانند از نظر سلامت بررسی شوند. آن‌ها را به‌عنوان خطوط اشتراک اضافه کنید (از هر منبعی — URL، `base64://`، `file://` یا به‌صورت درون‌خطی):

```
socks://base64(user:pass)@host:port#name
socks5://user:pass@host:port#name
http://user:pass@host:port#name
https://user:pass@host:port#name
```

- `socks://`، `socks5://` و `socks5h://` به یک outbound از نوع SOCKS نگاشت می‌شوند. اعتبارنامه‌ها می‌توانند به‌صورت ساده `user:pass` باشند یا برای `socks://`، توکن استاندارد `user:pass` که با Base64 کدگذاری شده است.
- `http://` یک پروکسی ساده HTTP CONNECT است.
- `https://` یک پروکسی HTTP است که از طریق TLS در دسترس قرار می‌گیرد.
- فرگمنت `#name` نام نمایشی را تعیین می‌کند (به‌صورت پیش‌فرض `host:port`).

برای یک پروکسی `https://` با گواهی خودامضا (self-signed) یا خصوصی، به‌جای غیرفعال کردن تأیید، آن را pin کنید (xray-core دیگر از `allowInsecure` پشتیبانی نمی‌کند):

```
https://user:pass@host:port?pinnedPeerCertSha256=<sha256-hex>#name
https://user:pass@host:port?sni=real.example.com&verifyPeerCertByName=real.example.com#name
```

| پارامتر کوئری | نام مستعار | توضیحات |
|-------------|-------|-------------|
| `pinnedPeerCertSha256` | `pcs` | پذیرش گواهی همتا که SHA-256 آن (هگزادسیمال، با امکان استفاده از دونقطه) مطابقت داشته باشد — برای گواهی‌های خودامضا/داخلی |
| `verifyPeerCertByName` | `vcn` | تأیید گواهی در برابر این نام به‌جای هاست |
| `sni` | — | نام سرور TLS (به‌صورت پیش‌فرض هاست) |

## هدرهای درخواست سفارشی

پنل‌هایی که اشتراک را پشت یک توکن یا کلاینت خاص قرار می‌دهند، می‌توانند با یک `User-Agent` سفارشی و هدرهای دلخواه برآورده شوند:

```bash
SUBSCRIPTION_USER_AGENT="Happ/1.0"
SUBSCRIPTION_HEADERS="X-Token: abc, X-Region: eu"
```

[`SUBSCRIPTION_USER_AGENT`](/fa/configuration/envs#subscription_user_agent) و [`SUBSCRIPTION_HEADERS`](/fa/configuration/envs#subscription_headers) را ببینید.
