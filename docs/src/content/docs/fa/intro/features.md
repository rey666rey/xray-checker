---
title: امکانات
description: امکانات Xray Checker
tableOfContents: false
---

### 🚀 امکانات اصلی

- 🔍 نظارت بر سلامت سرورهای پروکسی Xray با پشتیبانی از پروتکل‌های مختلف (VLESS، VMess، Trojan، Shadowsocks، Hysteria2)

- 🧦 بررسی [پروکسی‌های فورواردِ ساده SOCKS / HTTP / HTTPS](/fa/configuration/subscription#۷-پروکسیهای-socks-http-و-https) در کنار پروتکل‌های Xray

- 🔄 به‌روزرسانی خودکار پیکربندی پروکسی از URL‌های اشتراک با [فواصل قابل تنظیم](/fa/configuration/envs#subscription_update_interval)

- 📊 [صدور متریک‌ها](/fa/integrations/metrics) در فرمت Prometheus با اطلاعات وضعیت و تأخیر پروکسی

- 🌓 رابط وب با قالب تاریک/روشن برای نظارت بر وضعیت تمام نقاط پایانی پروکسی

  - 🔍 جستجو و فیلتر پروکسی‌ها بر اساس نام یا وضعیت
  - 📊 مرتب‌سازی بر اساس نام، تأخیر یا وضعیت
  - 🗂️ نمای گروه‌بندی‌شده و قابل جمع‌شدن برای گروه‌های متعادل‌کننده/اشتراک
  - 🔄 به‌روزرسانی خودکار بدون بارگذاری مجدد صفحه
  - 🎨 [سفارشی‌سازی کامل](/fa/configuration/web-customization) — لوگو، استایل‌ها یا کل قالب سفارشی

- 🌐 [REST API](/fa/usage/api-reference) با مستندات OpenAPI/Swagger

### 📝 فرمت‌ها و پیکربندی

- 📋 [پشتیبانی از فرمت‌های مختلف پیکربندی](/fa/configuration/subscription):

  - 🔗 لینک اشتراک (با پشتیبانی از چندین URL)
  - 🔐 رشته‌های کدگذاری شده Base64
  - 📄 فایل‌های JSON V2Ray/Xray
  - 📦 آرایه JSON Xray (چند کانفیگ)
  - 📁 پوشه‌های پیکربندی
  - ⚖️ [اشتراک‌های JSON با متعادل‌کننده](/fa/configuration/subscription#۶-اشتراک-json-متعادلکنندهها) — هر نود به‌صورت جداگانه ردیابی می‌شود
  - 🧦 پروکسی‌های فورواردِ SOCKS / HTTP / HTTPS

- 🔧 هدرهای درخواست اشتراک سفارشی و `User-Agent` برای پنل‌هایی که با توکن محدود شده‌اند یا مخصوص اپلیکیشن هستند

### 🔌 یکپارچه‌سازی‌ها

- 🌐 [REST API](/fa/usage/api-reference) با مستندات OpenAPI/Swagger برای یکپارچه‌سازی‌های سفارشی

- 📄 [صفحه وضعیت عمومی](/fa/configuration/status-page) برای سرویس‌های VPN — نمایش وضعیت پروکسی بدون احراز هویت، عنوان قابل تنظیم از نام اشتراک

- 📥 [تولید خودکار نقطه پایانی](/fa/integrations/uptime-kuma) برای یکپارچه‌سازی با سیستم‌های نظارتی (مثلاً Uptime-Kuma)

- ⏱️ [شبیه‌سازی تأخیر](/fa/configuration/advanced-conf) برای endpointها جهت اطمینان از تست دقیق سیستم نظارتی

- 📡 [یکپارچه‌سازی با Prometheus Pushgateway](/fa/integrations/prometheus#pushgateway-integration) برای ارسال متریک‌ها به سیستم‌های نظارتی خارجی

### ⚡ روش‌های بررسی

- 🔧 [پشتیبانی از سه روش تأیید اتصال پروکسی](/fa/configuration/check-methods):

  - 🌐 از طریق مقایسه آدرس IP
  - ✅ از طریق بررسی کد وضعیت HTTP
  - 📥 از طریق تأیید دانلود فایل

- ⏱️ اندازه‌گیری دقیق تأخیر با استفاده از TTFB (زمان تا اولین بایت)

### 🔒 امنیت

- 🛡️ [محافظت از متریک‌ها و رابط وب](/fa/configuration/advanced-conf#security-settings) با استفاده از احراز هویت پایه (Basic Authentication)

### 🚀 استقرار

- 🐳 قابلیت اجرا هم در [کانتینر Docker](/fa/usage/docker) (شامل Docker Compose) و هم به عنوان [برنامه CLI مستقل](/fa/usage/cli)

:::tip[💡 شروع سریع]
برای شروع استفاده از Xray Checker همین الان، به بخش [شروع سریع](/fa/intro/quick-start) بروید
:::
