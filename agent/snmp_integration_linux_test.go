//go:build linux
// +build linux

package agent

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Integracao com um snmpd local. Pula quando snmpd/snmptrap nao estao instalados
// (apt-get install -y snmpd snmp).

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	c.Close()
	return port
}

func startTestSnmpd(t *testing.T) int {
	t.Helper()
	bin, err := exec.LookPath("snmpd")
	if err != nil {
		if _, e := os.Stat("/usr/sbin/snmpd"); e != nil {
			t.Skip("snmpd nao instalado")
		}
		bin = "/usr/sbin/snmpd"
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "snmpd.conf")
	content := `rocommunity wctest 127.0.0.1
createUser wcuser SHA "authpass123" AES "privpass123"
rouser wcuser priv
sysName wincare-test
sysLocation lab
sysContact noc@example.com
`
	if err := os.WriteFile(conf, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	port := freeUDPPort(t)
	cmd := exec.Command(bin, "-f", "-Lo", "-C", "-c", conf, fmt.Sprintf("udp:127.0.0.1:%d", port))
	cmd.Env = append(os.Environ(), "SNMP_PERSISTENT_DIR="+dir, "MIBS=")
	if err := cmd.Start(); err != nil {
		t.Skipf("snmpd nao iniciou: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	probe := snmpTarget{Host: "127.0.0.1", Port: port, Version: "v2c", Community: "wctest", Timeout: 1}
	if err := probe.normalize(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if r := collectSNMP(context.Background(), &probe, true); r.Reachable {
			return port
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("snmpd nao respondeu")
	return 0
}

func checkCollected(t *testing.T, r snmpResult) {
	t.Helper()
	if !r.Reachable || r.Error != nil {
		t.Fatalf("coleta falhou: %+v", r)
	}
	if r.System == nil || r.System.Name != "wincare-test" || r.System.Location != "lab" || r.System.Contact != "noc@example.com" || r.System.UptimeTicks == 0 || r.System.ObjectID == "" {
		t.Fatalf("system: %+v", r.System)
	}
	if len(r.Interfaces) == 0 {
		t.Fatal("nenhuma interface")
	}
	var lo *snmpInterface
	for i := range r.Interfaces {
		if r.Interfaces[i].Descr == "lo" {
			lo = &r.Interfaces[i]
		}
	}
	if lo == nil || !lo.HC || lo.OperStatus != "up" || lo.AdminStatus != "up" || lo.InOctets == 0 {
		t.Fatalf("interface lo: %+v", lo)
	}
	if len(r.Sensors) != 2 || r.Sensors[0].Value == nil || *r.Sensors[0].Value <= 0 || r.Sensors[1].Text == nil || *r.Sensors[1].Text != "wincare-test" {
		t.Fatalf("sensores: %+v", r.Sensors)
	}
}

func TestSNMPIntegrationPollV2cAndV3(t *testing.T) {
	port := startTestSnmpd(t)
	sensors := []snmpSensorTarget{{ID: []byte(`"up"`), OID: "1.3.6.1.2.1.1.3.0"}, {ID: []byte(`"name"`), OID: ".1.3.6.1.2.1.1.5.0"}}

	v2 := snmpTarget{ID: []byte(`1`), Host: "127.0.0.1", Port: port, Version: "v2c", Community: "wctest", Timeout: 2, Retries: 1, Interfaces: true, Sensors: sensors}
	if err := v2.normalize(); err != nil {
		t.Fatal(err)
	}
	checkCollected(t, collectSNMP(context.Background(), &v2, false))

	v3 := snmpTarget{ID: []byte(`2`), Host: "127.0.0.1", Port: port, Version: "v3", Timeout: 2, Retries: 1, Interfaces: true, Sensors: sensors,
		V3: &snmpV3Params{Username: "wcuser", SecurityLevel: "authPriv", AuthProtocol: "SHA", AuthPassword: "authpass123", PrivProtocol: "AES", PrivPassword: "privpass123"}}
	if err := v3.normalize(); err != nil {
		t.Fatal(err)
	}
	checkCollected(t, collectSNMP(context.Background(), &v3, false))

	wrong := v3
	wrong.V3 = &snmpV3Params{Username: "wcuser", SecurityLevel: "authPriv", AuthProtocol: "SHA", AuthPassword: "senhaerrada1", PrivProtocol: "AES", PrivPassword: "privpass123"}
	wrong.Timeout, wrong.Retries = 1, 0
	if r := collectSNMP(context.Background(), &wrong, true); r.Reachable || r.Error == nil {
		t.Fatalf("senha errada deveria falhar: %+v", r)
	}
}

func TestSNMPIntegrationTraps(t *testing.T) {
	bin, err := exec.LookPath("snmptrap")
	if err != nil {
		t.Skip("snmptrap nao instalado")
	}
	port := freeUDPPort(t)
	var mu sync.Mutex
	var got []snmpTrap
	tl, err := listenSNMPTraps(fmt.Sprintf("127.0.0.1:%d", port), func(tr snmpTrap) {
		mu.Lock()
		got = append(got, tr)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tl.Close()

	dest := "127.0.0.1:" + strconv.Itoa(port)
	cmds := [][]string{
		{"-v", "2c", "-c", "wctest", dest, "", "1.3.6.1.4.1.8072.2.3.0.1", "1.3.6.1.4.1.8072.2.3.2.1", "i", "123"},
		{"-v", "1", "-c", "wctest", dest, "1.3.6.1.4.1.8072.2.3", "127.0.0.1", "6", "17", "", "1.3.6.1.2.1.1.5.0", "s", "ola"},
	}
	for _, args := range cmds {
		c := exec.Command(bin, args...)
		c.Env = append(os.Environ(), "MIBS=")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("snmptrap: %v %s", err, out)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("traps recebidos: %d", len(got))
	}
	byVersion := map[string]snmpTrap{}
	for _, tr := range got {
		byVersion[tr.Version] = tr
	}
	v2 := byVersion["v2c"]
	if v2.TrapOID != ".1.3.6.1.4.1.8072.2.3.0.1" || v2.Community != "wctest" || v2.SourceIP != "127.0.0.1" {
		t.Fatalf("trap v2c: %+v", v2)
	}
	found := false
	for _, vb := range v2.Varbinds {
		if vb.OID == ".1.3.6.1.4.1.8072.2.3.2.1" && vb.Value == "123" && vb.Type == "Integer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("varbinds v2c: %+v", v2.Varbinds)
	}
	v1 := byVersion["v1"]
	if v1.TrapOID != ".1.3.6.1.4.1.8072.2.3.0.17" || len(v1.Varbinds) != 1 || v1.Varbinds[0].Value != "ola" {
		t.Fatalf("trap v1: %+v", v1)
	}
}
