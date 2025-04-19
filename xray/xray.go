package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"text/template"
	"xray-checker/checker"
	"xray-checker/config"
	"xray-checker/models"
	"xray-checker/runner"
	"xray-checker/web"
)

type TemplateData struct {
	Proxies      []*models.ProxyConfig
	StartPort    int
	XrayLogLevel string
}

func generateConfig(proxies []*models.ProxyConfig, startPort int, xrayLogLevel string) ([]byte, error) {
	if len(proxies) == 0 {
		return nil, fmt.Errorf("no valid proxy configurations found")
	}

	data := TemplateData{
		Proxies:      proxies,
		StartPort:    startPort,
		XrayLogLevel: xrayLogLevel,
	}

	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"toJson": func(v interface{}) string {
			b, err := json.Marshal(v)
			if err != nil {
				return "null"
			}
			return string(b)
		},
	}

	tmpl, err := template.New("xray.json.tmpl").
		Funcs(funcMap).
		ParseFS(templates, "templates/xray.json.tmpl")
	if err != nil {
		return nil, fmt.Errorf("error parsing template: %v", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("error executing template: %v", err)
	}

	var jsonCheck interface{}
	if err := json.Unmarshal(buf.Bytes(), &jsonCheck); err != nil {
		log.Printf("Generated invalid JSON: %s", buf.String())
		return nil, fmt.Errorf("invalid JSON generated: %v", err)
	}

	return buf.Bytes(), nil
}

func saveConfig(config []byte, filename string) error {
	if err := os.WriteFile(filename, config, 0644); err != nil {
		return fmt.Errorf("error writing config file: %v", err)
	}
	return nil
}

func PrepareProxyConfigs(proxies []*models.ProxyConfig) {
	for i := range proxies {
		proxies[i].Index = i

		if proxies[i].StableID == "" {
			proxies[i].StableID = proxies[i].GenerateStableID()
		}
	}
}

func GenerateAndSaveConfig(proxies []*models.ProxyConfig, startPort int, filename string, xrayLogLevel string) error {
	configBytes, err := generateConfig(proxies, startPort, xrayLogLevel)
	if err != nil {
		return fmt.Errorf("error generating config: %v", err)
	}

	if err := saveConfig(configBytes, filename); err != nil {
		return fmt.Errorf("error saving config: %v", err)
	}

	return nil
}

func UpdateConfiguration(newConfigs []*models.ProxyConfig, currentConfigs *[]*models.ProxyConfig,
	xrayRunner *runner.XrayRunner, proxyChecker *checker.ProxyChecker) error {

	log.Println("Found changes in subscription, updating configuration...")

	PrepareProxyConfigs(newConfigs)

	configFile := "xray_config.json"
	if err := GenerateAndSaveConfig(newConfigs, config.CLIConfig.Xray.StartPort, configFile, config.CLIConfig.Xray.LogLevel); err != nil {
		return fmt.Errorf("error generating new Xray config: %v", err)
	}

	if err := xrayRunner.Stop(); err != nil {
		return fmt.Errorf("error stopping Xray: %v", err)
	}

	if err := xrayRunner.Start(); err != nil {
		return fmt.Errorf("error starting Xray with new config: %v", err)
	}

	proxyChecker.UpdateProxies(newConfigs)

	*currentConfigs = newConfigs

	web.RegisterConfigEndpoints(newConfigs, proxyChecker, config.CLIConfig.Xray.StartPort)

	log.Println("Configuration updated successfully")
	return nil
}

func IsConfigsEqual(old, new []*models.ProxyConfig) bool {
	if len(old) != len(new) {
		return false
	}

	oldMap := make(map[string]bool)
	newMap := make(map[string]bool)

	for _, cfg := range old {
		if cfg.StableID == "" {
			cfg.StableID = cfg.GenerateStableID()
		}
		oldMap[cfg.StableID] = true
	}

	for _, cfg := range new {
		if cfg.StableID == "" {
			cfg.StableID = cfg.GenerateStableID()
		}
		newMap[cfg.StableID] = true
	}

	for id := range oldMap {
		if !newMap[id] {
			return false
		}
	}

	for id := range newMap {
		if !oldMap[id] {
			return false
		}
	}

	return true
}
