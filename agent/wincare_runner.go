package agent

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	nats "github.com/nats-io/nats.go"
	"github.com/ugorji/go/codec"
)

//go:embed wincare/catalog.json
var wincareCatalogRaw []byte

//go:embed wincare/windows/*.ps1 wincare/unix/*.sh
var wincareScriptsFS embed.FS

const (
	wcMaxEvents      = 5000
	wcReservedEvents = 100 // vagas guardadas para task, result, progress e done quando os logs passam a ser agrupados
	wcMaxLine        = 4 * 1024
	wcMaxRawLine     = 512 * 1024 // linhas ##WC com result podem ser maiores que uma linha de log
	wcDefaultTimeout = 2 * time.Hour
	wcUpdateTimeout  = 4 * time.Hour
	wcWaitDelay      = 15 * time.Second
	wcLinePrefix     = "##WC "
)

var (
	wcRunIDRe = regexp.MustCompile(`^wc-[0-9a-f]{32}$`)
	wcKeyRe   = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

	errWinCareBusy       = errors.New("busy")
	errWinCareNotRunning = errors.New("not running")
)

// -- Catálogo -------------------------------------------------------------------

type wcParam struct {
	Name     string      `json:"name"`
	Label    string      `json:"label"`
	Type     string      `json:"type"`
	Options  []string    `json:"options,omitempty"`
	Default  interface{} `json:"default,omitempty"`
	Required bool        `json:"required"`
}

type wcTask struct {
	Key         string    `json:"key"`
	Label       string    `json:"label"`
	Group       string    `json:"group"`
	Description string    `json:"description"`
	Default     bool      `json:"default"`
	Platforms   []string  `json:"platforms"`
	SelfService bool      `json:"selfService"`
	Reboot      bool      `json:"reboot"`
	Dangerous   bool      `json:"dangerous"`
	Params      []wcParam `json:"params"`
}

type wcModule struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Platforms   []string `json:"platforms"`
	Tasks       []wcTask `json:"tasks"`
}

type wcCatalog struct {
	Version string     `json:"version"`
	Modules []wcModule `json:"modules"`
}

var (
	wcCatalogOnce sync.Once
	wcCatalogVal  *wcCatalog
	wcCatalogErr  error
)

func wincareCatalog() (*wcCatalog, error) {
	wcCatalogOnce.Do(func() {
		var c wcCatalog
		if err := json.Unmarshal(wincareCatalogRaw, &c); err != nil {
			wcCatalogErr = fmt.Errorf("invalid embedded catalog: %w", err)
			return
		}
		wcCatalogVal = &c
	})
	return wcCatalogVal, wcCatalogErr
}

func wcHasPlatform(platforms []string, goos string) bool {
	for _, p := range platforms {
		if p == goos {
			return true
		}
	}
	return false
}

// filterWinCareCatalog devolve só os módulos e tarefas disponíveis em goos.
func filterWinCareCatalog(c *wcCatalog, goos string) *wcCatalog {
	out := &wcCatalog{Version: c.Version, Modules: []wcModule{}}
	for _, m := range c.Modules {
		if !wcHasPlatform(m.Platforms, goos) {
			continue
		}
		fm := m
		fm.Platforms = []string{goos}
		fm.Tasks = []wcTask{}
		for _, t := range m.Tasks {
			if !wcHasPlatform(t.Platforms, goos) {
				continue
			}
			ft := t
			if ft.Params == nil {
				ft.Params = []wcParam{}
			}
			fm.Tasks = append(fm.Tasks, ft)
		}
		if len(fm.Tasks) > 0 {
			out.Modules = append(out.Modules, fm)
		}
	}
	return out
}

