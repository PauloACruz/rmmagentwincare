//go:build windows
// +build windows

package agent

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wincareBaseDir usa a pasta de trabalho do agente (ProgramData\TacticalRMM por padrão).
func wincareBaseDir(a *Agent) string {
	if a != nil && a.WinTmpDir != "" {
		return a.WinTmpDir
	}
	return defaultWinTmpDir
}

// wincareMakeRunDir cria o diretório já com DACL protegida (somente SYSTEM e Administradores),
// sem janela entre a criação e a troca de permissões.
func wincareMakeRunDir(base string) (string, error) {
	if base == "" {
		base = os.TempDir()
	}
	if err := os.MkdirAll(base, 0o775); err != nil {
		return "", err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return "", err
	}
	sa := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	for i := 0; i < 5; i++ {
		rnd := make([]byte, 12)
		if _, err := rand.Read(rnd); err != nil {
			return "", err
		}
		dir := filepath.Join(base, "wincare-"+hex.EncodeToString(rnd))
		p, err := windows.UTF16PtrFromString(dir)
		if err != nil {
			return "", err
		}
		err = windows.CreateDirectory(p, sa)
		if err == nil {
			return dir, nil
		}
		if err != windows.ERROR_ALREADY_EXISTS {
			return "", err
		}
	}
	return "", os.ErrExist
}

func wincareSetProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}

// Mesmo mecanismo usado pelo agente para encerrar processos: taskkill /T /F para a árvore,
// com KillProc como alternativa.
func wincareKillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	tk := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
	kill := exec.Command(tk, "/T", "/F", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err := kill.Run(); err != nil {
		return KillProc(int32(pid))
	}
	return nil
}
