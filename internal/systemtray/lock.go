//go:build !headless

package systemtray

import (
	"fmt"
	"net"
)

// acquireSingleInstance binds 127.0.0.1:<port> as a process-lifetime
// lock. If another tray instance is already running on this machine,
// the bind fails and we return an error — caller should bail out
// instead of starting a second tray icon.
//
// The listener is intentionally never accepted from; it just holds
// the port for as long as this process is alive. The OS releases it
// on exit.
func acquireSingleInstance() (net.Listener, error) {
	const lockPort = 47829 // arbitrary, picked to be unlikely to collide
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", lockPort))
	if err != nil {
		return nil, fmt.Errorf("another instance is already running")
	}
	return ln, nil
}
