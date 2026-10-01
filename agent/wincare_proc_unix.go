//go:build !windows
// +build !windows

package agent

import (
	"os"
	"os/exec"
	"syscall"
)

// wincareBaseDir devolve onde criar o diretório temporário da execução ("" usa o TMPDIR).
func wincareBaseDir(a *Agent) string {
	return ""
}

// wincareMakeRunDir cria um diretório novo com permissão 0700 (apenas root).
func wincareMakeRunDir(base string) (string, error) {
	return os.MkdirTemp(base, "wincare-")
}

// Grupo de processos próprio para encerrar a árvore inteira no cancelamento ou no tempo limite.
func wincareSetProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func wincareKillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
