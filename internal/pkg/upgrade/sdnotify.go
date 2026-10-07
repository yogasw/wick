package upgrade

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// Notify sends one sd_notify datagram to systemd. No-op (nil) when the
// process was not started by systemd with NotifyAccess, so callers can fire
// and forget on every platform.
//
// Written by hand rather than pulled from a dependency: the protocol is a
// newline-separated KEY=VALUE blob on a unix datagram socket, and wick only
// ever sends four of them.
func Notify(state string) error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" || state == "" {
		return nil
	}
	// A leading '@' means the abstract namespace; systemd spells it that way.
	if strings.HasPrefix(addr, "@") {
		addr = "\x00" + addr[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(state))
	return err
}

// NotifyReady tells systemd this process is serving AND that it is now the
// unit's main process. The MAINPID line is what makes a tableflip upgrade
// survivable under systemd: after the handoff the unit must follow the CHILD,
// otherwise the parent's exit looks like the service dying and systemd kills
// the cgroup — taking the freshly-started process with it.
//
// Requires NotifyAccess=all in the unit, because the notification comes from
// a process systemd did not itself fork.
func NotifyReady() error {
	return Notify(fmt.Sprintf("READY=1\nMAINPID=%d", os.Getpid()))
}

// NotifyStopping declares that the whole SERVICE is going down. Only correct
// from the process systemd currently tracks as MAINPID, and never on the
// graceful-upgrade path: a draining predecessor is not the service, and this
// there makes systemd deactivate the unit and kill the successor.
func NotifyStopping() error { return Notify("STOPPING=1") }

// NotifyStatus sets the one-line status shown by `systemctl status`.
func NotifyStatus(msg string) error { return Notify("STATUS=" + msg) }