func wincareCatalogJSON(goos string) (string, error) {
	c, err := wincareCatalog()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(filterWinCareCatalog(c, goos)); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// -- Validação da requisição ----------------------------------------------------

type wcPlan struct {
	RunID   string
	Module  wcModule
	Tasks   []wcTask
	Params  map[string]interface{}
	Timeout time.Duration
}

func validWinCareRunID(id string) bool {
	return wcRunIDRe.MatchString(id)
}

func buildWinCarePlan(payload map[string]string, c *wcCatalog, goos string) (*wcPlan, error) {
	runID := strings.TrimSpace(payload["run_id"])
	if !validWinCareRunID(runID) {
		return nil, errors.New("invalid run_id")
	}
	modKey := strings.TrimSpace(payload["module"])
	var mod *wcModule
	for i := range c.Modules {
		if c.Modules[i].Key == modKey {
			mod = &c.Modules[i]
			break
		}
	}
	if mod == nil {
		return nil, fmt.Errorf("unknown module: %s", wcClip(modKey, 64))
	}
	if !wcHasPlatform(mod.Platforms, goos) {
		return nil, fmt.Errorf("module %s not supported on %s", mod.Key, goos)
	}

	plan := &wcPlan{RunID: runID, Module: *mod, Timeout: wcDefaultTimeout}
	if mod.Key == "windows_update" {
		plan.Timeout = wcUpdateTimeout
	}
	seen := map[string]bool{}
	for _, raw := range strings.Split(payload["tasks"], ",") {
		key := strings.TrimSpace(raw)
		if key == "" || seen[key] {
			continue
		}
		var task *wcTask
		for i := range mod.Tasks {
			if mod.Tasks[i].Key == key {
				task = &mod.Tasks[i]
				break
			}
		}
		if task == nil {
			return nil, fmt.Errorf("unknown task: %s", wcClip(key, 64))
		}
		if !wcHasPlatform(task.Platforms, goos) {
			return nil, fmt.Errorf("task %s not supported on %s", key, goos)
		}
		seen[key] = true
		plan.Tasks = append(plan.Tasks, *task)
	}
	if len(plan.Tasks) == 0 {
		return nil, errors.New("no tasks")
	}

	params, err := normalizeWinCareParams(payload["params"], plan.Tasks)
	if err != nil {
		return nil, err
	}
	plan.Params = params
	return plan, nil
}

// normalizeWinCareParams aceita só os parâmetros declarados pelas tarefas escolhidas e
// converte cada valor para o tipo do catálogo.
func normalizeWinCareParams(raw string, tasks []wcTask) (map[string]interface{}, error) {
	in := map[string]interface{}{}
	if s := strings.TrimSpace(raw); s != "" && s != "null" {
		if err := json.Unmarshal([]byte(s), &in); err != nil {
			return nil, errors.New("invalid params: expected a JSON object")
		}
	}
	out := map[string]interface{}{}
	for _, t := range tasks {
		for _, p := range t.Params {
			if _, done := out[p.Name]; done {
				continue
			}
			v, present := in[p.Name]
			if !present || v == nil {
				if p.Required {
					return nil, fmt.Errorf("missing param: %s", p.Name)
				}
				if p.Default != nil {
					out[p.Name] = p.Default
				}
				continue
			}
			nv, err := wcConvertParam(p, v)
			if err != nil {
				return nil, fmt.Errorf("invalid param %s: %v", p.Name, err)
			}
			if p.Required {
				if s, ok := nv.(string); ok && s == "" {
					return nil, fmt.Errorf("missing param: %s", p.Name)
				}
			}
			out[p.Name] = nv
		}
	}
	return out, nil
}

func wcConvertParam(p wcParam, v interface{}) (interface{}, error) {
	switch p.Type {
	case "bool":
		switch x := v.(type) {
		case bool:
			return x, nil
		case float64:
			return x != 0, nil
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(x))
			if err != nil {
				return nil, errors.New("expected bool")
			}
			return b, nil
		}
		return nil, errors.New("expected bool")
	case "number":
		switch x := v.(type) {
		case float64:
			return x, nil
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, errors.New("expected number")
			}
			return f, nil
		}
		return nil, errors.New("expected number")
	default:
		var s string
		switch x := v.(type) {
		case string:
			s = x
		case float64:
			s = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			s = strconv.FormatBool(x)
		default:
			return nil, errors.New("expected string")
		}
		s = strings.TrimSpace(s)
		if len(s) > wcMaxLine {
			return nil, errors.New("value too long")
		}
		if strings.ContainsRune(s, 0) {
			return nil, errors.New("invalid character")
		}
		if p.Type == "select" && s != "" {
			ok := false
			for _, o := range p.Options {
				if o == s {
					ok = true
					break
				}
			}
			if !ok {
				return nil, errors.New("value not in options")
			}
		}
		return s, nil
	}
}

