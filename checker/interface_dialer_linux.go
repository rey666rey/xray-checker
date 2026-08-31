//go:build linux

package checker

import (
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func newInterfaceDialer(interfaceName string, timeout time.Duration) *net.Dialer {
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: -1}
	if interfaceName == "" {
		return dialer
	}
	dialer.Control = func(_, _ string, connection syscall.RawConn) error {
		var bindErr error
		if err := connection.Control(func(fd uintptr) {
			bindErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, interfaceName)
		}); err != nil {
			return err
		}
		return bindErr
	}
	return dialer
}
