//go:build !linux && !darwin && !windows

package ipc

import (
	"context"
	"errors"
	"net"
)

// DefaultPath nao existe em sistemas sem suporte.
const DefaultPath = ""

func dial(context.Context, string) (net.Conn, error) {
	return nil, errors.New("sistema operacional sem suporte")
}
