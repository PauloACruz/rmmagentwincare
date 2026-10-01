package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"
)

// Coleta SNMP do agente coletor (fase 8). Nada aqui registra credenciais em log.

type snmpV3Params struct {
	Username      string `json:"username"`
	SecurityLevel string `json:"security_level"`
	AuthProtocol  string `json:"auth_protocol"`
	AuthPassword  string `json:"auth_password"`
	PrivProtocol  string `json:"priv_protocol"`
	PrivPassword  string `json:"priv_password"`
}

type snmpSensorTarget struct {
	ID  json.RawMessage `json:"id"`
	OID string          `json:"oid"`
}

type snmpTarget struct {
	ID         json.RawMessage    `json:"id,omitempty"`
	Host       string             `json:"host"`
	Port       int                `json:"port"`
	Version    string             `json:"version"`
	Community  string             `json:"community"`
	V3         *snmpV3Params      `json:"v3"`
	Interval   int                `json:"interval"`
	Timeout    int                `json:"timeout"`
	Retries    int                `json:"retries"`
	Interfaces bool               `json:"interfaces"`
	Sensors    []snmpSensorTarget `json:"sensors"`
}

type snmpSystem struct {
	Descr       string `json:"descr"`
	ObjectID    string `json:"object_id"`
	UptimeTicks uint64 `json:"uptime_ticks"`
	Contact     string `json:"contact"`
	Name        string `json:"name"`
	Location    string `json:"location"`
}

type snmpInterface struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	Descr       string `json:"descr"`
	Alias       string `json:"alias"`
	Type        int    `json:"type"`
	SpeedBps    uint64 `json:"speed_bps"`
	AdminStatus string `json:"admin_status"`
	OperStatus  string `json:"oper_status"`
	InOctets    uint64 `json:"in_octets"`
	OutOctets   uint64 `json:"out_octets"`
	InErrors    uint64 `json:"in_errors"`
	OutErrors   uint64 `json:"out_errors"`
	HC          bool   `json:"hc"`
}

type snmpSensorValue struct {
	ID    json.RawMessage `json:"id"`
	Value *float64        `json:"value"`
	Text  *string         `json:"text"`
}

type snmpResult struct {
	DeviceID   json.RawMessage   `json:"device_id"`
	Time       string            `json:"time"`
	Reachable  bool              `json:"reachable"`
	Error      *string           `json:"error"`
	RttMs      int64             `json:"rtt_ms"`
	System     *snmpSystem       `json:"system"`
	Interfaces []snmpInterface   `json:"interfaces"`
	Sensors    []snmpSensorValue `json:"sensors"`
}

const (
	oidSysDescr    = ".1.3.6.1.2.1.1.1.0"
	oidSysObjectID = ".1.3.6.1.2.1.1.2.0"
	oidSysUpTime   = ".1.3.6.1.2.1.1.3.0"
	oidSysContact  = ".1.3.6.1.2.1.1.4.0"
	oidSysName     = ".1.3.6.1.2.1.1.5.0"
	oidSysLocation = ".1.3.6.1.2.1.1.6.0"
)

var snmpSystemOIDs = []string{oidSysDescr, oidSysObjectID, oidSysUpTime, oidSysContact, oidSysName, oidSysLocation}

// Colunas da ifTable e ifXTable usadas na coleta de interfaces.
var snmpIfColumns = map[string]string{
	"ifIndex":       ".1.3.6.1.2.1.2.2.1.1",
	"ifDescr":       ".1.3.6.1.2.1.2.2.1.2",
	"ifType":        ".1.3.6.1.2.1.2.2.1.3",
	"ifSpeed":       ".1.3.6.1.2.1.2.2.1.5",
	"ifAdminStatus": ".1.3.6.1.2.1.2.2.1.7",
	"ifOperStatus":  ".1.3.6.1.2.1.2.2.1.8",
	"ifInOctets":    ".1.3.6.1.2.1.2.2.1.10",
	"ifInErrors":    ".1.3.6.1.2.1.2.2.1.14",
	"ifOutOctets":   ".1.3.6.1.2.1.2.2.1.16",
	"ifOutErrors":   ".1.3.6.1.2.1.2.2.1.20",
	"ifName":        ".1.3.6.1.2.1.31.1.1.1.1",
	"ifHCInOctets":  ".1.3.6.1.2.1.31.1.1.1.6",
	"ifHCOutOctets": ".1.3.6.1.2.1.31.1.1.1.10",
	"ifHighSpeed":   ".1.3.6.1.2.1.31.1.1.1.15",
	"ifAlias":       ".1.3.6.1.2.1.31.1.1.1.18",
}

