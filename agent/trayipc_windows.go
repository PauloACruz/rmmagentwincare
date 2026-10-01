package agent

import (
	"errors"
	"net"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const trayPipeName = `\\.\pipe\wincare-tray`

// SYSTEM e administradores com acesso total; usuarios autenticados podem ler e escrever.
const trayPipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;AU)"

func listenTrayIPC() (net.Listener, error) {
	return winio.ListenPipe(trayPipeName, &winio.PipeConfig{SecurityDescriptor: trayPipeSDDL, InputBufferSize: 4096, OutputBufferSize: 4096})
}

func trayPeerUsername(conn net.Conn) (string, error) {
	f, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return "", errors.New("not a named pipe")
	}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(windows.Handle(f.Fd()), &pid); err != nil {
		return "", err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(proc)
	var token windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()
	tu, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	for _, wk := range []windows.WELL_KNOWN_SID_TYPE{windows.WinLocalSystemSid, windows.WinLocalServiceSid, windows.WinNetworkServiceSid} {
		if tu.User.Sid.IsWellKnown(wk) {
			return "", errors.New("service accounts cannot use the tray")
		}
	}
	account, domain, _, err := tu.User.Sid.LookupAccount("")
	if err != nil {
		return "", err
	}
	return domain + `\` + account, nil
}
