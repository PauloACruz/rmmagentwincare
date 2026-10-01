package agent

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ugorji/go/codec"
)

// wcFakePublisher guarda os eventos publicados, decodificando o msgpack como o servidor faz.
type wcFakePublisher struct {
	mu       sync.Mutex
	subjects []string
	events   []map[string]interface{}
}

func (p *wcFakePublisher) Publish(subject string, data []byte) error {
	var s string
	var mh codec.MsgpackHandle
	mh.RawToString = true
	if err := codec.NewDecoderBytes(data, &mh).Decode(&s); err != nil {
		return err
	}
	var ev map[string]interface{}
	if err := json.Unmarshal([]byte(s), &ev); err != nil {
		return fmt.Errorf("evento não é JSON: %q", s)
	}
	p.mu.Lock()
	p.subjects = append(p.subjects, subject)
	p.events = append(p.events, ev)
	p.mu.Unlock()
	return nil
}

func (p *wcFakePublisher) snapshot() []map[string]interface{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]interface{}, len(p.events))
	copy(out, p.events)
	return out
}

func (p *wcFakePublisher) waitDone(t *testing.T, timeout time.Duration) []map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		evs := p.snapshot()
		if n := len(evs); n > 0 && evs[n-1]["type"] == "done" {
			return evs
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("evento done não chegou em %s; eventos: %v", timeout, p.snapshot())
	return nil
}

func wcCheckSeq(t *testing.T, evs []map[string]interface{}) {
	t.Helper()
	for i, ev := range evs {
		if seq, _ := ev["seq"].(float64); int(seq) != i+1 {
			t.Fatalf("evento %d com seq %v, esperado %d", i, ev["seq"], i+1)
		}
		ts, _ := ev["time"].(string)
		if _, err := time.Parse(time.RFC3339, ts); err != nil {
			t.Fatalf("evento %d com time inválido %q", i, ts)
		}
	}
	if evs[len(evs)-1]["type"] != "done" {
		t.Fatalf("último evento não é done: %v", evs[len(evs)-1])
	}
}

func TestWinCareParseLine(t *testing.T) {
	ev, ok := parseWinCareLine(`##WC {"type":"log","level":"warn","message":"oi é","seq":99,"time":"x"}`)
	if !ok || ev["level"] != "WARN" || ev["message"] != "oi é" {
		t.Fatalf("log inválido: %v %v", ok, ev)
	}
	if _, has := ev["seq"]; has {
		t.Fatal("seq do script não pode passar adiante")
	}
	ev, ok = parseWinCareLine(`##WC {"type":"log","level":"DEBUG","message":"x"}`)
	if !ok || ev["level"] != "INFO" {
		t.Fatalf("nível desconhecido deveria virar INFO: %v", ev)
	}
	ev, ok = parseWinCareLine(`##WC {"type":"progress","value":150,"message":"m"}`)
	if !ok || ev["value"] != 100 || ev["message"] != "m" {
		t.Fatalf("progress: %v", ev)
	}
	ev, ok = parseWinCareLine(`##WC {"type":"task","key":"temp_files","status":"ok"}`)
	if !ok || ev["key"] != "temp_files" || ev["status"] != "ok" {
		t.Fatalf("task: %v", ev)
	}
	ev, ok = parseWinCareLine(`##WC {"type":"result","data":{"task":"list","apps":[{"name":"7-Zip"}]}}`)
	if !ok || ev["data"].(map[string]interface{})["task"] != "list" {
		t.Fatalf("result: %v", ev)
	}
	ev, _ = parseWinCareLine(`##WC {"type":"log","level":"INFO","message":"` + strings.Repeat("é", 4000) + `"}`)
	if m := ev["message"].(string); len(m) > wcMaxLine || !strings.HasPrefix(m, "é") {
		t.Fatalf("mensagem não foi limitada a 4 KB: %d bytes", len(m))
	}
	for _, bad := range []string{
		`texto comum`,
		`##WC nao e json`,
		`##WC {"type":"done","status":"ok"}`,
		`##WC {"type":"task","key":"x","status":"finished"}`,
		`##WC {"type":"task","key":"../x","status":"ok"}`,
		`##WC {"type":"progress","value":"10"}`,
	} {
		if _, ok := parseWinCareLine(bad); ok {
			t.Fatalf("linha deveria ser recusada: %s", bad)
		}
	}
}

