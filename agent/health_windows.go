package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Servicos automaticos que costumam ficar parados por design (inicio por gatilho ou atualizadores).
var healthIgnoredServices = map[string]bool{
	"gupdate": true, "gupdatem": true, "edgeupdate": true, "edgeupdatem": true, "sppsvc": true, "mapsbroker": true,
	"remoteregistry": true, "tiledatamodelsvc": true, "wbiosrvc": true, "sharedaccess": true, "cdpsvc": true,
	"gpsvc": true, "trustedinstaller": true, "wuauserv": true, "bits": true, "usosvc": true, "dosvc": true,
	"stisvc": true, "shellhwdetection": true, "sysmain": true, "wscsvc": true,
}

func (a *Agent) platformHealthItems() []HealthItem {
	return []HealthItem{a.healthServices(), a.healthEvents(), healthDefender(), healthFirewall(), healthWindowsUpdate()}
}

func (a *Agent) healthServices() HealthItem {
	it := HealthItem{Key: "services", Label: "Servicos automaticos parados", Category: "sistema", Weight: 10, Status: healthUnknown}
	svcs := a.GetServices()
	if len(svcs) == 0 {
		return it
	}
	stopped := make([]string, 0)
	for _, s := range svcs {
		if s.StartType != "Automatic" || s.DelayedAutoStart || s.Status == "running" || healthIgnoredServices[strings.ToLower(s.Name)] {
			continue
		}
		stopped = append(stopped, s.DisplayName)
	}
	countThreshold(&it, len(stopped), 1, 4)
	it.Detail = limitList(stopped, 10)
	return it
}

func (a *Agent) healthEvents() HealthItem {
	it := HealthItem{Key: "events", Label: "Erros no log do sistema (24 h)", Category: "sistema", Weight: 10, Status: healthUnknown}
	events := a.GetEventLog("System", 1)
	errs := 0
	sources := map[string]bool{}
	for _, e := range events {
		if e.EventType == "ERROR" {
			errs++
			sources[e.Source] = true
		}
	}
	countThreshold(&it, errs, 5, 20)
	names := make([]string, 0, len(sources))
	for s := range sources {
		names = append(names, s)
	}
	it.Detail = limitList(names, 8)
	return it
}

func powershellJSON(script string, timeout time.Duration, target interface{}) error {
	out, err := runHealthCmd(timeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(strings.TrimSpace(out)), target)
}

func healthDefender() HealthItem {
	it := HealthItem{Key: "antivirus", Label: "Antivirus (Microsoft Defender)", Category: "seguranca", Weight: 15, Status: healthUnknown}
	var st struct {
		AntivirusEnabled          bool `json:"AntivirusEnabled"`
		RealTimeProtectionEnabled bool `json:"RealTimeProtectionEnabled"`
		AntivirusSignatureAge     int  `json:"AntivirusSignatureAge"`
	}
	if err := powershellJSON("Get-MpComputerStatus | Select-Object AntivirusEnabled,RealTimeProtectionEnabled,AntivirusSignatureAge | ConvertTo-Json -Compress", 60*time.Second, &st); err != nil {
		it.Detail = "Defender indisponivel (pode haver outro antivirus)"
		return it
	}
	switch {
	case !st.AntivirusEnabled || !st.RealTimeProtectionEnabled:
		it.Status = healthCritical
		it.Value = "Desativado"
	case st.AntivirusSignatureAge > 7:
		it.Status = healthWarning
		it.Value = fmt.Sprintf("Assinaturas com %d dias", st.AntivirusSignatureAge)
	default:
		it.Status = healthOK
		it.Value = "Ativo"
		it.Detail = fmt.Sprintf("Assinaturas com %d dias", st.AntivirusSignatureAge)
	}
	return it
}

func healthFirewall() HealthItem {
	it := HealthItem{Key: "firewall", Label: "Firewall do Windows", Category: "seguranca", Weight: 10, Status: healthUnknown}
	var profiles []struct {
		Name    string `json:"Name"`
		Enabled bool   `json:"Enabled"`
	}
	if err := powershellJSON("@(Get-NetFirewallProfile | Select-Object Name,Enabled) | ConvertTo-Json -Compress", 60*time.Second, &profiles); err != nil {
		return it
	}
	off := make([]string, 0)
	for _, p := range profiles {
		if !p.Enabled {
			off = append(off, p.Name)
		}
	}
	if len(off) == 0 {
		it.Status, it.Value = healthOK, "Ativo em todos os perfis"
	} else {
		it.Status, it.Value, it.Detail = healthCritical, "Desativado", "Perfis desativados: "+strings.Join(off, ", ")
	}
	return it
}

func healthWindowsUpdate() HealthItem {
	it := HealthItem{Key: "updates", Label: "Atualizacoes pendentes", Category: "seguranca", Weight: 10, Status: healthUnknown}
	var count int
	script := `(New-Object -ComObject Microsoft.Update.Session).CreateUpdateSearcher().Search("IsInstalled=0 and IsHidden=0 and Type='Software'").Updates.Count`
	if err := powershellJSON(script, 3*time.Minute, &count); err != nil {
		it.Detail = "Consulta ao Windows Update nao respondeu a tempo"
		return it
	}
	countThreshold(&it, count, 1, 10)
	return it
}
