// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

// https://astro.build/config
export default defineConfig({
  site: "https://xray-checker.kutovoy.dev",
  integrations: [
    starlight({
      title: "Xray Checker",
      editLink: {
        baseUrl: "https://github.com/kutovoys/xray-checker/edit/main/docs/",
      },
      customCss: ["./src/styles/custom.css"],
      social: {
        github: "https://github.com/kutovoys/xray-checker",
        telegram: "https://t.me/kutovoys",
        linkedin: "https://www.linkedin.com/in/kutovoys/",
      },
      defaultLocale: "root",
      locales: {
        root: {
          label: "English",
          lang: "en",
        },
        ru: {
          label: "Русский",
          lang: "ru",
        },
      },
      sidebar: [
        {
          label: "Introduction",
          translations: {
            ru: "Введение",
          },
          items: [
            {
              label: "Overview",
              translations: {
                ru: "Обзор",
              },
              slug: "index",
            },
            {
              label: "Features",
              translations: {
                ru: "Возможности",
              },
              slug: "intro/features",
            },
            {
              label: "Architecture",
              translations: {
                ru: "Архитектура",
              },
              slug: "intro/architecture",
            },
            {
              label: "Quick Start",
              translations: {
                ru: "Быстрый старт",
              },
              slug: "intro/quick-start",
            },
          ],
        },
        {
          label: "Usage",
          translations: {
            ru: "Использование",
          },
          items: [
            {
              label: "CLI",
              translations: {
                ru: "CLI",
              },
              slug: "usage/cli",
            },
            {
              label: "Docker",
              translations: {
                ru: "Docker",
              },
              slug: "usage/docker",
            },
            {
              label: "GitHub Actions",
              translations: {
                ru: "GitHub Actions",
              },
              slug: "usage/github-actions",
            },
            {
              label: "API Reference",
              translations: {
                ru: "API Reference",
              },
              slug: "usage/api-reference",
            },
            {
              label: "Troubleshooting",
              translations: {
                ru: "Устранение неполадок",
              },
              slug: "usage/troubleshooting",
            },
          ],
        },
        {
          label: "Configuration",
          translations: {
            ru: "Конфигурация",
          },
          items: [
            {
              label: "Environment Variables",
              translations: {
                ru: "Переменные окружения",
              },
              slug: "configuration/envs",
            },
            {
              label: "Subscription Format",
              translations: {
                ru: "Формат подписки",
              },
              slug: "configuration/subscription",
            },
            {
              label: "Check Methods",
              translations: {
                ru: "Методы проверки",
              },
              slug: "configuration/check-methods",
            },
            {
              label: "Advanced Configuration",
              translations: {
                ru: "Расширенная конфигурация",
              },
              slug: "configuration/advanced-conf",
            },
          ],
        },
        {
          label: "Integrations",
          translations: {
            ru: "Интеграции",
          },
          items: [
            {
              label: "Metrics",
              translations: {
                ru: "Метрики",
              },
              slug: "integrations/metrics",
            },
            {
              label: "Prometheus Setup",
              translations: {
                ru: "Настройка Prometheus",
              },
              slug: "integrations/prometheus",
            },
            {
              label: "Uptime Kuma",
              translations: {
                ru: "Uptime Kuma",
              },
              slug: "integrations/uptime-kuma",
            },
            {
              label: "Grafana Dashboards",
              translations: {
                ru: "Grafana Dashboard",
              },
              slug: "integrations/grafana",
              badge: { text: "WIP", variant: "caution" },
            },
            {
              label: "Alternatives",
              translations: {
                ru: "Альтернативы",
              },
              slug: "integrations/alternatives",
            },
          ],
        },
        {
          label: "Contributing",
          translations: {
            ru: "Участие в разработке",
          },
          items: [
            {
              label: "Development Guide",
              translations: {
                ru: "Руководство для разработчиков",
              },
              link: "/contributing/guide",
            },
          ],
        },
        {
          label: "Other Software",
          items: [
            {
              label: "Marzban Exporter",
              link: "https://github.com/kutovoys/marzban-exporter",
            },
            {
              label: "Marzban Torrent Blocker",
              link: "https://github.com/kutovoys/marzban-torrent-blocker",
            },
            {
              label: "Speedtest Exporter",
              link: "https://github.com/kutovoys/speedtest-exporter",
            },
          ],
        },
        {
          label: "VPN Recommendation",
          translations: {
            ru: "Рекомендуем",
          },
          items: [
            {
              label: "BlancVPN",
              link: "https://getblancvpn.com/?ref=xc-doc",
              badge: { text: "TRYBLANCVPN", variant: "success" },
              attrs: {
                class: "blanc-vpn-menu-item",
              },
            },
          ],
        },
      ],
    }),
  ],
});