// -- Eventos ----------------------------------------------------------------------

var (
	wcLogLevels    = map[string]bool{"INFO": true, "WARN": true, "ERROR": true, "SUCCESS": true}
	wcTaskStatuses = map[string]bool{"running": true, "ok": true, "warning": true, "error": true, "skipped": true}
)

// wcClip corta s em no máximo n bytes sem quebrar um caractere UTF-8.
func wcClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func wcString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// parseWinCareLine interpreta uma linha "##WC {json}" emitida pelos scripts. Só os tipos
// log, progress, task e result são aceitos; seq e time são sempre definidos pelo agente.
func parseWinCareLine(line string) (map[string]interface{}, bool) {
	if !strings.HasPrefix(line, wcLinePrefix) {
		return nil, false
	}
	var in map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(line[len(wcLinePrefix):])), &in); err != nil {
		return nil, false
	}
	typ, _ := in["type"].(string)
	ev := map[string]interface{}{"type": typ}
	switch typ {
	case "log":
		level := strings.ToUpper(wcString(in["level"]))
		if !wcLogLevels[level] {
			level = "INFO"
		}
		ev["level"] = level
		ev["message"] = wcClip(wcString(in["message"]), wcMaxLine)
	case "progress":
		f, ok := in["value"].(float64)
		if !ok {
			return nil, false
		}
		ev["value"] = int(math.Max(0, math.Min(100, math.Round(f))))
		if m := wcString(in["message"]); m != "" {
			ev["message"] = wcClip(m, wcMaxLine)
		}
	case "task":
		key, _ := in["key"].(string)
		status, _ := in["status"].(string)
		if !wcKeyRe.MatchString(key) || !wcTaskStatuses[status] {
			return nil, false
		}
		ev["key"] = key
		ev["status"] = status
		if m := wcString(in["message"]); m != "" {
			ev["message"] = wcClip(m, wcMaxLine)
		}
	case "result":
		ev["data"] = in["data"]
	default:
		return nil, false
	}
	return ev, true
}

type wcPublisher interface {
	Publish(subject string, data []byte) error
}

type wcEmitter struct {
	mu         sync.Mutex
	pub        wcPublisher
	subject    string
	now        func() time.Time
	seq        int
	merging    bool
	pending    []string
	pendingLvl string
	pendingLen int
	dropped    int
	plan       map[string]bool
	taskStatus map[string]string
}

func newWinCareEmitter(pub wcPublisher, subject string, tasks []wcTask) *wcEmitter {
	plan := map[string]bool{}
	for _, t := range tasks {
		plan[t.Key] = true
	}
	return &wcEmitter{pub: pub, subject: subject, now: time.Now, plan: plan, taskStatus: map[string]string{}}
}

// publish precisa ser chamado com mu travado.
func (e *wcEmitter) publish(ev map[string]interface{}) {
	e.seq++
	ev["seq"] = e.seq
	ev["time"] = e.now().UTC().Format(time.RFC3339)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ev); err != nil {
		return
	}
	var resp []byte
	if err := codec.NewEncoderBytes(&resp, new(codec.MsgpackHandle)).Encode(strings.TrimSpace(buf.String())); err != nil {
		return
	}
	_ = e.pub.Publish(e.subject, resp)
}

func wcLevelRank(l string) int {
	switch l {
	case "ERROR":
		return 3
	case "WARN":
		return 2
	case "SUCCESS":
		return 1
	}
	return 0
}

func (e *wcEmitter) flushPending() {
	if len(e.pending) == 0 {
		return
	}
	if e.seq < wcMaxEvents-2 {
		e.publish(map[string]interface{}{"type": "log", "level": e.pendingLvl, "message": strings.Join(e.pending, "\n")})
	} else {
		e.dropped += len(e.pending)
	}
	e.pending = nil
	e.pendingLen = 0
	e.pendingLvl = ""
}

