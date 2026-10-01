package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSelfServiceOptionsAndRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Tray a" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/tray/self-service":
			_, _ = io.WriteString(w, `{"enabled":true,"tasks":[{"module":"cleanup","key":"temp","label":"Limpar temporarios","description":"Apaga arquivos temporarios"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/tray/self-service/run":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["module"] != "cleanup" || body["key"] != "temp" {
				t.Errorf("corpo inesperado: %v %v", body, err)
			}
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("content-type: %s", r.Header.Get("Content-Type"))
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"runId":"run-1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/tray/self-service/runs/run-1":
			_, _ = io.WriteString(w, `{"runId":"run-1","status":"running","progress":40,"label":"cleanup.temp","messages":["Limpando","Quase la"]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &Client{Tokens: &fakeTokens{url: srv.URL, tokens: []string{"a"}}, HTTP: srv.Client()}
	ctx := context.Background()

	opts, err := c.SelfServiceOptions(ctx)
	if err != nil || !opts.Enabled || len(opts.Tasks) != 1 || opts.Tasks[0].Label != "Limpar temporarios" || opts.Tasks[0].Key != "temp" {
		t.Fatalf("opcoes: %+v %v", opts, err)
	}
	start, err := c.RunSelfService(ctx, "cleanup", "temp")
	if err != nil || start.RunID != "run-1" {
		t.Fatalf("run: %+v %v", start, err)
	}
	run, err := c.SelfServiceRun(ctx, start.RunID)
	if err != nil || run.Status != RunRunning || run.Progress != 40 || len(run.Messages) != 2 {
		t.Fatalf("estado: %+v %v", run, err)
	}
}

func TestSelfServiceRunErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   string
	}{
		{http.StatusConflict, `{"title":"Ja existe uma execucao","status":409,"code":"AGENT_BUSY"}`, "AGENT_BUSY"},
		{http.StatusForbidden, `{"title":"Esta acao nao esta liberada para o autoatendimento","status":403,"code":"FORBIDDEN"}`, "FORBIDDEN"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		_, err := (&Client{Tokens: &fakeTokens{url: srv.URL, tokens: []string{"a"}}, HTTP: srv.Client()}).RunSelfService(context.Background(), "m", "k")
		srv.Close()
		var apiErr *Error
		if !errors.As(err, &apiErr) || apiErr.Status != tc.status || apiErr.Code != tc.code {
			t.Fatalf("esperava %d %s, veio %v", tc.status, tc.code, err)
		}
	}
}
