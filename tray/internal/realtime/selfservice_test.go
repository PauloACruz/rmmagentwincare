package realtime

import (
	"testing"

	"github.com/pauloacruz/rmmagentwincare/tray/internal/api"
)

func TestSelfServiceChangedDispatch(t *testing.T) {
	rec := &recorder{}
	c := &Client{Handler: rec}
	for _, raw := range []string{
		`{"type":1,"target":"selfServiceChanged","arguments":[{"runId":"r1","status":"running","progress":35,"message":"Limpando arquivos"}]}`,
		`{"type":1,"target":"selfServiceChanged","arguments":[{"runId":"r1","status":"ok","progress":100,"message":null}]}`,
		`{"type":1,"target":"selfServiceChanged","arguments":[]}`,
		`{"type":1,"target":"selfServiceChanged","arguments":["x"]}`,
	} {
		msg, err := ParseMessage([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		c.dispatch(msg)
	}
	want := []api.SelfServiceChange{
		{RunID: "r1", Status: "running", Progress: 35, Message: "Limpando arquivos"},
		{RunID: "r1", Status: "ok", Progress: 100},
	}
	if len(rec.selfService) != len(want) {
		t.Fatalf("eventos inesperados: %+v", rec.selfService)
	}
	for i := range want {
		if rec.selfService[i] != want[i] {
			t.Fatalf("evento %d: %+v, esperava %+v", i, rec.selfService[i], want[i])
		}
	}
}

func TestParseSelfServiceChangedRejectsMissingFields(t *testing.T) {
	msg, err := ParseMessage([]byte(`{"type":1,"target":"selfServiceChanged","arguments":[{"progress":10}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSelfServiceChanged(msg.Arguments); err == nil {
		t.Fatal("deveria recusar evento sem runId e status")
	}
}