func (e *wcEmitter) handle(ev map[string]interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	typ, _ := ev["type"].(string)
	if typ == "log" {
		if !e.merging && e.seq < wcMaxEvents-wcReservedEvents {
			e.publish(ev)
			return
		}
		// Perto do limite de eventos, linhas de log seguidas viram um único evento de até 4 KB.
		e.merging = true
		msg, _ := ev["message"].(string)
		level, _ := ev["level"].(string)
		if e.pendingLen > 0 && e.pendingLen+len(msg)+1 > wcMaxLine {
			e.flushPending()
		}
		e.pending = append(e.pending, msg)
		e.pendingLen += len(msg) + 1
		if wcLevelRank(level) >= wcLevelRank(e.pendingLvl) {
			e.pendingLvl = level
		}
		return
	}
	e.flushPending()
	if typ == "task" {
		key, _ := ev["key"].(string)
		if !e.plan[key] {
			e.publishLogLocked("WARN", "Evento de tarefa desconhecida ignorado: "+key)
			return
		}
		e.taskStatus[key], _ = ev["status"].(string)
	}
	if typ == "progress" && e.merging {
		e.dropped++
		return
	}
	if e.seq < wcMaxEvents-2 {
		e.publish(ev)
	} else {
		e.dropped++
	}
}

func (e *wcEmitter) publishLogLocked(level, msg string) {
	if e.seq < wcMaxEvents-2 {
		e.publish(map[string]interface{}{"type": "log", "level": level, "message": wcClip(msg, wcMaxLine)})
	} else {
		e.dropped++
	}
}

func (e *wcEmitter) log(level, msg string) {
	e.handle(map[string]interface{}{"type": "log", "level": level, "message": wcClip(msg, wcMaxLine)})
}

// handleLine converte uma linha do processo em evento: ##WC vira o evento correspondente,
// linha comum vira log INFO e stderr vira log WARN.
func (e *wcEmitter) handleLine(line string, stderr bool) {
	line = strings.TrimPrefix(strings.TrimRight(line, "\r"), "\ufeff")
	line = strings.ToValidUTF8(line, "\uFFFD")
	if strings.TrimSpace(line) == "" {
		return
	}
	if !stderr {
		if ev, ok := parseWinCareLine(line); ok {
			e.handle(ev)
			return
		}
	}
	level := "INFO"
	if stderr {
		level = "WARN"
	}
	e.log(level, wcClip(line, wcMaxLine))
}

func (e *wcEmitter) statusOf(key string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.taskStatus[key]
}

func (e *wcEmitter) done(status string, d time.Duration, reboot bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.flushPending()
	if e.dropped > 0 && e.seq < wcMaxEvents-1 {
		e.publish(map[string]interface{}{"type": "log", "level": "WARN",
			"message": fmt.Sprintf("%d evento(s) descartado(s): limite de %d eventos por execução", e.dropped, wcMaxEvents)})
	}
	e.publish(map[string]interface{}{"type": "done", "status": status, "durationMs": d.Milliseconds(), "rebootRequired": reboot})
}

// wcLineWriter separa a saída do processo em linhas, limitando o tamanho de cada uma.
type wcLineWriter struct {
	buf      []byte
	overflow bool
	fn       func(string)
}

func (w *wcLineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		if !w.overflow {
			room := wcMaxRawLine - len(w.buf)
			if len(chunk) > room {
				w.buf = append(w.buf, chunk[:room]...)
				w.overflow = true
			} else {
				w.buf = append(w.buf, chunk...)
			}
		}
		if i < 0 {
			break
		}
		w.emit()
		p = p[i+1:]
	}
	return n, nil
}

func (w *wcLineWriter) emit() {
	line := string(w.buf)
	if w.overflow {
		// Linha cortada: não é mais um JSON válido e vira log comum de no máximo 4 KB.
		line = strings.TrimPrefix(line, wcLinePrefix)
	}
	w.buf = w.buf[:0]
	w.overflow = false
	w.fn(line)
}

