package agent

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

func TestBuildSNMPSystem(t *testing.T) {
	s := buildSNMPSystem([]gosnmp.SnmpPDU{
		{Name: ".1.3.6.1.2.1.1.1.0", Type: gosnmp.OctetString, Value: []byte("Switch X")},
		{Name: "1.3.6.1.2.1.1.2.0", Type: gosnmp.ObjectIdentifier, Value: ".1.3.6.1.4.1.9.1.1"},
		{Name: ".1.3.6.1.2.1.1.3.0", Type: gosnmp.TimeTicks, Value: uint32(123456)},
		{Name: ".1.3.6.1.2.1.1.4.0", Type: gosnmp.OctetString, Value: []byte("noc@x")},
		{Name: ".1.3.6.1.2.1.1.5.0", Type: gosnmp.OctetString, Value: []byte("sw01")},
		{Name: ".1.3.6.1.2.1.1.6.0", Type: gosnmp.NoSuchObject, Value: nil},
	})
	want := snmpSystem{Descr: "Switch X", ObjectID: ".1.3.6.1.4.1.9.1.1", UptimeTicks: 123456, Contact: "noc@x", Name: "sw01"}
	if s != want {
		t.Fatalf("system = %+v", s)
	}
}

func pdu(oid string, typ gosnmp.Asn1BER, v interface{}) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Name: oid, Type: typ, Value: v}
}

func TestBuildSNMPInterfacesPrefersHC(t *testing.T) {
	cols := map[string][]gosnmp.SnmpPDU{
		"ifIndex":       {pdu(".1.3.6.1.2.1.2.2.1.1.2", gosnmp.Integer, 2), pdu(".1.3.6.1.2.1.2.2.1.1.1", gosnmp.Integer, 1)},
		"ifDescr":       {pdu(".1.3.6.1.2.1.2.2.1.2.1", gosnmp.OctetString, []byte("lo")), pdu(".1.3.6.1.2.1.2.2.1.2.2", gosnmp.OctetString, []byte("eth0"))},
		"ifType":        {pdu(".1.3.6.1.2.1.2.2.1.3.2", gosnmp.Integer, 6)},
		"ifSpeed":       {pdu(".1.3.6.1.2.1.2.2.1.5.1", gosnmp.Gauge32, uint(10000000)), pdu(".1.3.6.1.2.1.2.2.1.5.2", gosnmp.Gauge32, uint(4294967295))},
		"ifHighSpeed":   {pdu(".1.3.6.1.2.1.31.1.1.1.15.2", gosnmp.Gauge32, uint(10000))},
		"ifAdminStatus": {pdu(".1.3.6.1.2.1.2.2.1.7.1", gosnmp.Integer, 1), pdu(".1.3.6.1.2.1.2.2.1.7.2", gosnmp.Integer, 1)},
		"ifOperStatus":  {pdu(".1.3.6.1.2.1.2.2.1.8.1", gosnmp.Integer, 1), pdu(".1.3.6.1.2.1.2.2.1.8.2", gosnmp.Integer, 2)},
		"ifInOctets":    {pdu(".1.3.6.1.2.1.2.2.1.10.1", gosnmp.Counter32, uint(500)), pdu(".1.3.6.1.2.1.2.2.1.10.2", gosnmp.Counter32, uint(7))},
		"ifOutOctets":   {pdu(".1.3.6.1.2.1.2.2.1.16.1", gosnmp.Counter32, uint(600)), pdu(".1.3.6.1.2.1.2.2.1.16.2", gosnmp.Counter32, uint(8))},
		"ifInErrors":    {pdu(".1.3.6.1.2.1.2.2.1.14.2", gosnmp.Counter32, uint(3))},
		"ifOutErrors":   {pdu(".1.3.6.1.2.1.2.2.1.20.2", gosnmp.Counter32, uint(4))},
		"ifName":        {pdu(".1.3.6.1.2.1.31.1.1.1.1.2", gosnmp.OctetString, []byte("Gi0/1"))},
		"ifAlias":       {pdu(".1.3.6.1.2.1.31.1.1.1.18.2", gosnmp.OctetString, []byte("uplink"))},
		"ifHCInOctets":  {pdu(".1.3.6.1.2.1.31.1.1.1.6.2", gosnmp.Counter64, uint64(9000000000))},
		"ifHCOutOctets": {pdu(".1.3.6.1.2.1.31.1.1.1.10.2", gosnmp.Counter64, uint64(18000000000)), pdu(".1.3.6.1.2.1.31.1.1.1.10.9", gosnmp.NoSuchInstance, nil)},
	}
	got := buildSNMPInterfaces(cols)
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	lo, eth := got[0], got[1]
	if lo.Index != 1 || lo.Name != "lo" || lo.HC || lo.InOctets != 500 || lo.OutOctets != 600 || lo.SpeedBps != 10000000 || lo.OperStatus != "up" {
		t.Fatalf("lo = %+v", lo)
	}
	want := snmpInterface{Index: 2, Name: "Gi0/1", Descr: "eth0", Alias: "uplink", Type: 6, SpeedBps: 10000000000,
		AdminStatus: "up", OperStatus: "down", InOctets: 9000000000, OutOctets: 18000000000, InErrors: 3, OutErrors: 4, HC: true}
	if eth != want {
		t.Fatalf("eth = %+v", eth)
	}
}

