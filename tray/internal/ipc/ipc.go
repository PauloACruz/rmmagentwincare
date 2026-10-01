// Package ipc fala com o servico do agente WinCare pelo canal local (named pipe no Windows,
// socket Unix no Linux e no macOS) para obter o token curto do app de bandeja.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// ErrUnavailable indica que o servico do agente nao respondeu pelo canal local.
var ErrUnavailable = errors.New("servico WinCare indisponivel")

// RenewBefore e a folga antes da expiracao a partir da qual o token e renovado.
const RenewBefore = time.Hour

// Credentials e a resposta do agente ao comando "token".
type Credentials struct {
	Token     string
	ExpiresAt time.Time
	APIURL    string
	Hostname  string
	Username  string
}

type request struct {
	Cmd     string `json:"cmd"`
	Refresh bool   `json:"refresh,omitempty"`
}

type response struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	APIURL    string `json:"api_url"`
	Hostname  string `json:"hostname"`
	Username  string `json:"username"`
	Error     string `json:"error"`
}

// Client pede tokens ao agente. Path vazio usa o caminho padrao do sistema operacional.
type Client struct {
	Path    string
	Timeout time.Duration
}

func (c *Client) path() string {
	if c.Path != "" {
		return c.Path
	}
	return DefaultPath
}

// RequestToken envia {"cmd":"token"} e devolve as credenciais emitidas pelo agente.
// Com refresh, o agente ignora o token que guardou e pede outro ao servidor.
func (c *Client) RequestToken(ctx context.Context, refresh bool) (Credentials, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := dial(ctx, c.path())
	if err != nil {
		return Credentials{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	payload, err := json.Marshal(request{Cmd: "token", Refresh: refresh})
	if err != nil {
		return Credentials{}, err
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return Credentials{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return readResponse(conn)
}

func readResponse(conn net.Conn) (Credentials, error) {
	line, err := bufio.NewReaderSize(conn, 8192).ReadString('\n')
	if err != nil && line == "" {
		return Credentials{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var resp response
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &resp); err != nil {
		return Credentials{}, fmt.Errorf("resposta invalida do agente: %w", err)
	}
	if resp.Error != "" {
		return Credentials{}, fmt.Errorf("agente: %s", resp.Error)
	}
	if resp.Token == "" {
		return Credentials{}, errors.New("agente nao devolveu token")
	}
	creds := Credentials{
		Token:    resp.Token,
		APIURL:   strings.TrimRight(resp.APIURL, "/"),
		Hostname: resp.Hostname,
		Username: resp.Username,
	}
	if resp.ExpiresAt != "" {
		exp, err := time.Parse(time.RFC3339, resp.ExpiresAt)
		if err != nil {
			return Credentials{}, fmt.Errorf("expires_at invalido: %w", err)
		}
		creds.ExpiresAt = exp
	}
	if creds.APIURL == "" {
		return Credentials{}, errors.New("agente nao informou a URL da API")
	}
	return creds, nil
}

// Source abstrai quem fornece o token (o Manager em producao, falsos nos testes).
type Source interface {
	Credentials(ctx context.Context, force bool) (Credentials, error)
}

// Manager guarda o token atual e pede outro ao agente quando faltar menos de RenewBefore
// para expirar ou quando o chamador forcar (por exemplo, depois de um 401 da API).
type Manager struct {
	Client *Client
	Now    func() time.Time

	mu      sync.Mutex
	current Credentials
}

// Credentials devolve o token em cache ou pede um novo ao agente.
func (m *Manager) Credentials(ctx context.Context, force bool) (Credentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	if !force && m.current.Token != "" && m.current.ExpiresAt.Sub(now()) > RenewBefore {
		return m.current, nil
	}
	creds, err := m.Client.RequestToken(ctx, force)
	if err != nil {
		return Credentials{}, err
	}
	m.current = creds
	return creds, nil
}

// Cached devolve o ultimo token obtido sem falar com o agente.
func (m *Manager) Cached() (Credentials, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current, m.current.Token != ""
}
