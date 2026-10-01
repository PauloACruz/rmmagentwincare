package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/pauloacruz/rmmagentwincare/tray/internal/api"
	"github.com/pauloacruz/rmmagentwincare/tray/internal/ipc"
	"github.com/pauloacruz/rmmagentwincare/tray/internal/realtime"
)

// Eventos enviados ao frontend.
const (
	eventTicketMessage = "tray:ticketMessage"
	eventTicketChanged = "tray:ticketChanged"
	eventConnection    = "tray:connection"
	eventNavigate      = "tray:navigate"
	eventRefresh       = "tray:refresh"
)

const (
	msgUnavailable = "Serviço WinCare indisponível"
	msgOffline     = "Não foi possível falar com o servidor WinCare. Tente novamente em instantes."
	msgChatLocked  = "O chat será liberado quando um técnico assumir o seu chamado."
	maxImageBytes  = api.MaxAttachmentBytes
)

var statusNames = map[string]string{
	"new":          "Novo",
	"in_progress":  "Em atendimento",
	"waiting_user": "Aguardando usuário",
	"resolved":     "Resolvido",
	"closed":       "Fechado",
}

// SessionInfo e o que o cabecalho da interface precisa saber.
type SessionInfo struct {
	Hostname   string `json:"hostname"`
	Username   string `json:"username"`
	ClientName string `json:"clientName"`
	SiteName   string `json:"siteName"`
	Connected  bool   `json:"connected"`
	Realtime   bool   `json:"realtime"`
	Error      string `json:"error,omitempty"`
}

// CreateResult traz o chamado criado e o resultado da captura de tela.
type CreateResult struct {
	Ticket             api.Ticket `json:"ticket"`
	ScreenshotAttached bool       `json:"screenshotAttached"`
	ScreenshotError    string     `json:"screenshotError,omitempty"`
}

// TicketMessageEvent e o payload de tray:ticketMessage.
type TicketMessageEvent struct {
	TicketID int         `json:"ticketId"`
	Message  api.Message `json:"message"`
}

// TrayService e o unico servico exposto ao JavaScript. O token fica so aqui.
type TrayService struct {
	tokens   *ipc.Manager
	client   *api.Client
	rt       *realtime.Client
	notifier *notifier

	app    *application.App
	window *application.WebviewWindow
	ctx    context.Context

	mu       sync.Mutex
	realtime bool
	statuses map[int]string
}

func newTrayService(insecure bool) *TrayService {
	tokens := &ipc.Manager{Client: &ipc.Client{}}
	httpClient := api.NewHTTPClient(insecure)
	s := &TrayService{
		tokens:   tokens,
		client:   &api.Client{Tokens: tokens, HTTP: httpClient},
		statuses: map[int]string{},
	}
	s.rt = &realtime.Client{Tokens: tokens, HTTP: httpClient, Handler: s, Logf: log.Printf}
	return s
}

// ServiceName fixa o nome usado nas chamadas Call.ByName do frontend.
func (s *TrayService) ServiceName() string { return "TrayService" }

// ServiceStartup inicia a conexao em tempo real; ela tenta de novo sozinha se o agente ou a API cairem.
func (s *TrayService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.ctx = ctx
	go s.rt.Run(ctx)
	return nil
}

func (s *TrayService) friendly(err error) error {
	if err == nil {
		return nil
	}
	log.Printf("erro: %v", err)
	var apiErr *api.Error
	switch {
	case errors.Is(err, ipc.ErrUnavailable):
		return errors.New(msgUnavailable)
	case errors.As(err, &apiErr):
		switch {
		case apiErr.Code == "CHAT_LOCKED":
			return errors.New(msgChatLocked)
		case apiErr.Status == http.StatusNotFound:
			return errors.New("Chamado não encontrado.")
		case apiErr.Status == http.StatusRequestEntityTooLarge:
			return errors.New("O arquivo passa de 10 MB.")
		case apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden:
			return errors.New("Acesso negado pelo servidor WinCare.")
		case apiErr.Status == http.StatusBadRequest:
			if apiErr.Detail != "" {
				return errors.New(apiErr.Detail)
			}
			return errors.New("Dados inválidos. Revise os campos e tente novamente.")
		}
		return errors.New(msgOffline)
	case strings.HasPrefix(err.Error(), "agente:"):
		return errors.New(msgUnavailable)
	}
	return errors.New(msgOffline)
}

