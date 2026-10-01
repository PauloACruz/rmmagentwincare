package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/pauloacruz/rmmagentwincare/tray/internal/api"
	"github.com/pauloacruz/rmmagentwincare/tray/internal/ipc"
)

// HubPath e o caminho do hub do app de bandeja.
const HubPath = "/hubs/tray"

// Handler recebe os eventos do hub.
type Handler interface {
	TicketMessage(ticketID int, msg api.Message)
	TicketChanged(ticket api.Ticket)
	ConnectionChanged(connected bool)
}

// Client mantem a conexao com o hub e reconecta com espera crescente.
type Client struct {
	Tokens  ipc.Source
	HTTP    *http.Client
	Handler Handler
	Logf    func(format string, args ...any)

	PingInterval  time.Duration // padrao 15 s
	ServerTimeout time.Duration // padrao 30 s sem nada do servidor
	MinBackoff    time.Duration // padrao 1 s
	MaxBackoff    time.Duration // padrao 30 s
}

var errUnauthorized = errors.New("hub recusou o token (401)")

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func orDefault(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

// Run conecta e reconecta ate ctx ser cancelado.
func (c *Client) Run(ctx context.Context) {
	minBackoff := orDefault(c.MinBackoff, time.Second)
	maxBackoff := orDefault(c.MaxBackoff, 30*time.Second)
	backoff := minBackoff
	force := false
	for {
		established, err := c.session(ctx, force)
		if ctx.Err() != nil {
			return
		}
		force = errors.Is(err, errUnauthorized)
		if established {
			backoff = minBackoff
		}
		c.logf("realtime: desconectado (%v); nova tentativa em %s", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// HubURL troca http(s) por ws(s) e acrescenta o token na query access_token.
func HubURL(apiURL, token string) (string, error) {
	u, err := url.Parse(strings.TrimRight(apiURL, "/") + HubPath)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("esquema nao suportado: %s", u.Scheme)
	}
	u.RawQuery = url.Values{"access_token": {token}}.Encode()
	return u.String(), nil
}

// session abre uma conexao e a mantem ate cair. established indica que o handshake foi aceito.
func (c *Client) session(ctx context.Context, forceToken bool) (established bool, err error) {
	creds, err := c.Tokens.Credentials(ctx, forceToken)
	if err != nil {
		return false, err
	}
	hubURL, err := HubURL(creds.APIURL, creds.Token)
	if err != nil {
		return false, err
	}

	dialCtx, cancelDial := context.WithTimeout(ctx, 30*time.Second)
	conn, resp, err := websocket.Dial(dialCtx, hubURL, &websocket.DialOptions{HTTPClient: c.HTTP})
	cancelDial()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return false, errUnauthorized
		}
		return false, err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var writeMu sync.Mutex
	write := func(p []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return conn.Write(wctx, websocket.MessageText, p)
	}

	if err := write(HandshakeRequest()); err != nil {
		return false, err
	}

	defer func() {
		if established {
			c.Handler.ConnectionChanged(false)
		}
	}()

	pingEvery := orDefault(c.PingInterval, 15*time.Second)
	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := write(PingMessage()); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	timeout := orDefault(c.ServerTimeout, 30*time.Second)
	var pending []byte
	handshakeDone := false
	for {
		rctx, rcancel := context.WithTimeout(ctx, timeout)
		_, data, err := conn.Read(rctx)
		rcancel()
		if err != nil {
			return established, err
		}
		var records [][]byte
		records, pending = SplitRecords(append(pending, data...))
		for _, rec := range records {
			if !handshakeDone {
				if err := ParseHandshake(rec); err != nil {
					return false, err
				}
				handshakeDone = true
				established = true
				c.Handler.ConnectionChanged(true)
				continue
			}
			msg, err := ParseMessage(rec)
			if err != nil {
				c.logf("realtime: %v", err)
				continue
			}
			switch msg.Type {
			case TypeInvocation:
				c.dispatch(msg)
			case TypePing:
				if err := write(PingMessage()); err != nil {
					return established, err
				}
			case TypeClose:
				if msg.Error != "" {
					return established, errors.New("hub fechou a conexao: " + msg.Error)
				}
				return established, errors.New("hub fechou a conexao")
			}
		}
	}
}

func (c *Client) dispatch(msg Message) {
	switch msg.Target {
	case "ticketMessage":
		if len(msg.Arguments) < 2 {
			return
		}
		var id int
		var m api.Message
		if json.Unmarshal(msg.Arguments[0], &id) != nil || json.Unmarshal(msg.Arguments[1], &m) != nil {
			c.logf("realtime: ticketMessage invalido")
			return
		}
		c.Handler.TicketMessage(id, m)
	case "ticketChanged":
		if len(msg.Arguments) < 1 {
			return
		}
		var t api.Ticket
		if json.Unmarshal(msg.Arguments[0], &t) != nil {
			c.logf("realtime: ticketChanged invalido")
			return
		}
		c.Handler.TicketChanged(t)
	}
}