var snmpIfColumnOrder = []string{"ifIndex", "ifDescr", "ifType", "ifSpeed", "ifAdminStatus", "ifOperStatus",
	"ifInOctets", "ifInErrors", "ifOutOctets", "ifOutErrors", "ifName", "ifHCInOctets", "ifHCOutOctets", "ifHighSpeed", "ifAlias"}

var (
	snmpOIDPattern  = regexp.MustCompile(`^\.?[0-2](\.[0-9]{1,10})+$`)
	snmpHostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62})(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}))*\.?$`)
)

func validSNMPOID(oid string) bool {
	return len(oid) <= 512 && snmpOIDPattern.MatchString(oid)
}

func validSNMPHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return snmpHostPattern.MatchString(host)
}

// normalize valida o alvo e aplica padroes e limites. Os erros nao citam credenciais.
func (t *snmpTarget) normalize() error {
	t.Host = strings.TrimSpace(t.Host)
	if !validSNMPHost(t.Host) {
		return errors.New("host invalido")
	}
	if t.Port == 0 {
		t.Port = 161
	}
	if t.Port < 1 || t.Port > 65535 {
		return errors.New("porta invalida")
	}
	if t.Interval < 60 {
		t.Interval = 60
	}
	if t.Interval > 3600 {
		t.Interval = 3600
	}
	if t.Timeout <= 0 {
		t.Timeout = 5
	}
	if t.Timeout > 60 {
		t.Timeout = 60
	}
	if t.Retries < 0 {
		t.Retries = 0
	}
	if t.Retries > 5 {
		t.Retries = 5
	}
	if len(t.Sensors) > 500 {
		return errors.New("sensores demais")
	}
	for i := range t.Sensors {
		t.Sensors[i].OID = strings.TrimSpace(t.Sensors[i].OID)
		if !validSNMPOID(t.Sensors[i].OID) {
			return fmt.Errorf("oid de sensor invalido: %q", t.Sensors[i].OID)
		}
	}
	switch t.Version {
	case "v2c":
		if t.Community == "" || len(t.Community) > 255 {
			return errors.New("comunidade invalida")
		}
	case "v3":
		if t.V3 == nil || t.V3.Username == "" || len(t.V3.Username) > 255 {
			return errors.New("usuario v3 invalido")
		}
		if _, err := snmpV3Flags(t.V3.SecurityLevel); err != nil {
			return err
		}
		if _, err := snmpAuthProtocol(t.V3); err != nil {
			return err
		}
		if _, err := snmpPrivProtocol(t.V3); err != nil {
			return err
		}
	default:
		return errors.New("versao SNMP invalida (use v2c ou v3)")
	}
	return nil
}

func snmpV3Flags(level string) (gosnmp.SnmpV3MsgFlags, error) {
	switch level {
	case "noAuthNoPriv":
		return gosnmp.NoAuthNoPriv, nil
	case "authNoPriv":
		return gosnmp.AuthNoPriv, nil
	case "authPriv":
		return gosnmp.AuthPriv, nil
	}
	return 0, errors.New("security_level invalido")
}

func snmpAuthProtocol(p *snmpV3Params) (gosnmp.SnmpV3AuthProtocol, error) {
	if p.SecurityLevel == "noAuthNoPriv" {
		return gosnmp.NoAuth, nil
	}
	if p.AuthPassword == "" {
		return 0, errors.New("senha de autenticacao ausente")
	}
	switch strings.ToUpper(p.AuthProtocol) {
	case "SHA", "SHA1", "":
		return gosnmp.SHA, nil
	case "SHA256":
		return gosnmp.SHA256, nil
	case "SHA512":
		return gosnmp.SHA512, nil
	case "MD5":
		return gosnmp.MD5, nil
	}
	return 0, errors.New("auth_protocol invalido")
}

func snmpPrivProtocol(p *snmpV3Params) (gosnmp.SnmpV3PrivProtocol, error) {
	if p.SecurityLevel != "authPriv" {
		return gosnmp.NoPriv, nil
	}
	if p.PrivPassword == "" {
		return 0, errors.New("senha de privacidade ausente")
	}
	switch strings.ToUpper(p.PrivProtocol) {
	case "AES", "":
		return gosnmp.AES, nil
	case "AES256":
		return gosnmp.AES256, nil
	case "DES":
		return gosnmp.DES, nil
	}
	return 0, errors.New("priv_protocol invalido")
}

// newSNMPClient monta o cliente gosnmp de um alvo ja normalizado.
func newSNMPClient(ctx context.Context, t *snmpTarget) (*gosnmp.GoSNMP, error) {
	g := &gosnmp.GoSNMP{
		Context:            ctx,
		Target:             t.Host,
		Port:               uint16(t.Port),
		Transport:          "udp",
		Timeout:            time.Duration(t.Timeout) * time.Second,
		Retries:            t.Retries,
		MaxOids:            gosnmp.MaxOids,
		MaxRepetitions:     25,
		ExponentialTimeout: false,
	}
	switch t.Version {
	case "v2c":
		g.Version = gosnmp.Version2c
		g.Community = t.Community
	case "v3":
		flags, err := snmpV3Flags(t.V3.SecurityLevel)
		if err != nil {
			return nil, err
		}
		auth, err := snmpAuthProtocol(t.V3)
		if err != nil {
			return nil, err
		}
		priv, err := snmpPrivProtocol(t.V3)
		if err != nil {
			return nil, err
		}
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		g.MsgFlags = flags
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 t.V3.Username,
			AuthenticationProtocol:   auth,
			AuthenticationPassphrase: t.V3.AuthPassword,
			PrivacyProtocol:          priv,
			PrivacyPassphrase:        t.V3.PrivPassword,
		}
	default:
		return nil, errors.New("versao SNMP invalida")
	}
	return g, nil
}

// collectSNMP faz uma coleta completa (ou so o grupo system quando systemOnly).
func collectSNMP(ctx context.Context, t *snmpTarget, systemOnly bool) snmpResult {
	res := snmpResult{DeviceID: t.ID, Time: time.Now().UTC().Format(time.RFC3339)}
	if len(res.DeviceID) == 0 {
		res.DeviceID = json.RawMessage("null")
	}
	fail := func(err error) snmpResult {
		msg := err.Error()
		res.Error = &msg
		return res
	}
	g, err := newSNMPClient(ctx, t)
	if err != nil {
		return fail(err)
	}
	if err := g.Connect(); err != nil {
		return fail(err)
	}
	defer g.Conn.Close()

	start := time.Now()
	pkt, err := g.Get(snmpSystemOIDs)
	if err != nil {
		return fail(err)
	}
	res.RttMs = time.Since(start).Milliseconds()
	if pkt.Error != gosnmp.NoError {
		return fail(fmt.Errorf("erro SNMP: %s", pkt.Error))
	}
	res.Reachable = true
	sys := buildSNMPSystem(pkt.Variables)
	res.System = &sys
	if systemOnly {
		return res
	}

	if t.Interfaces {
		cols := make(map[string][]gosnmp.SnmpPDU, len(snmpIfColumns))
		for _, name := range snmpIfColumnOrder {
			if ctx.Err() != nil {
				break
			}
			pdus, err := g.BulkWalkAll(snmpIfColumns[name])
			if err != nil {
				if name == "ifIndex" {
					msg := "falha ao ler interfaces: " + err.Error()
					res.Error = &msg
					break
				}
				continue
			}
			cols[name] = pdus
		}
		res.Interfaces = buildSNMPInterfaces(cols)
	}

	if len(t.Sensors) > 0 {
		oids := make([]string, 0, len(t.Sensors))
		for _, s := range t.Sensors {
			oids = append(oids, normalizeOID(s.OID))
		}
		values := map[string]gosnmp.SnmpPDU{}
		for i := 0; i < len(oids); i += 20 {
			end := i + 20
			if end > len(oids) {
				end = len(oids)
			}
			p, err := g.Get(oids[i:end])
			if err != nil {
				continue
			}
			for _, v := range p.Variables {
				values[normalizeOID(v.Name)] = v
			}
		}
		res.Sensors = buildSNMPSensors(t.Sensors, values)
	}
	return res
}

func normalizeOID(oid string) string {
	if strings.HasPrefix(oid, ".") {
		return oid
	}
	return "." + oid
}

func buildSNMPSystem(vars []gosnmp.SnmpPDU) snmpSystem {
	var s snmpSystem
	for _, v := range vars {
		switch normalizeOID(v.Name) {
		case oidSysDescr:
			s.Descr = snmpText(v)
		case oidSysObjectID:
			s.ObjectID = snmpText(v)
		case oidSysUpTime:
			s.UptimeTicks = snmpUint(v)
		case oidSysContact:
			s.Contact = snmpText(v)
		case oidSysName:
			s.Name = snmpText(v)
		case oidSysLocation:
			s.Location = snmpText(v)
		}
	}
	return s
}

var snmpIfStatus = map[uint64]string{1: "up", 2: "down", 3: "testing", 4: "unknown", 5: "dormant", 6: "notPresent", 7: "lowerLayerDown"}

func ifStatusName(v uint64) string {
	if s, ok := snmpIfStatus[v]; ok {
		return s
	}
	return "unknown"
}

// buildSNMPInterfaces junta as colunas lidas (chave = nome da coluna) por ifIndex.
func buildSNMPInterfaces(cols map[string][]gosnmp.SnmpPDU) []snmpInterface {
	byCol := make(map[string]map[int]gosnmp.SnmpPDU, len(cols))
	for name, pdus := range cols {
		prefix := snmpIfColumns[name] + "."
		m := make(map[int]gosnmp.SnmpPDU, len(pdus))
		for _, p := range pdus {
			oid := normalizeOID(p.Name)
			if !strings.HasPrefix(oid, prefix) || !snmpHasValue(p) {
				continue
			}
			idx, err := strconv.Atoi(strings.TrimPrefix(oid, prefix))
			if err != nil {
				continue
			}
			m[idx] = p
		}
		byCol[name] = m
	}
	indexes := make([]int, 0, len(byCol["ifIndex"]))
	for idx := range byCol["ifIndex"] {
		indexes = append(indexes, idx)
	}
	if len(indexes) == 0 {
		for idx := range byCol["ifDescr"] {
			indexes = append(indexes, idx)
		}
	}
	sort.Ints(indexes)

	out := make([]snmpInterface, 0, len(indexes))
	for _, idx := range indexes {
		it := snmpInterface{Index: idx}
		get := func(col string) (gosnmp.SnmpPDU, bool) {
			p, ok := byCol[col][idx]
			return p, ok
		}
		if p, ok := get("ifDescr"); ok {
			it.Descr = snmpText(p)
		}
		if p, ok := get("ifName"); ok {
			it.Name = snmpText(p)
		}
		if it.Name == "" {
			it.Name = it.Descr
		}
		if p, ok := get("ifAlias"); ok {
			it.Alias = snmpText(p)
		}
		if p, ok := get("ifType"); ok {
			it.Type = int(snmpUint(p))
		}
		if p, ok := get("ifSpeed"); ok {
			it.SpeedBps = snmpUint(p)
		}
		if p, ok := get("ifHighSpeed"); ok {
			if hs := snmpUint(p); hs > 0 && (it.SpeedBps == 0 || it.SpeedBps >= math.MaxUint32 || hs*1000000 > it.SpeedBps) {
				it.SpeedBps = hs * 1000000
			}
		}
		if p, ok := get("ifAdminStatus"); ok {
			it.AdminStatus = ifStatusName(snmpUint(p))
		} else {
			it.AdminStatus = "unknown"
		}
		if p, ok := get("ifOperStatus"); ok {
			it.OperStatus = ifStatusName(snmpUint(p))
		} else {
			it.OperStatus = "unknown"
		}
		hcIn, okIn := get("ifHCInOctets")
		hcOut, okOut := get("ifHCOutOctets")
		if okIn && okOut {
			it.HC = true
			it.InOctets = snmpUint(hcIn)
			it.OutOctets = snmpUint(hcOut)
		} else {
			if p, ok := get("ifInOctets"); ok {
				it.InOctets = snmpUint(p)
			}
			if p, ok := get("ifOutOctets"); ok {
				it.OutOctets = snmpUint(p)
			}
		}
		if p, ok := get("ifInErrors"); ok {
			it.InErrors = snmpUint(p)
		}
		if p, ok := get("ifOutErrors"); ok {
			it.OutErrors = snmpUint(p)
		}
		out = append(out, it)
	}
	return out
}

func buildSNMPSensors(sensors []snmpSensorTarget, values map[string]gosnmp.SnmpPDU) []snmpSensorValue {
	out := make([]snmpSensorValue, 0, len(sensors))
	for _, s := range sensors {
		sv := snmpSensorValue{ID: s.ID}
		if len(sv.ID) == 0 {
			sv.ID = json.RawMessage("null")
		}
		p, ok := values[normalizeOID(s.OID)]
		switch {
		case !ok:
			txt := "sem resposta"
			sv.Text = &txt
		case !snmpHasValue(p):
			txt := p.Type.String()
			sv.Text = &txt
		default:
			if f, isNum := snmpNumber(p); isNum {
				sv.Value = &f
			} else {
				txt := snmpText(p)
				if f, err := strconv.ParseFloat(strings.TrimSpace(txt), 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
					sv.Value = &f
				}
				sv.Text = &txt
			}
		}
		out = append(out, sv)
	}
	return out
}

func snmpHasValue(p gosnmp.SnmpPDU) bool {
	switch p.Type {
	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView, gosnmp.Null:
		return false
	}
	return true
}

func snmpNumber(p gosnmp.SnmpPDU) (float64, bool) {
	switch p.Type {
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Counter64, gosnmp.Uinteger32:
		b := gosnmp.ToBigInt(p.Value)
		f, _ := new(big.Float).SetInt(b).Float64()
		return f, true
	case gosnmp.OpaqueFloat:
		if v, ok := p.Value.(float32); ok {
			return float64(v), true
		}
	case gosnmp.OpaqueDouble:
		if v, ok := p.Value.(float64); ok {
			return v, true
		}
	}
	return 0, false
}

func snmpUint(p gosnmp.SnmpPDU) uint64 {
	b := gosnmp.ToBigInt(p.Value)
	if b.Sign() < 0 || !b.IsUint64() {
		return 0
	}
	return b.Uint64()
}

// snmpText formata um valor para exibicao: texto legivel, OID, IP ou hexadecimal.
func snmpText(p gosnmp.SnmpPDU) string {
	switch v := p.Value.(type) {
	case nil:
		return ""
	case []byte:
		if utf8.Valid(v) && isPrintableText(string(v)) {
			return strings.TrimRight(string(v), "\x00")
		}
		return "0x" + hex.EncodeToString(v)
	case string:
		return v
	case float32, float64:
		return fmt.Sprintf("%v", v)
	}
	if f, ok := snmpNumber(p); ok {
		if p.Type == gosnmp.Counter64 || p.Type == gosnmp.Integer || p.Type == gosnmp.Counter32 || p.Type == gosnmp.Gauge32 || p.Type == gosnmp.TimeTicks || p.Type == gosnmp.Uinteger32 {
			return gosnmp.ToBigInt(p.Value).String()
		}
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprintf("%v", p.Value)
}

func isPrintableText(s string) bool {
	for _, r := range strings.TrimRight(s, "\x00") {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}
