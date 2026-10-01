package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAgent responde ao comando "token" como o agente faria e conta quantos tokens emitiu.
func fakeAgent(t *testing.T, reply func(n int32) string) (string, *atomic.Int32) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tray.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var count atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return
				}
				var req request
				if json.Unmarshal([]byte(line), &req) != nil || req.Cmd != "token" {
					_, _ = c.Write([]byte(`{"error":"unknown command"}` + "\n"))
					return
				}
				_, _ = c.Write([]byte(reply(count.Add(1)) + "\n"))
			}(conn)
		}
	}()
	return path, &count
}

func TestRequestToken(t *testing.T) {
	path, _ := fakeAgent(t, func(int32) string {
		return `{"token":"abc","expires_at":"2030-01-02T03:04:05Z","api_url":"https://rmm.example.com/","hostname":"PC01","username":"maria"}`
	})
	c := &Client{Path: path}
	creds, err := c.RequestToken(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if creds.Token != "abc" || creds.APIURL != "https://rmm.example.com" || creds.Hostname != "PC01" || creds.Username != "maria" {
		t.Fatalf("credenciais inesperadas: %+v", creds)
	}
	if !creds.ExpiresAt.Equal(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("expires_at: %v", creds.ExpiresAt)
	}
}

func TestRequestTokenAgentError(t *testing.T) {
	path, _ := fakeAgent(t, func(int32) string { return `{"error":"unable to get token from server"}` })
	_, err := (&Client{Path: path}).RequestToken(context.Background(), false)
	if err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("esperava erro do agente, veio %v", err)
	}
}

func TestRequestTokenUnavailable(t *testing.T) {
	_, err := (&Client{Path: filepath.Join(t.TempDir(), "nada.sock"), Timeout: time.Second}).RequestToken(context.Background(), false)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("esperava ErrUnavailable, veio %v", err)
	}
}

func TestManagerRenewsNearExpiry(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	path, count := fakeAgent(t, func(n int32) string {
		// O primeiro token vence em 2 h; os seguintes em 12 h.
		exp := now.Add(2 * time.Hour)
		if n > 1 {
			exp = now.Add(12 * time.Hour)
		}
		return `{"token":"t` + string(rune('0'+n)) + `","expires_at":"` + exp.Format(time.RFC3339) + `","api_url":"https://x"}`
	})
	clock := now
	m := &Manager{Client: &Client{Path: path}, Now: func() time.Time { return clock }}
	ctx := context.Background()

	c1, err := m.Credentials(ctx, false)
	if err != nil || c1.Token != "t1" {
		t.Fatalf("primeiro token: %+v %v", c1, err)
	}
	if c, _ := m.Credentials(ctx, false); c.Token != "t1" || count.Load() != 1 {
		t.Fatalf("deveria reaproveitar o token, veio %s (%d pedidos)", c.Token, count.Load())
	}
	clock = now.Add(61 * time.Minute) // falta menos de 1 h
	if c, _ := m.Credentials(ctx, false); c.Token != "t2" {
		t.Fatalf("deveria renovar perto da expiracao, veio %s", c.Token)
	}
	if c, _ := m.Credentials(ctx, true); c.Token != "t3" {
		t.Fatalf("force deveria renovar, veio %s", c.Token)
	}
}
