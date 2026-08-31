package config

import (
	"fmt"

	"github.com/alecthomas/kong"
)

var CLIConfig CLI
var Version string

func Parse(version string) {
	Version = version
	ctx := kong.Parse(&CLIConfig,
		kong.Name("xray-checker"),
		kong.Description("Xray Checker: A Prometheus exporter for monitoring Xray proxies"),
		kong.Vars{
			"version": version,
		},
	)
	_ = ctx
}

type CLI struct {
	Subscription struct {
		URLs           []string `name:"subscription-url" help:"URL(s) of the subscription (can be specified multiple times)" required:"true" env:"SUBSCRIPTION_URL"`
		Update         bool     `name:"subscription-update" help:"Whether to recheck the subscription" default:"true" env:"SUBSCRIPTION_UPDATE"`
		UpdateInterval int      `name:"subscription-update-interval" help:"Interval for subscription updates in seconds" default:"300" env:"SUBSCRIPTION_UPDATE_INTERVAL"`
		PoolSamples    int      `name:"subscription-pool-samples" help:"Independent subscription samples per update used to discover rotating host endpoints" default:"2" env:"SUBSCRIPTION_POOL_SAMPLES"`
		JSONFormat     bool     `name:"subscription-json-format" help:"Request full JSON configs from the panel (sends app-like headers so grouped/balancer nodes are returned individually instead of collapsed share links)" default:"false" env:"SUBSCRIPTION_JSON_FORMAT"`
		UserAgent      string   `name:"subscription-user-agent" help:"Custom User-Agent for subscription requests (overrides the default and the --subscription-json-format preset)" default:"" env:"SUBSCRIPTION_USER_AGENT"`
		Headers        []string `name:"subscription-header" help:"Extra HTTP header for subscription requests in 'Key: Value' form (repeatable; env: comma-separated)" env:"SUBSCRIPTION_HEADERS"`
	} `embed:"" prefix:""`

	Proxy struct {
		CheckInterval    int    `name:"proxy-check-interval" help:"Interval for proxy checks in seconds" default:"300" env:"PROXY_CHECK_INTERVAL"`
		InitialCheckOnly bool   `name:"proxy-initial-check-only" help:"Run one full check after startup without scheduled bulk rechecks; targeted repair checks continue" default:"false" env:"PROXY_INITIAL_CHECK_ONLY"`
		CheckConcurrency int    `name:"proxy-check-concurrency" help:"Max proxies checked in parallel per cycle (0 = unlimited)" default:"0" env:"PROXY_CHECK_CONCURRENCY"`
		CheckMethod      string `name:"proxy-check-method" help:"Method for checking proxy: urltest, ip, status or download" default:"ip" env:"PROXY_CHECK_METHOD"`
		IpCheckUrl       string `name:"proxy-ip-check-url" help:"Service URL for IP checking" default:"https://api.ipify.org?format=text" env:"PROXY_IP_CHECK_URL"`
		URLTestUrl       string `name:"proxy-url-test-url" help:"Small page used by check-method=urltest" default:"http://captive.apple.com/hotspot-detect.html" env:"PROXY_URL_TEST_URL"`
		URLTestExpected  string `name:"proxy-url-test-expected" help:"Substring required in a successful URL-test response (empty accepts any 2xx response)" default:"Success" env:"PROXY_URL_TEST_EXPECTED"`
		URLTestAttempts  int    `name:"proxy-url-test-attempts" help:"Number of independent URL-test requests; the best successful latency is retained" default:"2" env:"PROXY_URL_TEST_ATTEMPTS"`
		RetryTimeout     int    `name:"proxy-retry-timeout" help:"Per-request timeout in seconds for the slower failed-node retry pass" default:"10" env:"PROXY_RETRY_TIMEOUT"`
		RetryConcurrency int    `name:"proxy-retry-concurrency" help:"Concurrency for the failed-node retry pass (0 = half of normal concurrency)" default:"0" env:"PROXY_RETRY_CONCURRENCY"`
		StatusCheckUrl   string `name:"proxy-status-check-url" help:"Response status generator, used by check-method=status" default:"http://cp.cloudflare.com/generate_204" env:"PROXY_STATUS_CHECK_URL"`
		DownloadUrl      string `name:"proxy-download-url" help:"URL for file download checking, used by check-method=download" default:"https://proof.ovh.net/files/1Mb.dat" env:"PROXY_DOWNLOAD_URL"`
		DownloadTimeout  int    `name:"proxy-download-timeout" help:"Timeout for download checking in seconds" default:"60" env:"PROXY_DOWNLOAD_TIMEOUT"`
		DownloadMinSize  int64  `name:"proxy-download-min-size" help:"Minimum bytes to download for successful check" default:"51200" env:"PROXY_DOWNLOAD_MIN_SIZE"`
		Timeout          int    `name:"proxy-timeout" help:"Per-request timeout for proxy checks in seconds" default:"30" env:"PROXY_TIMEOUT"`
		SimulateLatency  bool   `name:"simulate-latency" help:"Whether to add latency to the response" default:"true" env:"SIMULATE_LATENCY"`
		ResolveDomains   bool   `name:"proxy-resolve-domains" help:"Resolve proxy server domains into IPs and expand configs" env:"PROXY_RESOLVE_DOMAINS"`
	} `embed:"" prefix:""`

	Xray struct {
		StartPort int    `name:"xray-start-port" help:"Start port for proxy configuration" default:"10000" env:"XRAY_START_PORT"`
		LogLevel  string `name:"xray-log-level" help:"Xray log level (debug|info|warning|error|none)" default:"none" env:"XRAY_LOG_LEVEL"`
	} `embed:"" prefix:""`

	Metrics struct {
		Host      string `name:"metrics-host" help:"Host to listen on" default:"0.0.0.0" env:"METRICS_HOST"`
		Port      string `name:"metrics-port" help:"Port to listen on" default:"2112" env:"METRICS_PORT"`
		Protected bool   `name:"metrics-protected" help:"Whether metrics are protected by basic auth" default:"false" env:"METRICS_PROTECTED"`
		Username  string `name:"metrics-username" help:"Username for metrics if protected by basic auth" default:"metricsUser" env:"METRICS_USERNAME"`
		Password  string `name:"metrics-password" help:"Password for metrics if protected by basic auth" default:"MetricsVeryHardPassword" env:"METRICS_PASSWORD"`
		Instance  string `name:"metrics-instance" help:"Instance label for metrics" default:"" env:"METRICS_INSTANCE"`
		PushURL   string `name:"metrics-push-url" help:"Prometheus pushgateway URL (e.g. https://user:pass@host:port)" default:"" env:"METRICS_PUSH_URL"`
		BasePath  string `name:"metrics-base-path" help:"URL path to metrics (e.g. /xray/metrics)" default:"" env:"METRICS_BASE_PATH"`
	} `embed:"" prefix:""`

	Web struct {
		ShowServerDetails   bool   `name:"web-show-details" help:"Show server IP addresses and ports in web UI" default:"false" env:"WEB_SHOW_DETAILS"`
		Public              bool   `name:"web-public" help:"Make dashboard public (requires --metrics-protected)" default:"false" env:"WEB_PUBLIC"`
		TrustedExternalAuth bool   `name:"web-trusted-external-auth" help:"Allow server details in public mode when an external auth proxy protects the dashboard" default:"false" env:"WEB_TRUSTED_EXTERNAL_AUTH"`
		PublicShowProtocol  bool   `name:"web-public-show-protocol" help:"Show protocol badge on cards in public mode (always shown when not public)" default:"false" env:"WEB_PUBLIC_SHOW_PROTOCOL"`
		CustomAssetsPath    string `name:"web-custom-assets-path" help:"Path to custom assets directory (logo.svg, favicon.ico, custom.css, index.html)" default:"" env:"WEB_CUSTOM_ASSETS_PATH"`
	} `embed:"" prefix:""`

	Alerts struct {
		SettingsFile  string `name:"alerts-settings-file" help:"Path to persistent Telegram alert settings" default:"" env:"ALERTS_SETTINGS_FILE"`
		TelegramToken string `name:"telegram-bot-token" help:"Telegram bot token managed through the environment" default:"" env:"TELEGRAM_BOT_TOKEN"`
		TelegramChat  int64  `name:"telegram-chat-id" help:"Telegram chat ID managed through the environment" default:"0" env:"TELEGRAM_CHAT_ID"`
		TelegramProxy string `name:"telegram-proxy-url" help:"HTTP or SOCKS proxy for Telegram managed through the environment" default:"" env:"TELEGRAM_PROXY_URL"`
	} `embed:"" prefix:""`

	NetworkStatusFile   string      `name:"network-status-file" help:"Path to a route-monitor status JSON file; unavailable status pauses proxy checks" default:"" env:"NETWORK_STATUS_FILE"`
	NetworkStatusMaxAge int         `name:"network-status-max-age" help:"Maximum network status age in seconds before checks are paused" default:"15" env:"NETWORK_STATUS_MAX_AGE"`
	ResultsFile         string      `name:"results-file" help:"Path to a persistent proxy-results snapshot" default:"" env:"RESULTS_FILE"`
	NodeHistoryFile     string      `name:"node-history-file" help:"Path to persistent logical-node repair history" default:"" env:"NODE_HISTORY_FILE"`
	NodeDiagnosisFile   string      `name:"node-diagnosis-file" help:"Path to persistent manual node-diagnosis history" default:"" env:"NODE_DIAGNOSIS_FILE"`
	AccessCheckFile     string      `name:"access-check-file" help:"Path to persistent direct-vs-VPN access-check history" default:"" env:"ACCESS_CHECK_FILE"`
	Version             VersionFlag `name:"version" help:"Print version information and quit"`
	RunOnce             bool        `name:"run-once" help:"Run one check cycle and exit" default:"false" env:"RUN_ONCE"`
	LogLevel            string      `name:"log-level" help:"Log level (debug|info|warn|error|none)" default:"info" env:"LOG_LEVEL"`
}

func (c *CLI) Validate() error {
	if c.Web.Public && !c.Metrics.Protected {
		return fmt.Errorf("--web-public requires --metrics-protected to be enabled")
	}
	return nil
}

type VersionFlag string

func (v VersionFlag) Decode(ctx *kong.DecodeContext) error { return nil }
func (v VersionFlag) IsBool() bool                         { return true }
func (v VersionFlag) BeforeApply(app *kong.Kong, vars kong.Vars) error {
	fmt.Println("Xray Checker: A Prometheus exporter for monitoring Xray proxies")
	fmt.Printf("Version:\t %s\n", vars["version"])
	fmt.Printf("GitHub: https://github.com/kutovoys/xray-checker\n")
	app.Exit(0)
	return nil
}
