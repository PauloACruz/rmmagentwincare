package ipc

import (
	"context"
	"net"

	winio "github.com/Microsoft/go-winio"
)

// DefaultPath e o named pipe criado pelo agente no Windows.
const DefaultPath = `\\.\pipe\wincare-tray`

func dial(ctx context.Context, path string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, path)
}
