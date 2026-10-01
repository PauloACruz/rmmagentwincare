package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/pauloacruz/rmmagentwincare/tray/internal/api"
	"github.com/pauloacruz/rmmagentwincare/tray/internal/ipc"
)

type staticTokens struct{ url string }

func (s staticTokens) Credentials(context.Context, bool) (ipc.Credentials, error) {
	return ipc.Credentials{Token: "tok", APIURL: s.url}, nil
}

type recorder struct {
	mu       sync.Mutex
	messages []int
	changed  []string
	conn     []bool
	done     chan struct{}
}

func (r *recorder) TicketMessage(id int, m api.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, id)
	if m.AuthorType != "technician" {
		panic("authorType")
	}
}

func (r *recorder) TicketChanged(t api.Ticket) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changed = append(r.changed, t.Status)
	close(r.done)
}

func (r *recorder) ConnectionChanged(c bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conn = append(r.conn, c)
}

// Hub falso: confere o token e o handshake, envia ping e duas invocacoes no mesmo quadro e
// espera o ping de resposta do cliente.
func TestClientAgainstFakeHub(t *testing.T) {
	gotPong := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != HubPath || r.URL.Query().Get("access_token") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		_, hs, err := c.Read(ctx)
		if err != nil || string(hs) != string(HandshakeRequest()) {
			t.Errorf("handshake: %q %v", hs, err)
			return
		}
		frame := "{}\x1e{\"type\":6}\x1e" +
			`{"type":1,"target":"ticketMessage","arguments":[5,{"id":1,"authorType":"technician","authorName":"Ana","body":"Oi","createdAt":"2026-10-01T10:00:00Z","attachments":[]}]}` + "\x1e" +
			`{"type":1,"target":"ticketChanged","arguments":[{"id":5,"title":"x","status":"in_progress","priority":"medium","createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-01T10:00:00Z","assignedToName":"Ana","chatEnabled":true,"lastMessageAt":null}]}` + "\x1e"
		if err := c.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
			return
		}
		_, p, err := c.Read(ctx)
		if err == nil && string(p) == string(PingMessage()) {
			close(gotPong)
		}
		<-ctx.Done()
	}))
	defer srv.Close()

	rec := &recorder{done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cl := &Client{Tokens: staticTokens{url: srv.URL}, HTTP: srv.Client(), Handler: rec, PingInterval: time.Hour}
	go cl.Run(ctx)

	for _, ch := range []chan struct{}{rec.done, gotPong} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("tempo esgotado esperando o hub")
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.messages) != 1 || rec.messages[0] != 5 || len(rec.changed) != 1 || rec.changed[0] != "in_progress" {
		t.Fatalf("eventos inesperados: %+v %+v", rec.messages, rec.changed)
	}
	if len(rec.conn) == 0 || !rec.conn[0] {
		t.Fatalf("esperava ConnectionChanged(true): %v", rec.conn)
	}
}
