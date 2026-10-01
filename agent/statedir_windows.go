//go:build windows
// +build windows

package agent

import (
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wincareStateDir guarda arquivos de estado do agente na pasta de dados (ProgramData\TacticalRMM).
func wincareStateDir() string {
	return filepath.Join(defaultWinTmpDir, "state")
}

// ensureStateDir cria a pasta ja com DACL protegida (somente SYSTEM e Administradores).
func ensureStateDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o775); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	if err := windows.CreateDirectory(p, sa); err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return err
	}
	return nil
}
