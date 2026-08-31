package web

import (
	"fmt"
	"net/http"
	"os"
)

type HealthCheck func() error

func HealthHandler(checks ...HealthCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for _, check := range checks {
			if check == nil {
				continue
			}
			if err := check(); err != nil {
				http.Error(w, "UNHEALTHY: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}

// PersistentStorageHealthCheck detects both a nearly full filesystem and a
// volume that has become read-only. The tiny write is removed immediately.
func PersistentStorageHealthCheck(directory string, minimumFreeBytes uint64) HealthCheck {
	return func() error {
		available, supported, err := availableDiskBytes(directory)
		if err != nil {
			return fmt.Errorf("persistent storage unavailable: %w", err)
		}
		if supported && available < minimumFreeBytes {
			return fmt.Errorf("persistent storage low: %d MiB free, %d MiB required", available/(1024*1024), minimumFreeBytes/(1024*1024))
		}
		file, err := os.CreateTemp(directory, ".health-write-*")
		if err != nil {
			return fmt.Errorf("persistent storage is not writable: %w", err)
		}
		name := file.Name()
		if _, err = file.Write([]byte("ok")); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		removeErr := os.Remove(name)
		if err != nil {
			return fmt.Errorf("persistent storage write failed: %w", err)
		}
		if closeErr != nil {
			return fmt.Errorf("persistent storage close failed: %w", closeErr)
		}
		if removeErr != nil {
			return fmt.Errorf("persistent storage cleanup failed: %w", removeErr)
		}
		return nil
	}
}
