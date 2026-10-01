//go:build !windows
// +build !windows

package agent

import "os"

// wincareStateDir guarda arquivos de estado do agente (posicao dos logs etc.).
func wincareStateDir() string {
	return "/var/lib/wincare-agent"
}

func ensureStateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}
