//go:build !linux

package web

func availableDiskBytes(_ string) (uint64, bool, error) {
	return 0, false, nil
}
