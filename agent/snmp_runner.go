package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
	nats "github.com/nats-io/nats.go"
)

// Agendador do coletor SNMP: le a configuracao, mantem uma goroutine por
// dispositivo, envia resultados e traps em lote.

const (
	snmpConfigInterval  = 5 * time.Minute
	snmpResultsInterval = 30 * time.Second
	snmpTrapsInterval   = 10 * time.Second
	snmpMaxConcurrent   = 16
	snmpMaxBuffered     = 1000
	snmpMaxDevices      = 2000
)

type snmpConfig struct {
	Enabled  bool         `json:"enabled"`
	TrapPort int          `json:"trap_port"`
	Devices  []snmpTarget `json:"devices"`
}

type snmpTrapVarbind struct {
	OID   string `json:"oid"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type snmpTrap struct {
	Time      string            `json:"time"`
	SourceIP  string            `json:"source_ip"`
	Version   string            `json:"version"`
	Community string            `json:"community"`
	TrapOID   string            `json:"trap_oid"`
	Varbinds  []snmpTrapVarbind `json:"varbinds"`
}

type snmpDeviceRunner struct {
	key    string
	cancel context.CancelFunc
}

type snmpCollector struct {
	a       *Agent
	sem     chan struct{}
	mu      sync.Mutex
	results []snmpResult
	traps   []snmpTrap
	devices map[string]*snmpDeviceRunner

	trapPort     int
	trapListener *gosnmp.TrapListener
}

// RunSNMP roda o papel de coletor SNMP; so faz algo quando o servidor habilita.
func (a *Agent) RunSNMP() {
	c := &snmpCollector{a: a, sem: make(chan struct{}, snmpMaxConcurrent), devices: map[string]*snmpDeviceRunner{}}
	time.Sleep(time.Duration(randRange(15, 45)) * time.Second)
	go c.flushLoop(snmpResultsInterval, c.sendResults)
	go c.flushLoop(snmpTrapsInterval, c.sendTraps)
	for {
		if cfg, ok := c.fetchConfig(); ok {
			c.apply(cfg)
		}
		time.Sleep(snmpConfigInterval)
	}
}

func (c *snmpCollector) fetchConfig() (snmpConfig, bool) {
	url := fmt.Sprintf("/api/v3/%s/snmp/", c.a.AgentID)
	// sem debug do resty: a resposta traz credenciais
	r, err := c.a.rClient.R().SetDebug(false).SetResult(&snmpConfig{}).Get(url)
	if err != nil {
		c.a.Logger.Debugln("snmp config:", err)
		return snmpConfig{}, false
	}
	if r.StatusCode() == http.StatusNotFound {
		return snmpConfig{}, true
	}
	if r.IsError() {
		c.a.Logger.Debugln("snmp config: status", r.StatusCode())
		return snmpConfig{}, false
	}
	cfg, ok := r.Result().(*snmpConfig)
	if !ok || cfg == nil {
		return snmpConfig{}, false
	}
	return *cfg, true
}

// apply sincroniza os agendadores com a configuracao recebida.
func (c *snmpCollector) apply(cfg snmpConfig) {
	wanted := map[string]snmpTarget{}
	if cfg.Enabled {
		for _, t := range cfg.Devices {
			if len(wanted) >= snmpMaxDevices {
				break
			}
			id := string(t.ID)
			if id == "" || id == "null" {
				continue
			}
			if err := t.normalize(); err != nil {
				c.a.Logger.Errorf("snmp: dispositivo %s ignorado: %v", id, err)
				continue
			}
			wanted[id] = t
		}
	}

	for id, run := range c.devices {
		t, ok := wanted[id]
		if ok && run.key == snmpTargetKey(t) {
			continue
		}
		run.cancel()
		delete(c.devices, id)
	}
	for id, t := range wanted {
		if _, ok := c.devices[id]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		c.devices[id] = &snmpDeviceRunner{key: snmpTargetKey(t), cancel: cancel}
		go c.pollDevice(ctx, t)
	}

	port := 0
	if cfg.Enabled {
		port = cfg.TrapPort
		if port <= 0 || port > 65535 {
			port = 162
		}
	}
	c.updateTrapListener(port)
}

func snmpTargetKey(t snmpTarget) string {
	b, err := json.Marshal(t)
	if err != nil {
		return ""
	}
	return string(b)
}

func (c *snmpCollector) pollDevice(ctx context.Context, t snmpTarget) {
	interval := time.Duration(t.Interval) * time.Second
	jitter := t.Interval
	if jitter > 30 {
		jitter = 30
	}
	timer := time.NewTimer(time.Duration(randRange(1, jitter+1)) * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		select {
		case <-ctx.Done():
			return
		case c.sem <- struct{}{}:
		}
		budget := time.Duration(t.Timeout*(t.Retries+1))*time.Second*20 + 10*time.Second
		if budget > interval {
			budget = interval
		}
		cctx, cancel := context.WithTimeout(ctx, budget)
		res := collectSNMP(cctx, &t, false)
		cancel()
		<-c.sem
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		c.results = appendCapped(c.results, res, snmpMaxBuffered)
		c.mu.Unlock()
		timer.Reset(interval)
	}
}

func appendCapped(list []snmpResult, r snmpResult, max int) []snmpResult {
	list = append(list, r)
	if len(list) > max {
		list = list[len(list)-max:]
	}
	return list
}

func (c *snmpCollector) flushLoop(every time.Duration, send func()) {
	for {
		time.Sleep(every)
		send()
	}
}

func (c *snmpCollector) sendResults() {
	c.mu.Lock()
	batch := c.results
	c.results = nil
	c.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if c.post("/api/v3/snmp/results/", map[string]interface{}{"results": batch}) {
		return
	}
	c.mu.Lock()
	merged := append(batch, c.results...)
	if len(merged) > snmpMaxBuffered {
		merged = merged[len(merged)-snmpMaxBuffered:]
	}
	c.results = merged
	c.mu.Unlock()
}

func (c *snmpCollector) sendTraps() {
	c.mu.Lock()
	batch := c.traps
	c.traps = nil
	c.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if c.post("/api/v3/snmp/traps/", map[string]interface{}{"traps": batch}) {
		return
	}
	c.mu.Lock()
	merged := append(batch, c.traps...)
	if len(merged) > snmpMaxBuffered {
		merged = merged[:snmpMaxBuffered]
	}
	c.traps = merged
	c.mu.Unlock()
}

// post devolve true quando o lote pode sair do buffer (aceito ou recusado de vez).
func (c *snmpCollector) post(url string, body interface{}) bool {
	r, err := c.a.rClient.R().SetDebug(false).SetBody(body).Post(url)
	if err != nil {
		c.a.Logger.Debugln("snmp send:", url, err)
		return false
	}
	if r.IsError() {
		code := r.StatusCode()
		if code >= 400 && code < 500 && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests {
			c.a.Logger.Errorf("snmp send: %s recusado (status %d), lote descartado", url, code)
			return true
		}
		c.a.Logger.Debugln("snmp send:", url, "status", code)
		return false
	}
	return true
}

func (c *snmpCollector) addTrap(t snmpTrap) {
	c.mu.Lock()
	if len(c.traps) < snmpMaxBuffered {
		c.traps = append(c.traps, t)
	}
	c.mu.Unlock()
}

func (c *snmpCollector) updateTrapListener(port int) {
	if port == c.trapPort {
		return
	}
	if c.trapListener != nil {
		c.trapListener.Close()
		c.trapListener = nil
	}
	c.trapPort = port
	if port == 0 {
		return
	}
	tl, err := listenSNMPTraps(fmt.Sprintf("0.0.0.0:%d", port), c.addTrap)
	if err != nil {
		c.a.Logger.Errorf("snmp: nao foi possivel escutar traps na porta UDP %d: %v (segue so com a coleta)", port, err)
		return
	}
	c.trapListener = tl
}

// listenSNMPTraps abre o receptor de traps v1/v2c e espera ele ficar pronto.
func listenSNMPTraps(addr string, onTrap func(snmpTrap)) (*gosnmp.TrapListener, error) {
	tl := gosnmp.NewTrapListener()
	tl.Params = &gosnmp.GoSNMP{
		Version:   gosnmp.Version2c,
		Transport: "udp",
		Timeout:   2 * time.Second,
		MaxOids:   gosnmp.MaxOids,
	}
	tl.OnNewTrap = func(p *gosnmp.SnmpPacket, u *net.UDPAddr) {
		if t, ok := buildSNMPTrap(p, u, time.Now()); ok {
			onTrap(t)
		}
	}
	errc := make(chan error, 1)
	go func() { errc <- tl.Listen(addr) }()
	select {
	case <-tl.Listening():
		return tl, nil
	case err := <-errc:
		if err == nil {
			err = fmt.Errorf("receptor encerrado")
		}
		return nil, err
	case <-time.After(5 * time.Second):
		tl.Close()
		return nil, fmt.Errorf("tempo esgotado ao abrir %s", addr)
	}
}

const (
	oidSnmpTrapOID   = ".1.3.6.1.6.3.1.1.4.1.0"
	oidSnmpTrapsBase = ".1.3.6.1.6.3.1.1.5."
)

// buildSNMPTrap converte o pacote recebido; traps v3 sao ignorados nesta fase.
func buildSNMPTrap(p *gosnmp.SnmpPacket, u *net.UDPAddr, now time.Time) (snmpTrap, bool) {
	if p == nil {
		return snmpTrap{}, false
	}
	t := snmpTrap{Time: now.UTC().Format(time.RFC3339), Community: p.Community, Varbinds: []snmpTrapVarbind{}}
	if u != nil {
		t.SourceIP = u.IP.String()
	}
	switch p.Version {
	case gosnmp.Version1:
		t.Version = "v1"
		ent := normalizeOID(p.Enterprise)
		if p.GenericTrap == 6 {
			t.TrapOID = ent + ".0." + fmt.Sprint(p.SpecificTrap)
		} else {
			t.TrapOID = oidSnmpTrapsBase + fmt.Sprint(p.GenericTrap+1)
		}
	case gosnmp.Version2c:
		t.Version = "v2c"
	default:
		return snmpTrap{}, false
	}
	for _, v := range p.Variables {
		oid := normalizeOID(v.Name)
		if oid == oidSnmpTrapOID && t.TrapOID == "" {
			t.TrapOID = normalizeOID(snmpText(v))
		}
		t.Varbinds = append(t.Varbinds, snmpTrapVarbind{OID: oid, Type: v.Type.String(), Value: truncateRunes(snmpText(v), 2000)})
	}
	return t, true
}

// HandleSNMPTest atende o comando NATS snmp_test com uma leitura do grupo system.
func (a *Agent) HandleSNMPTest(msg *nats.Msg, payload *NatsMsg) {
	type testResp struct {
		Reachable bool        `json:"reachable"`
		Error     *string     `json:"error"`
		RttMs     int64       `json:"rtt_ms"`
		System    *snmpSystem `json:"system"`
	}
	respond := func(r testResp) {
		b, err := json.Marshal(r)
		if err != nil {
			respondString(msg, `{"reachable":false,"error":"falha interna","rtt_ms":0,"system":null}`)
			return
		}
		respondString(msg, string(b))
	}
	fail := func(s string) { respond(testResp{Error: &s}) }

	var raw string
	if payload != nil && payload.Data != nil {
		raw = payload.Data["target"]
	}
	if raw == "" || len(raw) > 64*1024 {
		fail("target ausente ou invalido")
		return
	}
	var t snmpTarget
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		fail("target invalido")
		return
	}
	if err := t.normalize(); err != nil {
		fail(err.Error())
		return
	}
	budget := time.Duration(t.Timeout*(t.Retries+1))*time.Second + 5*time.Second
	if budget > 60*time.Second {
		budget = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	r := collectSNMP(ctx, &t, true)
	respond(testResp{Reachable: r.Reachable, Error: r.Error, RttMs: r.RttMs, System: r.System})
}