// Session obtem (ou renova) o token pelo agente e devolve os dados do cabecalho.
func (s *TrayService) Session(ctx context.Context) SessionInfo {
	info := SessionInfo{}
	creds, err := s.tokens.Credentials(ctx, false)
	if err != nil {
		info.Error = s.friendly(err).Error()
		return info
	}
	info.Hostname, info.Username = creds.Hostname, creds.Username
	me, err := s.client.Me(ctx)
	if err != nil {
		info.Error = s.friendly(err).Error()
		return info
	}
	info.Hostname, info.Username, info.ClientName, info.SiteName = me.Hostname, me.Username, me.ClientName, me.SiteName
	if creds.Username != "" {
		info.Username = creds.Username
	}
	info.Connected = true
	s.mu.Lock()
	info.Realtime = s.realtime
	s.mu.Unlock()
	return info
}

func (s *TrayService) remember(tickets ...api.Ticket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range tickets {
		s.statuses[t.ID] = t.Status
	}
}

// ListTickets devolve os chamados do usuario nesta maquina.
func (s *TrayService) ListTickets(ctx context.Context) ([]api.Ticket, error) {
	list, err := s.client.ListTickets(ctx)
	if err != nil {
		return nil, s.friendly(err)
	}
	s.remember(list...)
	return list, nil
}

// GetTicket devolve o chamado com a conversa.
func (s *TrayService) GetTicket(ctx context.Context, id int) (api.TicketDetail, error) {
	t, err := s.client.GetTicket(ctx, id)
	if err != nil {
		return api.TicketDetail{}, s.friendly(err)
	}
	s.remember(t.Ticket)
	return t, nil
}

// CreateTicket abre um chamado, com captura da tela principal quando pedido.
func (s *TrayService) CreateTicket(ctx context.Context, title, description string, includeScreenshot bool) (CreateResult, error) {
	title, description = strings.TrimSpace(title), strings.TrimSpace(description)
	if n := len([]rune(title)); n < 3 || n > 200 {
		return CreateResult{}, errors.New("O título deve ter entre 3 e 200 caracteres.")
	}
	if len([]rune(description)) > 20000 {
		return CreateResult{}, errors.New("A descrição deve ter no máximo 20000 caracteres.")
	}
	var result CreateResult
	var upload *api.Upload
	if includeScreenshot {
		shot, err := s.captureHidingWindow()
		if err != nil {
			log.Printf("captura de tela: %v", err)
			result.ScreenshotError = "Não foi possível capturar a tela; o chamado foi aberto sem a captura."
		} else {
			upload = shot
		}
	}
	t, err := s.client.CreateTicket(ctx, title, description, upload)
	if err != nil {
		return CreateResult{}, s.friendly(err)
	}
	s.remember(t)
	result.Ticket = t
	result.ScreenshotAttached = upload != nil
	return result, nil
}

// SendMessage envia uma mensagem do usuario no chat do chamado.
func (s *TrayService) SendMessage(ctx context.Context, ticketID int, body string) (api.Message, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return api.Message{}, errors.New("Escreva uma mensagem.")
	}
	if len([]rune(body)) > 20000 {
		return api.Message{}, errors.New("A mensagem deve ter no máximo 20000 caracteres.")
	}
	m, err := s.client.SendMessage(ctx, ticketID, body)
	if err != nil {
		return api.Message{}, s.friendly(err)
	}
	return m, nil
}

