//go:build !linux

package checker

import (
	"net"
	"time"
)

func newInterfaceDialer(_ string, timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, KeepAlive: -1}
}
