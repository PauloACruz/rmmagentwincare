package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/pauloacruz/rmmagentwincare/tray/internal/api"
)

const (
	msgAgentBusy         = "Já existe uma manutenção em andamento neste computador. Tente novamente em alguns minutos."
	msgSelfServiceDenied = "Esta ação não está liberada pelo suporte."
)

// selfServiceResult e o texto da notificacao quando a execucao termina.
var selfServiceResult = map[string]string{
	api.RunOK:        "Pronto! A ação foi concluída.",
	api.RunWarning:   "A ação foi concluída com avisos.",
	api.RunError:     "A ação não conseguiu terminar. Se o problema continuar, abra um chamado.",
	api.RunCancelled: "A ação foi cancelada.",
	api.RunTimeout:   "A ação demorou mais que o esperado e foi interrompida.",
}

func (s *TrayService) friendlySelfService(err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == "AGENT_BUSY" || apiErr.Status == http.StatusConflict:
			log.Printf("erro: %v", err)
			return errors.New(msgAgentBusy)
		case apiErr.Status == http.StatusForbidden:
			log.Printf("erro: %v", err)
			return errors.New(msgSelfServiceDenied)
		case apiErr.Status == http.StatusNotFound:
			log.Printf("erro: %v", err)
			return errors.New("Execução não encontrada.")
		case apiErr.Status == http.StatusGatewayTimeout:
			log.Printf("erro: %v", err)
			return errors.New("O computador não respondeu a tempo. Tente novamente em instantes.")
		}
	}
	return s.friendly(err)
}

// SelfServiceOptions devolve se o autoatendimento esta liberado e as acoes disponiveis.
func (s *TrayService) SelfServiceOptions(ctx context.Context) (api.SelfServiceOptions, error) {
	o, err := s.client.SelfServiceOptions(ctx)
	if err != nil {
		return api.SelfServiceOptions{Tasks: []api.SelfServiceTask{}}, s.friendlySelfService(err)
	}
	s.mu.Lock()
	s.selfTasks = o.Tasks
	s.mu.Unlock()
	return o, nil
}

// RunSelfService inicia uma acao liberada e devolve o runId para acompanhar o progresso.
func (s *TrayService) RunSelfService(ctx context.Context, module, key string) (api.SelfServiceStart, error) {
	module, key = strings.TrimSpace(module), strings.TrimSpace(key)
	if module == "" || key == "" || len(module) > 64 || len(key) > 64 {
		return api.SelfServiceStart{}, errors.New(msgSelfServiceDenied)
	}
	start, err := s.client.RunSelfService(ctx, module, key)
	if err != nil {
		return api.SelfServiceStart{}, s.friendlySelfService(err)
	}
	s.mu.Lock()
	if s.selfRuns == nil {
		s.selfRuns = map[string]string{}
	}
	label := key
	for _, t := range s.selfTasks {
		if t.Module == module && t.Key == key && t.Label != "" {
			label = t.Label
		}
	}
	s.selfRuns[start.RunID] = label
	s.mu.Unlock()
	log.Printf("autoatendimento: %s.%s iniciado (%s)", module, key, start.RunID)
	return start, nil
}

// SelfServiceRun devolve o estado atual de uma execucao (usado ao abrir a tela e se o tempo real cair).
func (s *TrayService) SelfServiceRun(ctx context.Context, runID string) (api.SelfServiceRun, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return api.SelfServiceRun{}, errors.New("Execução não encontrada.")
	}
	r, err := s.client.SelfServiceRun(ctx, runID)
	if err != nil {
		return api.SelfServiceRun{}, s.friendlySelfService(err)
	}
	return r, nil
}

// SelfServiceChanged recebe do hub o progresso de uma execucao (realtime.Handler).
func (s *TrayService) SelfServiceChanged(change api.SelfServiceChange) {
	log.Printf("realtime: autoatendimento %s %s %d%%", change.RunID, change.Status, change.Progress)
	s.emit(eventSelfService, change)
	if !api.IsFinalRunStatus(change.Status) {
		return
	}
	s.mu.Lock()
	if s.notified == nil {
		s.notified = map[string]bool{}
	}
	already := s.notified[change.RunID]
	s.notified[change.RunID] = true
	label := s.selfRuns[change.RunID]
	delete(s.selfRuns, change.RunID)
	s.mu.Unlock()
	if already {
		return
	}
	body := label
	if body == "" {
		body = excerpt(change.Message)
	}
	s.notifier.notify(fmt.Sprintf("selfservice-%s", change.RunID), selfServiceResult[change.Status], body, 0)
}
