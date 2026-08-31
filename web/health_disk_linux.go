//go:build linux

package web

import "golang.org/x/sys/unix"

func availableDiskBytes(path string) (uint64, bool, error) {
	var stats unix.Statfs_t
	if err := unix.Statfs(path, &stats); err != nil {
		return 0, true, err
	}
	return stats.Bavail * uint64(stats.Bsize), true, nil
}