// Attachment devolve uma imagem anexada como data URL para exibir na interface.
func (s *TrayService) Attachment(ctx context.Context, ticketID int, attachmentID int64) (string, error) {
	data, contentType, err := s.client.Attachment(ctx, ticketID, attachmentID)
	if err != nil {
		return "", s.friendly(err)
	}
	switch contentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return "", errors.New("Este anexo não é uma imagem.")
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// CaptureScreenPreview captura a tela (escondendo a janela) e devolve uma miniatura em data URL.
func (s *TrayService) CaptureScreenPreview() (string, error) {
	shot, err := s.captureHidingWindow()
	if err != nil {
		log.Printf("captura de tela: %v", err)
		return "", errors.New("Não foi possível capturar a tela.")
	}
	return "data:" + shot.ContentType + ";base64," + base64.StdEncoding.EncodeToString(shot.Data), nil
}

func (s *TrayService) captureHidingWindow() (*api.Upload, error) {
	if s.window != nil {
		s.window.Hide()
		defer func() {
			s.window.Show()
			s.window.Focus()
		}()
	}
	time.Sleep(400 * time.Millisecond)
	img, err := captureScreen()
	if err != nil {
		return nil, err
	}
	return encodeScreenshot(img)
}

// encodeScreenshot gera PNG e, se passar de 10 MB, JPEG com qualidade decrescente.
func encodeScreenshot(img image.Image) (*api.Upload, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	if buf.Len() <= maxImageBytes {
		return &api.Upload{FileName: "captura.png", ContentType: "image/png", Data: buf.Bytes()}, nil
	}
	for _, q := range []int{85, 70, 50, 30} {
		buf.Reset()
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, err
		}
		if buf.Len() <= maxImageBytes {
			return &api.Upload{FileName: "captura.jpg", ContentType: "image/jpeg", Data: buf.Bytes()}, nil
		}
	}
	return nil, fmt.Errorf("captura maior que 10 MB mesmo em JPEG")
}

// TicketMessage recebe do hub uma mensagem nova (realtime.Handler).
func (s *TrayService) TicketMessage(ticketID int, msg api.Message) {
	log.Printf("realtime: mensagem %d no chamado #%d (%s)", msg.ID, ticketID, msg.AuthorType)
	s.emit(eventTicketMessage, TicketMessageEvent{TicketID: ticketID, Message: msg})
	if msg.AuthorType == "technician" && !s.windowFocused() {
		body := fmt.Sprintf("Nova mensagem no chamado #%d", ticketID)
		if msg.AuthorName != "" {
			body = fmt.Sprintf("%s respondeu o chamado #%d", msg.AuthorName, ticketID)
		}
		s.notifier.notify(fmt.Sprintf("msg-%d-%d", ticketID, msg.ID), body, excerpt(msg.Body), ticketID)
	}
}

// TicketChanged recebe do hub a mudanca de status ou atribuicao (realtime.Handler).
func (s *TrayService) TicketChanged(t api.Ticket) {
	s.mu.Lock()
	prev, known := s.statuses[t.ID]
	s.statuses[t.ID] = t.Status
	s.mu.Unlock()
	log.Printf("realtime: chamado #%d agora %s", t.ID, t.Status)
	s.emit(eventTicketChanged, t)
	if known && prev != t.Status {
		name := statusNames[t.Status]
		if name == "" {
			name = t.Status
		}
		s.notifier.notify(fmt.Sprintf("status-%d-%s", t.ID, t.Status),
			fmt.Sprintf("Chamado #%d: %s", t.ID, name), t.Title, t.ID)
	}
}

// ConnectionChanged recebe o estado da conexao com o hub (realtime.Handler).
func (s *TrayService) ConnectionChanged(connected bool) {
	s.mu.Lock()
	s.realtime = connected
	s.mu.Unlock()
	log.Printf("realtime: conectado=%v", connected)
	s.emit(eventConnection, map[string]bool{"realtime": connected})
	if connected {
		// Recarrega o estado conhecido para nao perder eventos enviados enquanto estava desconectado.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if list, err := s.client.ListTickets(ctx); err == nil {
				s.remember(list...)
			}
			s.emit(eventRefresh, nil)
		}()
	}
}

func (s *TrayService) emit(name string, data any) {
	if s.app != nil {
		s.app.Event.Emit(name, data)
	}
}

func (s *TrayService) windowFocused() bool {
	return s.window != nil && s.window.IsVisible() && s.window.IsFocused()
}

func excerpt(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) > 140 {
		return string(r[:137]) + "..."
	}
	return text
}
