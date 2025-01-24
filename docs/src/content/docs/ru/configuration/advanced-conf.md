---
title: Расширенная конфигурация
description: Расширенные параметры конфигурации
---

### Альтернативные сервисы проверки IP

Вы можете использовать альтернативные сервисы проверки IP (подробнее в разделе [методы проверки](/ru/configuration/check-methods)):

- `http://ip.sb`
- `https://api64.ipify.org`
- `http://ifconfig.me`

Пример:

```bash
PROXY_IP_CHECK_URL=http://ip.sb
```

### Альтернативные URL проверки статуса

Альтернативные URL для проверки статуса (подробнее в разделе [методы проверки](/ru/configuration/check-methods)):

- `http://www.gstatic.com/generate_204`
- `http://www.qualcomm.cn/generate_204`
- `http://cp.cloudflare.com/generate_204`

Пример:

```bash
PROXY_STATUS_CHECK_URL=http://www.gstatic.com/generate_204
```

### Настройка безопасности

Включение аутентификации для чувствительных эндпоинтов:

```bash
METRICS_PROTECTED=true
METRICS_USERNAME=custom_user
METRICS_PASSWORD=secure_password
```

### Маркировка экземпляров

Добавление меток экземпляров для распределенных установок:

```bash
METRICS_INSTANCE=datacenter-1
```

### Интервалы обновления

Настройка интервалов проверки и обновления:

```bash
# Проверка каждую минуту
PROXY_CHECK_INTERVAL=60

# Обновление подписки каждый час
SUBSCRIPTION_UPDATE_INTERVAL=3600
```

### Настройка логирования

Настройка логирования Xray Core:

```bash
# Включение отладочного логирования
XRAY_LOG_LEVEL=debug

# Отключение логирования
XRAY_LOG_LEVEL=none
```

### Настройка портов

Настройка диапазонов портов:

```bash
# Начало портов SOCKS5 с 20000
XRAY_START_PORT=20000

# Изменение порта метрик
METRICS_PORT=9090
```

### Настройка на steal-from-yourself домене

Если вы прикрываетесь своим собственным доменом в xray и настраиваете xray-checker на том же сервере, где работает xray и nginx-сервер для вашего домена, и хотите, чтобы мониторинг был доступен по пути, например, 
 `your-stealing-domain.com/xray/monitor`, то настройка будет выглядеть так:

Запустите docker-контейнер с xray checher на порту, например, 2112 так, чтобы он был доступен только с lockalhost'а:

```bash
docker run -d \
  -e SUBSCRIPTION_URL=https://your-subscription-url/sub \
  -p 127.0.0.1:2112:2112 \
  -e METRICS_BASE_URL="/xray/monitor \
  kutovoys/xray-checker
```

Откройте конфиг nginx (`sudo nano /etc/nginx/your-stealing-domain.com`), найдите там главную секцию, она выглядит так:

```
server {
    root /var/www/your-stealing-domain.com/html;

    index index.html;
    server_name your-stealing-domain.com;
    ...
}
```

Добавьте в неё 2 новых location для переадресации запросов на запущенный xray-checker:

```config

    # Обработка адреса /xray/monitor (без слеша в конце)
    location = /xray/monitor {
        return 301 https://$host$request_uri/;
    }

    # Обработка адреса  /xray/monitor/ - редирект на xray-checker
    location /xray/monitor/ {
        proxy_pass http://127.0.0.1:2112/xray/monitor/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
```

протестируйте и перезапустите nginx:

```bash
sudo nginx -t
sudo systemctl reload nginx
```

и проверьте, что мониторинг работает:

```bash
 curl -I -L https://your-stealing-domain.com/xray/monitor
```