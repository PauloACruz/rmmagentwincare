//go:build linux || darwin
// +build linux darwin

package agent

import (
	"errors"
	"net"
	"os"
	"os/user"
	"strconv"
)

func listenTrayIPC() (net.Listener, error) {
	path := trayIPCPath()
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// Qualquer usuario local pode pedir o proprio token; a identidade vem do kernel (peerUID).
	if err := os.Chmod(path, 0o666); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func trayPeerUsername(conn net.Conn) (string, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return "", errors.New("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return "", err
	}
	var uid uint32
	var credErr error
	if err := raw.Control(func(fd uintptr) { uid, credErr = peerUID(int(fd)) }); err != nil {
		return "", err
	}
	if credErr != nil {
		return "", credErr
	}
	u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	if err != nil {
		return "", err
	}
	return u.Username, nil
}