func (w *wcLineWriter) Flush() {
	if len(w.buf) > 0 || w.overflow {
		w.emit()
	}
}

// -- Execução ---------------------------------------------------------------------

type wcActive struct {
	runID     string
	cancel    context.CancelFunc
	cancelled int32
	done      chan struct{}
}

type wcRunner struct {
	mu      sync.Mutex
	active  *wcActive
	goos    string
	scripts fs.FS
	// timeout substitui o tempo limite do catálogo quando maior que zero (usado nos testes).
	timeout time.Duration
}

var wincareRunner = &wcRunner{goos: runtime.GOOS, scripts: wincareScriptsFS}

// HandleWinCareRPC atende wincare_catalog, wincare_run e wincare_cancel.
func (a *Agent) HandleWinCareRPC(nc *nats.Conn, msg *nats.Msg, p *NatsMsg) {
	switch p.Func {
	case "wincare_catalog":
		s, err := wincareCatalogJSON(runtime.GOOS)
		if err != nil {
			a.Logger.Errorln("wincare_catalog:", err)
			respondString(msg, "error: "+err.Error())
			return
		}
		respondString(msg, s)
	case "wincare_run":
		if err := wincareRunner.Start(p.Data, nc, a.AgentID, wincareBaseDir(a)); err != nil {
			a.Logger.Debugln("wincare_run:", err)
			respondString(msg, "error: "+err.Error())
			return
		}
		respondString(msg, "started")
	case "wincare_cancel":
		if err := wincareRunner.Cancel(strings.TrimSpace(p.Data["run_id"])); err != nil {
			respondString(msg, "error: "+err.Error())
			return
		}
		respondString(msg, "ok")
	default:
		respondString(msg, "error: unknown command")
	}
}

// Start valida a requisição e inicia a execução em segundo plano.
func (r *wcRunner) Start(payload map[string]string, pub wcPublisher, agentID, baseDir string) error {
	c, err := wincareCatalog()
	if err != nil {
		return err
	}
	plan, err := buildWinCarePlan(payload, c, r.goos)
	if err != nil {
		return err
	}
	if r.timeout > 0 {
		plan.Timeout = r.timeout
	}
	r.mu.Lock()
	if r.active != nil {
		r.mu.Unlock()
		return errWinCareBusy
	}
	ctx, cancel := context.WithTimeout(context.Background(), plan.Timeout)
	act := &wcActive{runID: plan.RunID, cancel: cancel, done: make(chan struct{})}
	r.active = act
	r.mu.Unlock()

	go r.execute(ctx, act, plan, pub, agentID+".cmdoutput."+plan.RunID, baseDir)
	return nil
}

// Cancel encerra a execução em andamento com esse run_id.
func (r *wcRunner) Cancel(runID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil || r.active.runID != runID {
		return errWinCareNotRunning
	}
	atomic.StoreInt32(&r.active.cancelled, 1)
	r.active.cancel()
	return nil
}

