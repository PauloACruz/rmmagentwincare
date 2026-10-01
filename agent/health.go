package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

// Health Check do WinCare reescrito em Go: nota de 0 a 100 a partir de itens com peso.
// Itens que nao se aplicam ao sistema (status unknown) ficam fora da conta.

const (
	healthOK       = "ok"
	healthWarning  = "warning"
	healthCritical = "critical"
	healthUnknown  = "unknown"
)

type HealthItem struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Category string  `json:"category"`
	Status   string  `json:"status"`
	Value    string  `json:"value"`
	Detail   string  `json:"detail"`
	Weight   int     `json:"weight"`
	Points   float64 `json:"points"`
}

type HealthReport struct {
	Score       int          `json:"score"`
	Grade       string       `json:"grade"`
	CollectedAt string       `json:"collectedAt"`
	Platform    string       `json:"platform"`
	Items       []HealthItem `json:"items"`
}

// HandleHealthCheck atende o comando wincare_health.
func (a *Agent) HandleHealthCheck(msg *nats.Msg) {
	report := a.CollectHealth()
	data, err := json.Marshal(report)
	if err != nil {
		respondString(msg, "error: "+err.Error())
		return
	}
	respondString(msg, string(data))
}

func (a *Agent) CollectHealth() HealthReport {
	items := []HealthItem{healthCPU(), healthMemory(), a.healthDisks(), a.healthUptime()}
	items = append(items, a.platformHealthItems()...)
	return scoreHealth(items, time.Now())
}

func scoreHealth(items []HealthItem, now time.Time) HealthReport {
	total, got := 0, 0.0
	for i := range items {
		it := &items[i]
		switch it.Status {
		case healthOK:
			it.Points = float64(it.Weight)
		case healthWarning:
			it.Points = float64(it.Weight) / 2
		default:
			it.Points = 0
		}
		if it.Status != healthUnknown {
			total += it.Weight
			got += it.Points
		}
	}
	score := 0
	if total > 0 {
		score = int(got/float64(total)*100 + 0.5)
	}
	grade := "critico"
	switch {
	case score >= 90:
		grade = "otimo"
	case score >= 75:
		grade = "bom"
	case score >= 50:
		grade = "atencao"
	}
	return HealthReport{Score: score, Grade: grade, CollectedAt: now.UTC().Format(time.RFC3339), Platform: runtime.GOOS, Items: items}
}

// level devolve ok, warning ou critical comparando um valor em que maior e pior.
func level(value, warnAt, critAt float64) string {
	switch {
	case value >= critAt:
		return healthCritical
	case value >= warnAt:
		return healthWarning
	default:
		return healthOK
	}
}

func healthCPU() HealthItem {
	it := HealthItem{Key: "cpu", Label: "Uso de CPU", Category: "desempenho", Weight: 10, Status: healthUnknown}
	pct, err := cpu.Percent(3*time.Second, false)
	if err != nil || len(pct) == 0 {
		it.Detail = "Nao foi possivel medir a CPU"
		return it
	}
	it.Value = fmt.Sprintf("%.0f%%", pct[0])
	it.Status = level(pct[0], 80, 95)
	return it
}

func healthMemory() HealthItem {
	it := HealthItem{Key: "memory", Label: "Uso de memoria", Category: "desempenho", Weight: 10, Status: healthUnknown}
	vm, err := mem.VirtualMemory()
	if err != nil {
		it.Detail = "Nao foi possivel medir a memoria"
		return it
	}
	it.Value = fmt.Sprintf("%.0f%%", vm.UsedPercent)
	it.Detail = fmt.Sprintf("%.1f GB livres de %.1f GB", float64(vm.Available)/1e9, float64(vm.Total)/1e9)
	it.Status = level(vm.UsedPercent, 85, 95)
	return it
}

// Sistemas de arquivos de imagem ou midia, sempre cheios por natureza.
var healthIgnoredFs = map[string]bool{"squashfs": true, "iso9660": true, "erofs": true, "udf": true, "cdfs": true, "overlay": true, "tmpfs": true, "devtmpfs": true}

func (a *Agent) healthDisks() HealthItem {
	it := HealthItem{Key: "disks", Label: "Espaco em disco", Category: "armazenamento", Weight: 15, Status: healthUnknown}
	parts, err := disk.Partitions(false)
	if err != nil {
		return it
	}
	worst, details, minFree, seen := healthOK, make([]string, 0, len(parts)), 100.0, map[string]bool{}
	for _, p := range parts {
		if healthIgnoredFs[strings.ToLower(p.Fstype)] || seen[p.Device] || hasOpt(p.Opts, "ro") {
			continue
		}
		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u.Total < 1<<30 {
			continue
		}
		seen[p.Device] = true
		free := 100 - u.UsedPercent
		if free < minFree {
			minFree = free
		}
		if st := level(u.UsedPercent, 85, 95); rank(st) > rank(worst) {
			worst = st
		}
		details = append(details, fmt.Sprintf("%s: %.0f%% livre (%.1f GB de %.1f GB)", p.Mountpoint, free, float64(u.Free)/1e9, float64(u.Total)/1e9))
	}
	if len(details) == 0 {
		return it
	}
	it.Status = worst
	it.Value = fmt.Sprintf("%.0f%% livre no volume mais cheio", minFree)
	it.Detail = strings.Join(details, "; ")
	return it
}

func hasOpt(opts []string, want string) bool {
	for _, o := range opts {
		if o == want {
			return true
		}
	}
	return false
}

func (a *Agent) healthUptime() HealthItem {
	it := HealthItem{Key: "uptime", Label: "Tempo ligado e reinicio pendente", Category: "sistema", Weight: 5, Status: healthOK}
	up, err := host.Uptime()
	if err == nil {
		days := float64(up) / 86400
		it.Value = fmt.Sprintf("%.1f dias", days)
		if days > 30 {
			it.Status = healthWarning
			it.Detail = "Ligado ha mais de 30 dias"
		}
	}
	if pending, err := a.SystemRebootRequired(); err == nil && pending {
		it.Status = healthWarning
		it.Detail = strings.TrimPrefix(it.Detail+"; Reinicio pendente", "; ")
	}
	return it
}

func rank(status string) int {
	switch status {
	case healthCritical:
		return 2
	case healthWarning:
		return 1
	default:
		return 0
	}
}

// runHealthCmd executa um comando com tempo limite e devolve a saida combinada.
func runHealthCmd(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if ctx.Err() != nil {
		return string(out), ctx.Err()
	}
	return string(out), err
}

func countThreshold(it *HealthItem, n, warnAt, critAt int) {
	it.Value = fmt.Sprintf("%d", n)
	it.Status = level(float64(n), float64(warnAt), float64(critAt))
}

func limitList(values []string, max int) string {
	sort.Strings(values)
	if len(values) > max {
		return strings.Join(values[:max], ", ") + fmt.Sprintf(" e mais %d", len(values)-max)
	}
	return strings.Join(values, ", ")
}
