//go:build !windows
// +build !windows

package agent

import (
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func (a *Agent) platformHealthItems() []HealthItem {
	if runtime.GOOS == "darwin" {
		return []HealthItem{healthMacFirewall()}
	}
	return []HealthItem{healthSystemdFailed(), healthJournalErrors(), healthLinuxUpdates()}
}

func nonEmptyLines(out string) []string {
	lines := make([]string, 0)
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	return lines
}

func healthSystemdFailed() HealthItem {
	it := HealthItem{Key: "services", Label: "Servicos com falha (systemd)", Category: "sistema", Weight: 10, Status: healthUnknown}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return it
	}
	out, err := runHealthCmd(30*time.Second, "systemctl", "--failed", "--no-legend", "--plain")
	if err != nil {
		return it
	}
	units := make([]string, 0)
	for _, l := range nonEmptyLines(out) {
		units = append(units, strings.Fields(l)[0])
	}
	countThreshold(&it, len(units), 1, 4)
	it.Detail = limitList(units, 10)
	return it
}

func healthJournalErrors() HealthItem {
	it := HealthItem{Key: "events", Label: "Erros no log do sistema (24 h)", Category: "sistema", Weight: 10, Status: healthUnknown}
	if _, err := exec.LookPath("journalctl"); err != nil {
		return it
	}
	out, err := runHealthCmd(60*time.Second, "journalctl", "-p", "err", "--since", "24 hours ago", "-q", "--no-pager", "-o", "cat")
	if err != nil && out == "" {
		return it
	}
	countThreshold(&it, len(nonEmptyLines(out)), 5, 20)
	return it
}

func healthLinuxUpdates() HealthItem {
	it := HealthItem{Key: "updates", Label: "Atualizacoes pendentes", Category: "seguranca", Weight: 10, Status: healthUnknown}
	if _, err := exec.LookPath("apt-get"); err == nil {
		out, err := runHealthCmd(2*time.Minute, "apt-get", "-s", "-q", "upgrade")
		if err != nil {
			return it
		}
		n := 0
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "Inst ") {
				n++
			}
		}
		countThreshold(&it, n, 1, 30)
		return it
	}
	if _, err := exec.LookPath("dnf"); err == nil {
		out, _ := runHealthCmd(3*time.Minute, "dnf", "-q", "check-update")
		n := 0
		for _, l := range nonEmptyLines(out) {
			if len(strings.Fields(l)) == 3 && !strings.HasPrefix(l, "Obsoleting") {
				n++
			}
		}
		countThreshold(&it, n, 1, 30)
	}
	return it
}

func healthMacFirewall() HealthItem {
	it := HealthItem{Key: "firewall", Label: "Firewall do macOS", Category: "seguranca", Weight: 10, Status: healthUnknown}
	out, err := runHealthCmd(30*time.Second, "/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate")
	if err != nil {
		return it
	}
	if strings.Contains(strings.ToLower(out), "enabled") {
		it.Status, it.Value = healthOK, "Ativo"
	} else {
		it.Status, it.Value = healthWarning, "Desativado"
	}
	return it
}
