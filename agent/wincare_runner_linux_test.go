//go:build linux
// +build linux

package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const wcTestRunID = "wc-00000000000000000000000000000001"

func wcEventTypes(evs []map[string]interface{}) string {
	var b strings.Builder
	for _, ev := range evs {
		b.WriteString(ev["type"].(string))
		if ev["type"] == "task" {
			b.WriteString(":" + ev["key"].(string) + ":" + ev["status"].(string))
		}
		b.WriteString(" ")
	}
	return b.String()
}

// Executa tarefas reais do maintenance unix pelo runner, com um publicador falso.
func TestWinCareRunnerEndToEndMaintenance(t *testing.T) {
	r := &wcRunner{goos: "linux", scripts: wincareScriptsFS}
	pub := &wcFakePublisher{}
	base := t.TempDir()
	payload := map[string]string{"run_id": wcTestRunID, "module": "maintenance", "tasks": "disk_space,dns_flush"}
	if err := r.Start(payload, pub, "agente1", base); err != nil {
		t.Fatal(err)
	}
	evs := pub.waitDone(t, 2*time.Minute)
	wcCheckSeq(t, evs)
	if pub.subjects[0] != "agente1.cmdoutput."+wcTestRunID {
		t.Fatalf("assunto = %s", pub.subjects[0])
	}

	types := wcEventTypes(evs)
	order := []string{"task:disk_space:running", "result", "task:disk_space:", "task:dns_flush:running", "task:dns_flush:", "done"}
	pos := 0
	for _, o := range order {
		i := strings.Index(types[pos:], o)
		if i < 0 {
			t.Fatalf("faltou %q na ordem; eventos: %s", o, types)
		}
		pos += i + len(o)
	}
	if !strings.Contains(types, "progress") {
		t.Fatalf("sem eventos de progresso: %s", types)
	}
	done := evs[len(evs)-1]
	if st := done["status"]; st != "ok" && st != "warning" {
		t.Fatalf("done status = %v", st)
	}
	if done["rebootRequired"] != false {
		t.Fatalf("rebootRequired = %v", done["rebootRequired"])
	}
	if _, ok := done["durationMs"].(float64); !ok {
		t.Fatalf("durationMs ausente: %v", done)
	}
	if left, _ := os.ReadDir(base); len(left) != 0 {
		t.Fatalf("diretório temporário não foi removido: %v", left)
	}
	if err := r.Start(payload, pub, "agente1", base); err != nil {
		t.Fatalf("o runner deveria aceitar nova execução após o done: %v", err)
	}
	pub.waitDone(t, 2*time.Minute)
}

func wcSlowScripts() fstest.MapFS {
	rt, _ := wincareScriptsFS.ReadFile("wincare/unix/_runtime.sh")
	return fstest.MapFS{
		"wincare/unix/_runtime.sh": {Data: rt},
		"wincare/unix/maintenance.sh": {Data: []byte(`. "$(dirname "$0")/_runtime.sh"
task_temp_files() { wc_log INFO "dormindo"; sleep 317; }
wc_main
`)},
	}
}

func TestWinCareRunnerCancelKillsTree(t *testing.T) {
	r := &wcRunner{goos: "linux", scripts: wcSlowScripts()}
	pub := &wcFakePublisher{}
	payload := map[string]string{"run_id": wcTestRunID, "module": "maintenance", "tasks": "temp_files"}
	if err := r.Start(payload, pub, "a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	other := map[string]string{"run_id": "wc-00000000000000000000000000000002", "module": "maintenance", "tasks": "temp_files"}
	if err := r.Start(other, pub, "a", ""); err == nil || err.Error() != "busy" {
		t.Fatalf("segunda execução deveria ser recusada com busy: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(wcEventTypes(pub.snapshot()), "task:temp_files:running") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := r.Cancel("wc-00000000000000000000000000000002"); err == nil {
		t.Fatal("cancelar outro run_id deveria falhar")
	}
	start := time.Now()
	if err := r.Cancel(wcTestRunID); err != nil {
		t.Fatal(err)
	}
	evs := pub.waitDone(t, 30*time.Second)
	if time.Since(start) > wcWaitDelay/2 {
		t.Fatal("cancelamento demorou: o sleep filho não foi encerrado")
	}
	if out, _ := exec.Command("pgrep", "-f", "^sleep 317$").Output(); len(out) > 0 {
		t.Fatalf("processo filho continua em execução: %s", out)
	}
	wcCheckSeq(t, evs)
	if st := evs[len(evs)-1]["status"]; st != "cancelled" {
		t.Fatalf("done status = %v", st)
	}
	if err := r.Cancel(wcTestRunID); err == nil || err.Error() != "not running" {
		t.Fatalf("cancelar execução encerrada: %v", err)
	}
}

func TestWinCareRunnerTimeout(t *testing.T) {
	r := &wcRunner{goos: "linux", scripts: wcSlowScripts(), timeout: time.Second}
	pub := &wcFakePublisher{}
	if err := r.Start(map[string]string{"run_id": wcTestRunID, "module": "maintenance", "tasks": "temp_files"}, pub, "a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	evs := pub.waitDone(t, 30*time.Second)
	if st := evs[len(evs)-1]["status"]; st != "timeout" {
		t.Fatalf("done status = %v", st)
	}
}

// Com WINCARE_E2E_ALL=1 roda todas as tarefas unix de maintenance e component_test e mostra
// os eventos (go test -v -run WinCareAllUnixTasks ./agent/). Altera o sistema: use como root
// em máquina de teste.
func TestWinCareAllUnixTasks(t *testing.T) {
	if os.Getenv("WINCARE_E2E_ALL") != "1" {
		t.Skip("defina WINCARE_E2E_ALL=1 para rodar todas as tarefas unix")
	}
	r := &wcRunner{goos: "linux", scripts: wincareScriptsFS}
	c, _ := wincareCatalog()
	for i, m := range filterWinCareCatalog(c, "linux").Modules {
		pub := &wcFakePublisher{}
		id := "wc-0000000000000000000000000000001" + string(rune('a'+i))
		if err := r.Start(map[string]string{"run_id": id, "module": m.Key, "tasks": wcTaskKeys(m.Tasks)}, pub, "agente", ""); err != nil {
			t.Fatal(err)
		}
		evs := pub.waitDone(t, 10*time.Minute)
		wcCheckSeq(t, evs)
		t.Logf("== %s (%s) ==", m.Key, wcTaskKeys(m.Tasks))
		for _, ev := range evs {
			b, _ := jsonCompact(ev)
			t.Log(b)
		}
	}
}

func jsonCompact(v interface{}) (string, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	err := enc.Encode(v)
	return strings.TrimSpace(b.String()), err
}
