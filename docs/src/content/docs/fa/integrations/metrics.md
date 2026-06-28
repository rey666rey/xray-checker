---
title: متریک‌ها
description: گزینه‌ها و مثال‌های متریک‌ها
---

Xray Checker دو متریک Prometheus برای نظارت بر وضعیت و عملکرد پروکسی ارائه می‌دهد. برای دستورالعمل‌های راه‌اندازی دقیق، [یکپارچه‌سازی Prometheus](/fa/integrations/prometheus) را ببینید.

برای به تصویر کشیدن متریک‌ها، توصیه می‌کنیم از [Grafana](/fa/integrations/grafana) استفاده کنید.

### xray_proxy_status

متریک وضعیت که در دسترس بودن پروکسی را نشان می‌دهد:

- نوع: Gauge
- مقادیر: ۱ (کار می‌کند) یا ۰ (ناموفق)
- برچسب‌ها:
  - `protocol`: پروتکل پروکسی (vless/vmess/trojan/shadowsocks/hysteria/socks/http/wireguard)
  - `address`: آدرس و پورت سرور
  - `name`: نام پیکربندی پروکسی
  - `stable_id`: شناسه پایدار به‌ازای هر پروکسی؛ حتی وقتی نام‌ها یکسان باشند هر سری را مجزا نگه می‌دارد و در طول راه‌اندازی‌های مجدد/تغییر ترتیب ثابت می‌ماند
  - `sub_name`: نام اشتراک (از فرگمنت URL یا هدر profile-title)
  - `group_name`: نام متعادل‌کننده/گروه برای پروکسی‌های گروه‌بندی‌شده (برای موارد بدون گروه خالی است)
  - `instance`: نام نمونه (اگر پیکربندی شده باشد)

:::tip
برای راه‌اندازی برچسب‌گذاری نمونه [پیکربندی پیشرفته](/fa/configuration/advanced-conf#instance-labeling) را ببینید.
:::

مثال:

```text
# HELP xray_proxy_status Status of proxy connection (1: success, 0: failure)
# TYPE xray_proxy_status gauge
xray_proxy_status{protocol="vless",address="example.com:443",name="proxy1",stable_id="a1b2c3d4e5f67890",sub_name="Premium VPN",group_name="",instance="dc1"} 1
```

### xray_proxy_latency_ms

متریک تأخیر که زمان پاسخ اتصال را نشان می‌دهد:

- نوع: Gauge
- مقادیر: میلی‌ثانیه (۰ اگر ناموفق)
- برچسب‌ها: همانند xray_proxy_status

مثال:

```text
# HELP xray_proxy_latency_ms Latency of proxy connection in milliseconds
# TYPE xray_proxy_latency_ms gauge
xray_proxy_latency_ms{protocol="vless",address="example.com:443",name="proxy1",stable_id="a1b2c3d4e5f67890",sub_name="Premium VPN",group_name="",instance="dc1"} 156
```

### لیبل‌های سفارشی

یک outbound در [اشتراک JSON](/fa/configuration/subscription#۹-لیبلهای-سفارشی-متریک) می‌تواند یک شیء `metricsLabels` تعریف کند. مدخل‌های آن به‌عنوان لیبل‌های اضافی روی هر دو متریک آن پروکسی، در کنار لیبل‌های داخلی بالا، افزوده می‌شوند:

```text
xray_proxy_status{protocol="trojan",address="1.1.1.1:443",name="proxy1",stable_id="a1b2c3d4e5f67890",sub_name="",group_name="",location="Netherlands, Amsterdam",hoster="FreeVDS"} 1
```

پروکسی‌های مختلف می‌توانند کلیدهای لیبل متفاوتی داشته باشند؛ پروکسی بدون `metricsLabels` تنها مجموعهٔ داخلی را نگه می‌دارد. کلیدها به نام‌های معتبر Prometheus تبدیل می‌شوند و نمی‌توانند لیبل‌های داخلی را بازنویسی کنند. متریک‌ها در هر scrape از مجموعهٔ فعلی پروکسی‌ها ساخته می‌شوند، بنابراین لیبل‌های سفارشی می‌توانند هنگام به‌روزرسانی اشتراک افزوده، تغییر یا حذف شوند بدون آنکه سری‌های دیگر به ۰ بازنشانی شوند.
