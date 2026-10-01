package xray

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"xray-checker/logger"
)

const (
	geoSiteURL  = "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat"
	geoIPURL    = "https://github.com/v2fly/geoip/releases/latest/download/geoip.dat"
	geoSiteFile = "geo/geosite.dat"
	geoIPFile   = "geo/geoip.dat"
	maxGeoSize  = 128 * 1024 * 1024
	geoTimeout  = 60 * time.Second
)

type GeoFileManager struct {
	baseDir string
}

func NewGeoFileManager(baseDir string) *GeoFileManager {
	if baseDir == "" {
		if wd, err := os.Getwd(); err == nil {
			baseDir = wd
		} else {
			baseDir = "."
		}
	}

	return &GeoFileManager{
		baseDir: baseDir,
	}
}

func (gfm *GeoFileManager) EnsureGeoFiles() error {
	if err := gfm.ensureFile(geoSiteFile, geoSiteURL); err != nil {
		return fmt.Errorf("failed to ensure geosite.dat: %v", err)
	}

	if err := gfm.ensureFile(geoIPFile, geoIPURL); err != nil {
		return fmt.Errorf("failed to ensure geoip.dat: %v", err)
	}

	return nil
}

func (gfm *GeoFileManager) ensureFile(filename, url string) error {
	filePath := filepath.Join(gfm.baseDir, filename)

	if _, err := os.Stat(filePath); err == nil {
		return nil
	}

	logger.Info("Downloading %s...", filename)

	fileDir := filepath.Dir(filePath)
	if err := os.MkdirAll(fileDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	if err := gfm.downloadFile(url, filePath); err != nil {
		return fmt.Errorf("failed to download %s: %v", filename, err)
	}

	logger.Info("Downloaded %s", filename)
	return nil
}

func (gfm *GeoFileManager) downloadFile(url, filePath string) error {
	client := &http.Client{Timeout: geoTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP request failed with status: %d", resp.StatusCode)
	}

	file, err := os.CreateTemp(filepath.Dir(filePath), ".geo-download-*")
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)

	written, err := io.Copy(file, io.LimitReader(resp.Body, maxGeoSize+1))
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to write file: %v", err)
	}
	if written == 0 || written > maxGeoSize {
		_ = file.Close()
		return fmt.Errorf("downloaded file has invalid size: %d bytes", written)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to sync file: %v", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close file: %v", err)
	}
	if err := os.Rename(temporary, filePath); err != nil {
		return fmt.Errorf("failed to install file: %v", err)
	}
	return nil
}
