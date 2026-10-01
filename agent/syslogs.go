package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

// Coletor de logs do sistema (fase 8): le journald, Event Log ou log unificado
// a partir da ultima posicao e envia em lotes para POST /api/v3/logs/.

const (
	sysLogCycle          = 60 * time.Second
	sysLogConfigInterval = 10 * time.Minute
	sysLogMaxMessage     = 8000
	sysLogMaxSource      = 200
	sysLogDefaultMax     = 500
	sysLogServerMax      = 1000
	sysLogReadCap        = 5000
	sysLogChunkBytes     = 1536 * 1024
	sysLogStateFile      = "syslogs-state.json"
	sysLogAgentSource    = "wincare-agent"
)

type sysLogConfig struct {
	Enabled     bool     `json:"enabled"`
	MinLevel    string   `json:"min_level"`
	WindowsLogs []string `json:"windows_logs"`
	MaxPerCycle int      `json:"max_per_cycle"`
}

type sysLogEntry struct {
	Time    string  `json:"time"`
	Level   string  `json:"level"`
	Source  string  `json:"source"`
	Log     string  `json:"log"`
	EventID *int64  `json:"event_id"`
	Message string  `json:"message"`
	Host    *string `json:"host"`
}

// sysLogPosition guarda onde a leitura parou em cada plataforma.
type sysLogPosition struct {
	Cursor  string           `json:"cursor,omitempty"`
	Since   string           `json:"since,omitempty"`
	Records map[string]int64 `json:"records,omitempty"`
}

func (p sysLogPosition) isZero() bool {
	return p.Cursor == "" && p.Since == "" && len(p.Records) == 0
}

type sysLogCollector struct {
	a          *Agent
	cfg        sysLogConfig
	cfgAt      time.Time
	pending    []sysLogEntry
	pendingPos *sysLogPosition
	statePath  string
}

// RunSysLogs roda o coletor de logs para sempre; desligado ate o servidor habilitar.
func (a *Agent) RunSysLogs() {
	c := &sysLogCollector{a: a, statePath: filepath.Join(wincareStateDir(), sysLogStateFile)}
	time.Sleep(time.Duration(randRange(20, 60)) * time.Second)
	for {
		c.cycle(time.Now())
		time.Sleep(sysLogCycle)
	}
}

func (c *sysLogCollector) cycle(now time.Time) {
	if c.cfgAt.IsZero() || now.Sub(c.cfgAt) >= sysLogConfigInterval {
		c.refreshConfig()
		c.cfgAt = now
	}
	if !c.cfg.Enabled {
		c.pending, c.pendingPos = nil, nil
		if _, err := os.Stat(c.statePath); err == nil {
			_ = os.Remove(c.statePath)
		}
		return
	}

	if c.pendingPos == nil {
		pos := c.loadState()
		entries, next, err := collectPlatformLogs(c.cfg, pos, c.a.Hostname, sysLogReadCap)
		if err != nil {
			c.a.Logger.Debugln("syslogs collect:", err)
		}
		if len(entries) == 0 {
			if !reflect.DeepEqual(pos, next) {
				c.saveState(next)
			}
			return
		}
		c.pending = limitSysLogs(entries, c.cfg.MaxPerCycle, now, sysLogPlatformLog)
		c.pendingPos = &next
	}

	if c.sendPending() {
		c.saveState(*c.pendingPos)
		c.pending, c.pendingPos = nil, nil
	}
}

func (c *sysLogCollector) refreshConfig() {
	url := fmt.Sprintf("/api/v3/%s/logconfig/", c.a.AgentID)
	r, err := c.a.rClient.R().SetResult(&sysLogConfig{}).Get(url)
	if err != nil {
		c.a.Logger.Debugln("syslogs config:", err)
		return
	}
	if r.StatusCode() == http.StatusNotFound {
		c.cfg = sysLogConfig{}
		return
	}
	if r.IsError() {
		c.a.Logger.Debugln("syslogs config: status", r.StatusCode())
		return
	}
	cfg, ok := r.Result().(*sysLogConfig)
	if !ok || cfg == nil {
		return
	}
	c.cfg = normalizeSysLogConfig(*cfg)
}

