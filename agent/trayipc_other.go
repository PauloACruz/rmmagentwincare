//go:build !windows && !linux && !darwin
// +build !windows,!linux,!darwin

package agent

import (
	"errors"
	"net"
)

func listenTrayIPC() (net.Listener, error) {
	return nil, errors.New("tray IPC not supported on this platform")
}

func trayPeerUsername(conn net.Conn) (string, error) { return "", errors.New("not supported") }