func TestWinCareCatalogFilter(t *testing.T) {
	c, err := wincareCatalog()
	if err != nil {
		t.Fatal(err)
	}
	keyRe := regexp.MustCompile(`^[a-z0-9_]+$`)
	modules := map[string]bool{}
	for _, m := range c.Modules {
		modules[m.Key] = true
		seen := map[string]bool{}
		for _, task := range m.Tasks {
			if !keyRe.MatchString(task.Key) || seen[task.Key] || task.Label == "" || task.Group == "" || len(task.Platforms) == 0 {
				t.Fatalf("tarefa inválida %s.%s", m.Key, task.Key)
			}
			seen[task.Key] = true
			for _, p := range task.Platforms {
				if !wcHasPlatform(m.Platforms, p) {
					t.Fatalf("%s.%s tem plataforma %s fora do módulo", m.Key, task.Key, p)
				}
			}
		}
	}
	for _, k := range []string{"maintenance", "windows_update", "bug_fixer", "office", "registry", "app_remover", "component_test", "winget"} {
		if !modules[k] {
			t.Fatalf("módulo %s ausente", k)
		}
	}

	s, err := wincareCatalogJSON("linux")
	if err != nil {
		t.Fatal(err)
	}
	var linux wcCatalog
	if err := json.Unmarshal([]byte(s), &linux); err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, m := range linux.Modules {
		for _, task := range m.Tasks {
			got[m.Key] = append(got[m.Key], task.Key)
		}
	}
	want := map[string][]string{
		"maintenance":    {"dns_flush", "temp_files", "package_cache", "journal_vacuum", "disk_space"},
		"component_test": {"cpu_bench", "ram_info", "disk_speed", "net_ping"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("catálogo linux = %v, esperado %v", got, want)
	}

	darwin := filterWinCareCatalog(c, "darwin")
	for _, m := range darwin.Modules {
		for _, task := range m.Tasks {
			if task.Key == "journal_vacuum" {
				t.Fatal("journal_vacuum não existe no macOS")
			}
		}
	}
	if n := len(filterWinCareCatalog(c, "windows").Modules); n != 8 {
		t.Fatalf("windows deveria ter 8 módulos, tem %d", n)
	}
	if len(filterWinCareCatalog(c, "freebsd").Modules) != 0 {
		t.Fatal("sistema sem suporte deveria ter catálogo vazio")
	}
}

// Cada tarefa do catálogo precisa existir no script da plataforma correspondente.
func TestWinCareCatalogMatchesScripts(t *testing.T) {
	c, _ := wincareCatalog()
	for _, m := range c.Modules {
		for _, task := range m.Tasks {
			for _, p := range task.Platforms {
				name, marker := "wincare/unix/"+m.Key+".sh", "task_"+task.Key+"() {"
				if p == "windows" {
					name, marker = "wincare/windows/"+m.Key+".ps1", "'"+task.Key+"' = {"
				}
				data, err := fs.ReadFile(wincareScriptsFS, name)
				if err != nil {
					t.Fatalf("%s.%s: script %s ausente", m.Key, task.Key, name)
				}
				if !strings.Contains(string(data), marker) {
					t.Fatalf("%s.%s não implementada em %s", m.Key, task.Key, name)
				}
			}
		}
	}
}

func TestWinCareRunValidation(t *testing.T) {
	c, _ := wincareCatalog()
	id := "wc-0123456789abcdef0123456789abcdef"
	cases := []struct {
		goos    string
		payload map[string]string
		err     string
	}{
		{"linux", map[string]string{"run_id": "wc-123", "module": "maintenance", "tasks": "temp_files"}, "invalid run_id"},
		{"linux", map[string]string{"run_id": "wc-0123456789ABCDEF0123456789ABCDEF", "module": "maintenance", "tasks": "temp_files"}, "invalid run_id"},
		{"linux", map[string]string{"run_id": id, "module": "nope", "tasks": "x"}, "unknown module"},
		{"linux", map[string]string{"run_id": id, "module": "windows_update", "tasks": "install_updates"}, "not supported"},
		{"linux", map[string]string{"run_id": id, "module": "maintenance", "tasks": "temp_files,nope"}, "unknown task"},
		{"linux", map[string]string{"run_id": id, "module": "maintenance", "tasks": "sfc"}, "not supported"},
		{"linux", map[string]string{"run_id": id, "module": "maintenance", "tasks": " , "}, "no tasks"},
		{"windows", map[string]string{"run_id": id, "module": "app_remover", "tasks": "uninstall"}, "missing param: apps"},
		{"windows", map[string]string{"run_id": id, "module": "app_remover", "tasks": "uninstall", "params": `{"apps":"  "}`}, "missing param: apps"},
		{"windows", map[string]string{"run_id": id, "module": "winget", "tasks": "search", "params": `[1]`}, "invalid params"},
		{"windows", map[string]string{"run_id": id, "module": "winget", "tasks": "install", "params": `{"ids":"Git.Git","silent":"talvez"}`}, "invalid param silent"},
	}
	for _, tc := range cases {
		_, err := buildWinCarePlan(tc.payload, c, tc.goos)
		if err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Fatalf("%v: erro %v, esperado %q", tc.payload, err, tc.err)
		}
	}

	plan, err := buildWinCarePlan(map[string]string{
		"run_id": id, "module": "app_remover", "tasks": "list, uninstall,list",
		"params": `{"apps":"7-Zip, VLC","timeout_minutes":"5","extra":"ignorado"}`,
	}, c, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if wcTaskKeys(plan.Tasks) != "list,uninstall" {
		t.Fatalf("tarefas = %s", wcTaskKeys(plan.Tasks))
	}
	if plan.Params["apps"] != "7-Zip, VLC" || plan.Params["timeout_minutes"] != 5.0 || plan.Params["clean_folders"] != true {
		t.Fatalf("params = %v", plan.Params)
	}
	if _, has := plan.Params["extra"]; has {
		t.Fatal("parâmetro não declarado deveria ser descartado")
	}
	if plan.Timeout != wcDefaultTimeout {
		t.Fatalf("timeout = %s", plan.Timeout)
	}
	plan, err = buildWinCarePlan(map[string]string{"run_id": id, "module": "windows_update", "tasks": "install_updates"}, c, "windows")
	if err != nil || plan.Timeout != wcUpdateTimeout || plan.Params["recursive"] != true {
		t.Fatalf("windows_update: %v %v", err, plan)
	}
}

func TestWinCareEmitterLimitsEvents(t *testing.T) {
	pub := &wcFakePublisher{}
	em := newWinCareEmitter(pub, "agent.cmdoutput.wc-x", []wcTask{{Key: "temp_files"}})
	em.handleLine(`##WC {"type":"task","key":"temp_files","status":"running"}`, false)
	for i := 0; i < 12000; i++ {
		em.handleLine(fmt.Sprintf("linha %d", i), i%7 == 0)
		if i%500 == 0 {
			em.handleLine(fmt.Sprintf(`##WC {"type":"progress","value":%d}`, i/120), false)
		}
	}
	em.handleLine(`##WC {"type":"task","key":"temp_files","status":"ok"}`, false)
	em.handleLine(`##WC {"type":"task","key":"outra","status":"ok"}`, false)
	em.done("ok", time.Second, false)

	evs := pub.snapshot()
	if len(evs) > wcMaxEvents {
		t.Fatalf("%d eventos, limite %d", len(evs), wcMaxEvents)
	}
	wcCheckSeq(t, evs)
	merged := false
	for _, ev := range evs {
		if m, _ := ev["message"].(string); len(m) > wcMaxLine {
			t.Fatalf("mensagem com %d bytes", len(m))
		} else if strings.Contains(m, "\n") {
			merged = true
		}
	}
	if !merged {
		t.Fatal("logs deveriam ter sido agrupados perto do limite")
	}
	if em.statusOf("temp_files") != "ok" || em.statusOf("outra") != "" {
		t.Fatal("status das tarefas incorreto")
	}
	if pub.subjects[0] != "agent.cmdoutput.wc-x" {
		t.Fatalf("assunto = %s", pub.subjects[0])
	}
}

func TestWinCareLineWriterLimitsLength(t *testing.T) {
	var lines []string
	w := &wcLineWriter{fn: func(l string) { lines = append(lines, l) }}
	_, _ = w.Write([]byte("a\r\nb"))
	_, _ = w.Write([]byte("c\n" + strings.Repeat("x", wcMaxRawLine+10) + "\nfim"))
	w.Flush()
	if len(lines) != 4 || lines[0] != "a\r" || lines[1] != "bc" || len(lines[2]) != wcMaxRawLine || lines[3] != "fim" {
		t.Fatalf("linhas = %d %q", len(lines), lines[:2])
	}

	pub := &wcFakePublisher{}
	em := newWinCareEmitter(pub, "s", nil)
	em.handleLine(lines[2], false)
	em.handleLine("##WC "+strings.Repeat("y", 6000), true)
	for _, ev := range pub.snapshot() {
		if len(ev["message"].(string)) > wcMaxLine {
			t.Fatal("linha comum deveria ser limitada a 4 KB")
		}
	}
	if evs := pub.snapshot(); evs[1]["level"] != "WARN" {
		t.Fatalf("stderr deveria virar WARN: %v", evs[1])
	}
}
