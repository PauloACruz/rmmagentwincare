package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pauloacruz/rmmagentwincare/tray/internal/ipc"
)

type fakeTokens struct {
	mu     sync.Mutex
	url    string
	tokens []string
	forced int
	calls  int
}

func (f *fakeTokens) Credentials(_ context.Context, force bool) (ipc.Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if force {
		f.forced++
	}
	tok := f.tokens[f.forced]
	f.calls++
	return ipc.Credentials{Token: tok, APIURL: f.url}, nil
}

func TestRenewsTokenOn401(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		seen = append(seen, auth)
		if auth != "Tray novo" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":7,"title":"Impressora","status":"new","priority":"medium","createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-01T10:00:00Z","assignedToName":null,"chatEnabled":false,"lastMessageAt":null}]`)
	}))
	defer srv.Close()

	tokens := &fakeTokens{url: srv.URL, tokens: []string{"velho", "novo"}}
	c := &Client{Tokens: tokens, HTTP: srv.Client()}
	list, err := c.ListTickets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != 7 || list[0].Status != "new" {
		t.Fatalf("lista inesperada: %+v", list)
	}
	if strings.Join(seen, ",") != "Tray velho,Tray novo" || tokens.forced != 1 {
		t.Fatalf("esperava uma renovacao forcada: %v forced=%d", seen, tokens.forced)
	}
}

func TestPersistent401IsReturned(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	tokens := &fakeTokens{url: srv.URL, tokens: []string{"a", "b", "c"}}
	_, err := (&Client{Tokens: tokens, HTTP: srv.Client()}).Me(context.Background())
	if !IsStatus(err, http.StatusUnauthorized) || calls != 2 {
		t.Fatalf("esperava 401 depois de uma nova tentativa, veio %v (%d chamadas)", err, calls)
	}
}

func TestChatLockedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"title":"O chat e liberado quando um tecnico assume o chamado","status":409,"code":"CHAT_LOCKED"}`)
	}))
	defer srv.Close()
	_, err := (&Client{Tokens: &fakeTokens{url: srv.URL, tokens: []string{"a"}}, HTTP: srv.Client()}).SendMessage(context.Background(), 1, "oi")
	var apiErr *Error
	if !IsStatus(err, http.StatusConflict) || !errors.As(err, &apiErr) || apiErr.Code != "CHAT_LOCKED" {
		t.Fatalf("esperava CHAT_LOCKED, veio %v", err)
	}
}