func (r *wcRunner) execute(ctx context.Context, act *wcActive, plan *wcPlan, pub wcPublisher, subject, baseDir string) {
	start := time.Now()
	em := newWinCareEmitter(pub, subject, plan.Tasks)
	status := "error"
	reboot := false

	defer func() {
		if rec := recover(); rec != nil {
			em.log("ERROR", fmt.Sprintf("Falha interna do agente: %v", rec))
			status = "error"
		}
		act.cancel()
		r.mu.Lock()
		if r.active == act {
			r.active = nil
		}
		r.mu.Unlock()
		close(act.done)
		em.done(status, time.Since(start), reboot)
	}()

	dir, err := wincareMakeRunDir(baseDir)
	if err != nil {
		em.log("ERROR", "Não foi possível criar o diretório temporário: "+err.Error())
		return
	}
	defer wcRemoveAll(dir)

	script, reqPath, err := r.writeScripts(dir, plan)
	if err != nil {
		em.log("ERROR", "Não foi possível preparar os scripts: "+err.Error())
		return
	}

	cmd := wincareCommand(ctx, r.goos, script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"WINCARE_REQUEST="+reqPath,
		"WINCARE_DIR="+dir,
		"WINCARE_RUN_ID="+plan.RunID,
		"WINCARE_MODULE="+plan.Module.Key,
		"WINCARE_TASKS="+wcTaskKeys(plan.Tasks),
	)
	stdout := &wcLineWriter{fn: func(l string) { em.handleLine(l, false) }}
	stderr := &wcLineWriter{fn: func(l string) { em.handleLine(l, true) }}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	wincareSetProcAttrs(cmd)
	cmd.Cancel = func() error { return wincareKillTree(cmd) }
	cmd.WaitDelay = wcWaitDelay

	if err := cmd.Start(); err != nil {
		em.log("ERROR", "Não foi possível iniciar o script: "+err.Error())
		return
	}
	waitErr := cmd.Wait()
	stdout.Flush()
	stderr.Flush()

	switch {
	case atomic.LoadInt32(&act.cancelled) == 1:
		status = "cancelled"
		em.log("WARN", "Execução cancelada")
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		status = "timeout"
		em.log("ERROR", fmt.Sprintf("Tempo limite de %s excedido; processos encerrados", plan.Timeout))
	default:
		status = r.finalStatus(em, plan, waitErr)
	}
	for _, t := range plan.Tasks {
		if s := em.statusOf(t.Key); t.Reboot && (s == "ok" || s == "warning") {
			reboot = true
		}
	}
}

func (r *wcRunner) finalStatus(em *wcEmitter, plan *wcPlan, waitErr error) string {
	status := "ok"
	for _, t := range plan.Tasks {
		switch em.statusOf(t.Key) {
		case "error":
			status = "error"
		case "warning":
			if status == "ok" {
				status = "warning"
			}
		case "ok", "skipped":
		default:
			em.log("ERROR", "A tarefa "+t.Key+" não informou o resultado")
			status = "error"
		}
	}
	if waitErr != nil {
		em.log("WARN", "O script terminou com erro: "+waitErr.Error())
		if status == "ok" {
			status = "warning"
		}
	}
	return status
}

func wcTaskKeys(tasks []wcTask) string {
	keys := make([]string, 0, len(tasks))
	for _, t := range tasks {
		keys = append(keys, t.Key)
	}
	return strings.Join(keys, ",")
}

// writeScripts grava o harness, o script do módulo e o JSON da requisição no diretório
// privado da execução. Devolve o caminho do script de entrada e do JSON.
func (r *wcRunner) writeScripts(dir string, plan *wcPlan) (string, string, error) {
	sub, runtimeName, ext := "unix", "_runtime.sh", ".sh"
	if r.goos == "windows" {
		sub, runtimeName, ext = "windows", "_runtime.ps1", ".ps1"
	}
	var entry string
	for _, name := range []string{runtimeName, plan.Module.Key + ext} {
		data, err := fs.ReadFile(r.scripts, path.Join("wincare", sub, name))
		if err != nil {
			return "", "", fmt.Errorf("script %s: %w", name, err)
		}
		if ext == ".ps1" && !bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
			// O Windows PowerShell 5.1 só lê UTF-8 corretamente com BOM.
			data = append([]byte("\xef\xbb\xbf"), data...)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			return "", "", err
		}
		entry = p
	}
	req := map[string]interface{}{
		"run_id": plan.RunID,
		"module": plan.Module.Key,
		"tasks":  strings.Split(wcTaskKeys(plan.Tasks), ","),
		"params": plan.Params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return "", "", err
	}
	reqPath := filepath.Join(dir, "request.json")
	if err := os.WriteFile(reqPath, data, 0o600); err != nil {
		return "", "", err
	}
	return entry, reqPath, nil
}

func wincareCommand(ctx context.Context, goos, script string) *exec.Cmd {
	if goos == "windows" {
		ps := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if _, err := os.Stat(ps); err != nil {
			ps = "powershell.exe"
		}
		return exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
	}
	return exec.CommandContext(ctx, "/bin/bash", script)
}

func wcRemoveAll(dir string) {
	for i := 0; i < 5; i++ {
		if err := os.RemoveAll(dir); err == nil {
			return
		}
		time.Sleep(time.Second)
	}
}
