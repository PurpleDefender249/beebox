// Package sdnotify implements the systemd sd_notify(3) protocol without
// cgo or third-party dependencies.
package sdnotify

import (
	"net"
	"os"
	"strconv"
	"time"
)

// Notify sends state (e.g. "READY=1") to the service manager. It is a no-op
// when not running under systemd.
func Notify(state string) error {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return nil
	}
	addr := &net.UnixAddr{Name: sock, Net: "unixgram"}
	if sock[0] == '@' {
		addr.Name = "\x00" + sock[1:] // abstract namespace
	}
	conn, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(state))
	return err
}

// WatchdogInterval returns how often to send WATCHDOG=1 (half the
// configured WatchdogSec), or 0 if the watchdog is disabled.
func WatchdogInterval() time.Duration {
	usec := os.Getenv("WATCHDOG_USEC")
	if usec == "" {
		return 0
	}
	if pid := os.Getenv("WATCHDOG_PID"); pid != "" && pid != strconv.Itoa(os.Getpid()) {
		return 0
	}
	n, err := strconv.ParseInt(usec, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Microsecond / 2
}