func TestBuildSNMPSensorsAndResultJSON(t *testing.T) {
	sensors := []snmpSensorTarget{
		{ID: json.RawMessage(`"s1"`), OID: "1.3.6.1.4.1.1.1.0"},
		{ID: json.RawMessage(`"s2"`), OID: ".1.3.6.1.4.1.1.2.0"},
		{ID: json.RawMessage(`"s3"`), OID: ".1.3.6.1.4.1.1.3.0"},
		{ID: json.RawMessage(`"s4"`), OID: ".1.3.6.1.4.1.1.4.0"},
		{ID: json.RawMessage(`"s5"`), OID: ".1.3.6.1.4.1.1.5.0"},
	}
	values := map[string]gosnmp.SnmpPDU{
		".1.3.6.1.4.1.1.1.0": pdu(".1.3.6.1.4.1.1.1.0", gosnmp.Integer, -5),
		".1.3.6.1.4.1.1.2.0": pdu(".1.3.6.1.4.1.1.2.0", gosnmp.OctetString, []byte("toner baixo")),
		".1.3.6.1.4.1.1.3.0": pdu(".1.3.6.1.4.1.1.3.0", gosnmp.OctetString, []byte("42.5")),
		".1.3.6.1.4.1.1.4.0": pdu(".1.3.6.1.4.1.1.4.0", gosnmp.NoSuchObject, nil),
	}
	got := buildSNMPSensors(sensors, values)
	res := snmpResult{DeviceID: json.RawMessage(`7`), Time: "2026-10-01T00:00:00Z", Reachable: true, Sensors: got}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, part := range []string{
		`"device_id":7`,
		`{"id":"s1","value":-5,"text":null}`,
		`{"id":"s2","value":null,"text":"toner baixo"}`,
		`{"id":"s3","value":42.5,"text":"42.5"}`,
		`{"id":"s4","value":null,"text":"NoSuchObject"}`,
		`{"id":"s5","value":null,"text":"sem resposta"}`,
		`"error":null`, `"system":null`,
	} {
		if !strings.Contains(s, part) {
			t.Fatalf("faltou %s em %s", part, s)
		}
	}
}

func TestSNMPV3FlagsAndProtocols(t *testing.T) {
	cases := map[string]gosnmp.SnmpV3MsgFlags{"noAuthNoPriv": gosnmp.NoAuthNoPriv, "authNoPriv": gosnmp.AuthNoPriv, "authPriv": gosnmp.AuthPriv}
	for in, want := range cases {
		got, err := snmpV3Flags(in)
		if err != nil || got != want {
			t.Fatalf("%s = %v, %v", in, got, err)
		}
	}
	if _, err := snmpV3Flags("authpriv"); err == nil {
		t.Fatal("nivel invalido aceito")
	}
	p := &snmpV3Params{Username: "u", SecurityLevel: "authPriv", AuthProtocol: "SHA512", AuthPassword: "a12345678", PrivProtocol: "AES256", PrivPassword: "p12345678"}
	if a, _ := snmpAuthProtocol(p); a != gosnmp.SHA512 {
		t.Fatalf("auth = %v", a)
	}
	if pr, _ := snmpPrivProtocol(p); pr != gosnmp.AES256 {
		t.Fatalf("priv = %v", pr)
	}
	p.SecurityLevel = "authNoPriv"
	if pr, _ := snmpPrivProtocol(p); pr != gosnmp.NoPriv {
		t.Fatalf("authNoPriv deveria ignorar priv: %v", pr)
	}
	p.SecurityLevel = "noAuthNoPriv"
	if a, _ := snmpAuthProtocol(p); a != gosnmp.NoAuth {
		t.Fatalf("noAuthNoPriv deveria ignorar auth: %v", a)
	}
}