func normalizeSysLogConfig(cfg sysLogConfig) sysLogConfig {
	switch cfg.MinLevel {
	case "critical", "error", "warning", "info":
	default:
		cfg.MinLevel = "error"
	}
	if cfg.MaxPerCycle <= 0 {
		cfg.MaxPerCycle = sysLogDefaultMax
	}
	if cfg.MaxPerCycle > sysLogServerMax-1 {
		cfg.MaxPerCycle = sysLogServerMax - 1
	}
	logs := []string{"System", "Application"}
	seen := map[string]bool{"system": true, "application": true}
	for _, l := range cfg.WindowsLogs {
		l = strings.TrimSpace(l)
		if !validWindowsLogName(l) || seen[strings.ToLower(l)] {
			continue
		}
		seen[strings.ToLower(l)] = true
		logs = append(logs, l)
	}
	cfg.WindowsLogs = logs
	return cfg
}

// validWindowsLogName aceita nomes como "Microsoft-Windows-PowerShell/Operational".
func validWindowsLogName(name string) bool {
	if name == "" || len(name) > 200 {
		return false
	}
	for _, r := range name {
		ok := r == ' ' || r == '-' || r == '_' || r == '.' || r == '/' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

func (c *sysLogCollector) loadState() sysLogPosition {
	var pos sysLogPosition
	data, err := os.ReadFile(c.statePath)
	if err != nil {
		return pos
	}
	if err := json.Unmarshal(data, &pos); err != nil {
		c.a.Logger.Debugln("syslogs state:", err)
		return sysLogPosition{}
	}
	return pos
}

func (c *sysLogCollector) saveState(pos sysLogPosition) {
	data, err := json.Marshal(pos)
	if err != nil {
		c.a.Logger.Errorln("syslogs state:", err)
		return
	}
	if err := writeFileAtomic(c.statePath, data); err != nil {
		c.a.Logger.Errorln("syslogs state:", err)
	}
}

// writeFileAtomic grava em arquivo temporario no mesmo diretorio e renomeia.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := ensureStateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// sendPending envia o lote pendente em partes de ate 1000 entradas e ~1,5 MB.
// Partes enviadas saem do lote; devolve true quando nao sobrou nada.
func (c *sysLogCollector) sendPending() bool {
	for len(c.pending) > 0 {
		n := sysLogChunkLen(c.pending)
		r, err := c.a.rClient.R().SetBody(map[string]interface{}{"entries": c.pending[:n]}).Post("/api/v3/logs/")
		if err != nil {
			c.a.Logger.Debugln("syslogs send:", err)
			return false
		}
		if r.IsError() {
			code := r.StatusCode()
			if code >= 400 && code < 500 && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests {
				// Recusa definitiva: descarta a parte para nao travar a coleta.
				c.a.Logger.Errorf("syslogs send: servidor recusou %d entradas (status %d)", n, code)
			} else {
				c.a.Logger.Debugln("syslogs send: status", code)
				return false
			}
		}
		c.pending = c.pending[n:]
	}
	return true
}

func sysLogChunkLen(entries []sysLogEntry) int {
	size := 0
	for i, e := range entries {
		if i >= sysLogServerMax {
			return i
		}
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		size += len(b) + 1
		if size > sysLogChunkBytes && i > 0 {
			return i
		}
	}
	return len(entries)
}

// limitSysLogs normaliza as entradas e, acima do limite, fica com as mais recentes
// e acrescenta uma entrada de aviso com a quantidade descartada.
func limitSysLogs(entries []sysLogEntry, max int, now time.Time, logName string) []sysLogEntry {
	if max <= 0 {
		max = sysLogDefaultMax
	}
	if max > sysLogServerMax-1 {
		max = sysLogServerMax - 1
	}
	dropped := 0
	if len(entries) > max {
		dropped = len(entries) - max
		entries = entries[dropped:]
	}
	out := make([]sysLogEntry, 0, len(entries)+1)
	for _, e := range entries {
		e.Message = truncateRunes(e.Message, sysLogMaxMessage)
		e.Source = truncateRunes(e.Source, sysLogMaxSource)
		if e.Source == "" {
			e.Source = logName
		}
		out = append(out, e)
	}
	if dropped > 0 {
		out = append(out, sysLogEntry{
			Time:    now.UTC().Format(time.RFC3339),
			Level:   "warning",
			Source:  sysLogAgentSource,
			Log:     logName,
			Message: fmt.Sprintf("%d eventos descartados pelo limite", dropped),
		})
	}
	return out
}

func truncateRunes(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}

var errSysLogUnsupported = errors.New("coleta de logs nao suportada nesta plataforma")
