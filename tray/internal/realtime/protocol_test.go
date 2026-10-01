package realtime

import (
	"encoding/json"
	"testing"
)

func TestSplitRecords(t *testing.T) {
	buf := []byte("{}\x1e{\"type\":6}\x1e{\"type\":1,\"tar")
	records, rest := SplitRecords(buf)
	if len(records) != 2 || string(records[0]) != "{}" || string(records[1]) != `{"type":6}` {
		t.Fatalf("registros inesperados: %q", records)
	}
	if string(rest) != `{"type":1,"tar` {
		t.Fatalf("resto inesperado: %q", rest)
	}
	records, rest = SplitRecords(append(rest, []byte("get\":\"x\"}\x1e")...))
	if len(records) != 1 || string(records[0]) != `{"type":1,"target":"x"}` || len(rest) != 0 {
		t.Fatalf("remontagem falhou: %q %q", records, rest)
	}
}

func TestHandshake(t *testing.T) {
	req := HandshakeRequest()
	if req[len(req)-1] != RecordSeparator {
		t.Fatal("handshake sem separador")
	}
	var body map[string]any
	if err := json.Unmarshal(req[:len(req)-1], &body); err != nil || body["protocol"] != "json" || body["version"] != float64(1) {
		t.Fatalf("handshake inesperado: %s", req)
	}
	if err := ParseHandshake([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := ParseHandshake([]byte(`{"error":"Requested protocol 'x' is not available."}`)); err == nil {
		t.Fatal("deveria recusar handshake com erro")
	}
}

func TestParseMessages(t *testing.T) {
	ping, err := ParseMessage([]byte(`{"type":6}`))
	if err != nil || ping.Type != TypePing {
		t.Fatalf("ping: %+v %v", ping, err)
	}
	if p := PingMessage(); string(p) != "{\"type\":6}\x1e" {
		t.Fatalf("ping do cliente: %q", p)
	}
	inv, err := ParseMessage([]byte(`{"type":1,"target":"ticketMessage","arguments":[12,{"id":3,"authorType":"technician","authorName":"Ana","body":"Ola","createdAt":"2026-10-01T10:00:00Z","attachments":[]}]}`))
	if err != nil || inv.Type != TypeInvocation || inv.Target != "ticketMessage" || len(inv.Arguments) != 2 {
		t.Fatalf("invocation: %+v %v", inv, err)
	}
	closeMsg, err := ParseMessage([]byte(`{"type":7,"error":"Server is shutting down","allowReconnect":true}`))
	if err != nil || closeMsg.Type != TypeClose || !closeMsg.AllowReconnect {
		t.Fatalf("close: %+v %v", closeMsg, err)
	}
	if _, err := ParseMessage([]byte(`{}`)); err == nil {
		t.Fatal("mensagem sem tipo deveria falhar")
	}
}

func TestHubURL(t *testing.T) {
	u, err := HubURL("https://rmm.example.com/", "a+b")
	if err != nil || u != "wss://rmm.example.com/hubs/tray?access_token=a%2Bb" {
		t.Fatalf("url: %s %v", u, err)
	}
	if u, _ := HubURL("http://localhost:5000", "x"); u != "ws://localhost:5000/hubs/tray?access_token=x" {
		t.Fatalf("url http: %s", u)
	}
}