func TestSNMPTargetValidation(t *testing.T) {
	ok := snmpTarget{Host: "sw01.local", Version: "v2c", Community: "c", Interval: 5, Sensors: []snmpSensorTarget{{OID: "1.3.6.1.2.1.1.3.0"}}}
	if err := ok.normalize(); err != nil {
		t.Fatal(err)
	}
	if ok.Port != 161 || ok.Interval != 60 || ok.Timeout != 5 {
		t.Fatalf("padroes: %+v", ok)
	}
	bad := []snmpTarget{
		{Host: "bad host", Version: "v2c", Community: "c"},
		{Host: "1.2.3.4;rm", Version: "v2c", Community: "c"},
		{Host: "10.0.0.1", Port: 70000, Version: "v2c", Community: "c"},
		{Host: "10.0.0.1", Version: "v1", Community: "c"},
		{Host: "10.0.0.1", Version: "v2c", Community: "c", Sensors: []snmpSensorTarget{{OID: "iso.3.6"}}},
		{Host: "10.0.0.1", Version: "v3", V3: &snmpV3Params{Username: "u", SecurityLevel: "authPriv", AuthPassword: "x"}},
	}
	for i, b := range bad {
		if err := b.normalize(); err == nil {
			t.Fatalf("caso %d deveria falhar", i)
		} else if strings.Contains(err.Error(), "x") && b.V3 != nil {
			t.Fatalf("erro expoe credencial: %v", err)
		}
	}
}

func TestBuildSNMPTrap(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	src := &net.UDPAddr{IP: net.ParseIP("10.1.1.5"), Port: 50000}
	v2 := &gosnmp.SnmpPacket{Version: gosnmp.Version2c, Community: "pub", Variables: []gosnmp.SnmpPDU{
		pdu(".1.3.6.1.2.1.1.3.0", gosnmp.TimeTicks, uint32(100)),
		pdu(".1.3.6.1.6.3.1.1.4.1.0", gosnmp.ObjectIdentifier, ".1.3.6.1.6.3.1.1.5.3"),
		pdu(".1.3.6.1.2.1.2.2.1.1.2", gosnmp.Integer, 2),
	}}
	tr, ok := buildSNMPTrap(v2, src, now)
	if !ok || tr.Version != "v2c" || tr.TrapOID != ".1.3.6.1.6.3.1.1.5.3" || tr.SourceIP != "10.1.1.5" || tr.Community != "pub" || len(tr.Varbinds) != 3 {
		t.Fatalf("trap v2c: %+v", tr)
	}
	if tr.Varbinds[2] != (snmpTrapVarbind{OID: ".1.3.6.1.2.1.2.2.1.1.2", Type: "Integer", Value: "2"}) {
		t.Fatalf("varbind: %+v", tr.Varbinds[2])
	}
	v1 := &gosnmp.SnmpPacket{Version: gosnmp.Version1, Community: "pub", SnmpTrap: gosnmp.SnmpTrap{Enterprise: ".1.3.6.1.4.1.8072", GenericTrap: 6, SpecificTrap: 17}}
	if tr, _ := buildSNMPTrap(v1, src, now); tr.TrapOID != ".1.3.6.1.4.1.8072.0.17" || tr.Version != "v1" {
		t.Fatalf("trap v1 especifico: %+v", tr)
	}
	v1.GenericTrap = 2
	if tr, _ := buildSNMPTrap(v1, src, now); tr.TrapOID != ".1.3.6.1.6.3.1.1.5.3" {
		t.Fatalf("trap v1 generico: %+v", tr)
	}
	if _, ok := buildSNMPTrap(&gosnmp.SnmpPacket{Version: gosnmp.Version3}, src, now); ok {
		t.Fatal("trap v3 deveria ser ignorado")
	}
}
