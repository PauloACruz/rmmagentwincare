package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseJournalLineText(t *testing.T) {
	line := `{"__CURSOR":"s=abc;i=1","__REALTIME_TIMESTAMP":"1790000000123456","PRIORITY":"3","SYSLOG_IDENTIFIER":"sshd","_SYSTEMD_UNIT":"ssh.service","MESSAGE":"falha de login","_HOSTNAME":"srv01"}`
	e, cursor, ok := parseJournalLine([]byte(line))
	if !ok || cursor != "s=abc;i=1" {
		t.Fatalf("ok=%v cursor=%q", ok, cursor)
	}
	if e.Level != "error" || e.Source != "sshd" || e.Message != "falha de login" || e.Log != "journal" {
		t.Fatalf("entrada inesperada: %+v", e)
	}
	if e.Host == nil || *e.Host != "srv01" {
		t.Fatalf("host = %v", e.Host)
	}
	want := time.Unix(1790000000, 123456000).UTC().Format(time.RFC3339Nano)
	if e.Time != want {
		t.Fatalf("time = %s, esperado %s", e.Time, want)
	}
}

func TestParseJournalLineMessageBytesAndUnitFallback(t *testing.T) {
	// "ola\xff" chega como lista de bytes porque nao e UTF-8 valido
	line := `{"__CURSOR":"c2","__REALTIME_TIMESTAMP":"1","PRIORITY":"1","_SYSTEMD_UNIT":"kernel.service","MESSAGE":[111,108,97,255]}`
	e, _, ok := parseJournalLine([]byte(line))
	if !ok {
		t.Fatal("linha nao reconhecida")
	}
	if e.Level != "critical" || e.Source != "kernel.service" || e.Host != nil {
		t.Fatalf("entrada inesperada: %+v", e)
	}
	if e.Message != "ola\xff" {
		t.Fatalf("message = %q", e.Message)
	}
	limited := limitSysLogs([]sysLogEntry{e}, 10, time.Now(), "journal")
	if limited[0].Message != "ola�" {
		t.Fatalf("UTF-8 invalido nao foi corrigido: %q", limited[0].Message)
	}

	multi := `{"__CURSOR":"c3","MESSAGE":["a","b"]}`
	e, _, _ = parseJournalLine([]byte(multi))
	if e.Message != "a\nb" || e.Level != "info" {
		t.Fatalf("campo repetido: %+v", e)
	}
	if _, _, ok := parseJournalLine([]byte(`{"MESSAGE":"sem cursor"}`)); ok {
		t.Fatal("linha sem cursor deveria ser ignorada")
	}
	if _, _, ok := parseJournalLine([]byte(`nao e json`)); ok {
		t.Fatal("linha invalida deveria ser ignorada")
	}
}

func TestLevelMappings(t *testing.T) {
	journal := map[int]string{0: "critical", 2: "critical", 3: "error", 4: "warning", 5: "info", 6: "info", 7: "info"}
	for p, want := range journal {
		if got := journalPriorityLevel(p); got != want {
			t.Fatalf("PRIORITY %d = %s, esperado %s", p, got, want)
		}
	}
	win := map[int]string{0: "info", 1: "critical", 2: "error", 3: "warning", 4: "info"}
	for l, want := range win {
		if got := windowsEventLevel(l); got != want {
			t.Fatalf("Level %d = %s, esperado %s", l, got, want)
		}
	}
	mac := map[string]string{"Fault": "critical", "Error": "error", "Default": "info"}
	for k, want := range mac {
		if got := macMessageTypeLevel(k); got != want {
			t.Fatalf("messageType %s = %s, esperado %s", k, got, want)
		}
	}
	ranges := map[string]string{"critical": "0..2", "error": "0..3", "warning": "0..4", "info": "0..6", "": "0..3"}
	for k, want := range ranges {
		if got := journalPriorityRange(k); got != want {
			t.Fatalf("min_level %q = %s, esperado %s", k, got, want)
		}
	}
	if !strings.Contains(macLogPredicate("info"), "default") || strings.Contains(macLogPredicate("error"), "default") {
		t.Fatal("predicate do macOS incorreto")
	}
}

