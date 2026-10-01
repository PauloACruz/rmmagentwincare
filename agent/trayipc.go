package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

// Canal local entre o servico do agente e o app de bandeja (wincare-tray), que roda na sessao do usuario.
// O app pede um token curto; o agente identifica o usuario pelo processo do outro lado do canal
// (nunca pelo que o app informa) e pede o token ao servidor com a credencial do agente.

type trayRequest struct {
	Cmd string `json:"cmd"`
}

type trayResponse struct {
	Token     string `json:"token,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	APIURL    string `json:"api_url,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	Username  string `json:"username,omitempty"`
	Error     string `json:"error,omitempty"`
}

type trayTokenResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type trayTokenCache struct {
	mu     sync.Mutex
	tokens map[string]trayTokenResult
}

var trayTokens = trayTokenCache{tokens: map[string]trayTokenResult{}}

// RunTrayIPC escuta o canal local do app de bandeja enquanto o servico estiver ativo.
func (a *Agent) RunTrayIPC() {
	for {
		ln, err := listenTrayIPC()
		if err != nil {
			a.Logger.Errorln("Tray IPC listen:", err)
			time.Sleep(time.Minute)
			continue
		}
		a.Logger.Debugln("Tray IPC listening")
		for {
			conn, err := ln.Accept()
			if err != nil {
				a.Logger.Errorln("Tray IPC accept:", err)
				break
			}
			go a.handleTrayConn(conn)
		}
		ln.Close()
		time.Sleep(5 * time.Second)
	}
}

func (a *Agent) handleTrayConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	enc := json.NewEncoder(conn)

	line, err := bufio.NewReaderSize(conn, 4096).ReadString('\n')
	if err != nil && line == "" {
		return
	}
	var req trayRequest
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &req); err != nil {
		_ = enc.Encode(trayResponse{Error: "invalid request"})
		return
	}

	switch req.Cmd {
	case "ping":
		_ = enc.Encode(trayResponse{Hostname: a.Hostname})
	case "token":
		username, err := trayPeerUsername(conn)
		if err != nil {
			a.Logger.Debugln("Tray IPC peer:", err)
			_ = enc.Encode(trayResponse{Error: "unable to identify user"})
			return
		}
		tok, err := a.trayToken(username)
		if err != nil {
			a.Logger.Errorln("Tray token:", err)
			_ = enc.Encode(trayResponse{Error: "unable to get token from server"})
			return
		}
		_ = enc.Encode(trayResponse{
			Token:     tok.Token,
			ExpiresAt: tok.ExpiresAt.UTC().Format(time.RFC3339),
			APIURL:    a.BaseURL,
			Hostname:  a.Hostname,
			Username:  username,
		})
	default:
		_ = enc.Encode(trayResponse{Error: "unknown command"})
	}
}

// trayToken reaproveita o token do usuario enquanto faltar mais de 1 hora para expirar.
func (a *Agent) trayToken(username string) (trayTokenResult, error) {
	key := strings.ToLower(username)
	trayTokens.mu.Lock()
	cached, ok := trayTokens.tokens[key]
	trayTokens.mu.Unlock()
	if ok && time.Until(cached.ExpiresAt) > time.Hour {
		return cached, nil
	}

	var result trayTokenResult
	r, err := a.rClient.R().SetBody(map[string]string{"username": username}).SetResult(&result).Post("/api/v3/traytoken/")
	if err != nil {
		return result, err
	}
	if r.IsError() || result.Token == "" {
		return result, errors.New("server returned " + r.Status())
	}

	trayTokens.mu.Lock()
	trayTokens.tokens[key] = result
	trayTokens.mu.Unlock()
	return result, nil
}