func TestLimitSysLogsTruncatesAndAddsDropEntry(t *testing.T) {
	long := strings.Repeat("é", sysLogMaxMessage+50)
	var in []sysLogEntry
	for i := 0; i < 7; i++ {
		in = append(in, sysLogEntry{Level: "error", Source: strings.Repeat("s", 300), Message: long})
	}
	out := limitSysLogs(in, 5, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), "journal")
	if len(out) != 6 {
		t.Fatalf("len = %d, esperado 6", len(out))
	}
	for _, e := range out[:5] {
		if n := len([]rune(e.Message)); n != sysLogMaxMessage {
			t.Fatalf("mensagem com %d caracteres", n)
		}
		if n := len(e.Source); n != sysLogMaxSource {
			t.Fatalf("source com %d caracteres", n)
		}
	}
	drop := out[5]
	if drop.Level != "warning" || drop.Source != "wincare-agent" || drop.Message != "2 eventos descartados pelo limite" || drop.Time != "2026-10-01T12:00:00Z" {
		t.Fatalf("entrada de descarte: %+v", drop)
	}
	if got := limitSysLogs(in[:3], 5, time.Now(), "journal"); len(got) != 3 {
		t.Fatalf("abaixo do limite nao deveria descartar: %d", len(got))
	}
	if got := limitSysLogs(make([]sysLogEntry, 1500), 5000, time.Now(), "x"); len(got) != sysLogServerMax {
		t.Fatalf("lote acima do maximo do servidor: %d", len(got))
	}
}

func TestParseWindowsEventsAdvancesPosition(t *testing.T) {
	out := []byte("\xef\xbb\xbf" + `[{"name":"System","ok":true,"last":120,"events":[{"id":101,"t":"2026-10-01T10:00:00.0000000Z","lv":2,"src":"Disk","eid":7,"msg":"erro\r\nde disco","host":"PC1"},{"id":110,"t":"2026-10-01T10:01:00Z","lv":1,"src":"Kernel-Power","eid":41,"msg":"","host":"PC1"}]},
	{"name":"Application","ok":true,"last":50,"events":[]},
	{"name":"Setup","ok":false,"error":"acesso negado","last":0,"events":[]}]`)
	prev := map[string]int64{"System": 100, "Setup": 9}
	entries, next, err := parseWindowsEvents(out, prev, 1000)
	if err == nil || !strings.Contains(err.Error(), "Setup") {
		t.Fatalf("erro do log Setup esperado, veio %v", err)
	}
	if len(entries) != 2 || entries[0].Level != "error" || entries[1].Level != "critical" {
		t.Fatalf("entradas: %+v", entries)
	}
	if entries[0].Message != "erro\nde disco" || *entries[0].EventID != 7 || entries[0].Log != "System" || entries[0].Time != "2026-10-01T10:00:00Z" {
		t.Fatalf("entrada: %+v", entries[0])
	}
	if next["System"] != 120 || next["Application"] != 50 || next["Setup"] != 9 {
		t.Fatalf("posicoes: %+v", next)
	}
	if script := windowsEventScript([]string{"System", "Application"}, next, "warning", 500); !strings.Contains(script, "Read-WcLog 'System' 120 500 'Level=1 or Level=2 or Level=3'") || !strings.Contains(script, "Read-WcLog 'Application' 50") {
		t.Fatalf("script inesperado:\n%s", script)
	}
}

func TestParseMacLogLine(t *testing.T) {
	line := `{"timestamp":"2026-10-01 09:15:30.123456-0300","messageType":"Fault","eventMessage":"falhou","subsystem":"","processImagePath":"/usr/libexec/foo"}`
	e, ts, ok := parseMacLogLine([]byte(line), "mac1")
	if !ok || e.Level != "critical" || e.Source != "foo" || e.Log != "unified" || *e.Host != "mac1" {
		t.Fatalf("entrada: %+v ok=%v", e, ok)
	}
	if e.Time != "2026-10-01T12:15:30.123456Z" || ts.IsZero() {
		t.Fatalf("time = %s", e.Time)
	}
}

func TestNormalizeSysLogConfig(t *testing.T) {
	cfg := normalizeSysLogConfig(sysLogConfig{Enabled: true, MinLevel: "x", WindowsLogs: []string{"system", "Security", "bad'name", "Microsoft-Windows-PowerShell/Operational"}, MaxPerCycle: 5000})
	if cfg.MinLevel != "error" || cfg.MaxPerCycle != sysLogServerMax-1 {
		t.Fatalf("cfg: %+v", cfg)
	}
	want := "System,Application,Security,Microsoft-Windows-PowerShell/Operational"
	if got := strings.Join(cfg.WindowsLogs, ","); got != want {
		t.Fatalf("logs = %s", got)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	p := filepath.Join(dir, "x.json")
	if err := writeFileAtomic(p, []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("2")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "2" {
		t.Fatalf("conteudo %q err %v", b, err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatalf("sobraram temporarios: %d arquivos", len(files))
	}
}
