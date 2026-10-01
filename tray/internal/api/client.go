package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/pauloacruz/rmmagentwincare/tray/internal/ipc"
)

// MaxAttachmentBytes e o limite de anexo aceito pela API.
const MaxAttachmentBytes = 10 << 20

// Error e uma resposta de erro da API (ProblemDetails com "code").
type Error struct {
	Status int
	Code   string
	Title  string
	Detail string
}

func (e *Error) Error() string {
	msg := e.Detail
	if msg == "" {
		msg = e.Title
	}
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("API %d: %s", e.Status, msg)
}

// IsStatus diz se err e um *Error com o status informado.
func IsStatus(err error, status int) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Status == status
}

// Client chama a API com "Authorization: Tray <token>". Ao receber 401, pede um token novo ao
// agente e repete a chamada uma vez.
type Client struct {
	Tokens ipc.Source
	HTTP   *http.Client
}

// NewHTTPClient cria o cliente HTTP. insecure desliga a validacao TLS e so deve ser usado em testes.
func NewHTTPClient(insecure bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // somente com WINCARE_TRAY_INSECURE=1
	}
	return &http.Client{Transport: transport, Timeout: 60 * time.Second}
}

type requestBody struct {
	contentType string
	data        []byte
}

func (c *Client) do(ctx context.Context, method, path string, body *requestBody, out any) error {
	data, _, err := c.raw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("resposta invalida da API: %w", err)
	}
	return nil
}

func (c *Client) raw(ctx context.Context, method, path string, body *requestBody) ([]byte, string, error) {
	for attempt := 0; ; attempt++ {
		creds, err := c.Tokens.Credentials(ctx, attempt > 0)
		if err != nil {
			return nil, "", err
		}
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body.data)
		}
		req, err := http.NewRequestWithContext(ctx, method, creds.APIURL+path, reader)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Authorization", "Tray "+creds.Token)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", body.contentType)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, "", err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, MaxAttachmentBytes+1<<20))
		resp.Body.Close()
		if err != nil {
			return nil, "", err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			continue
		}
		if resp.StatusCode >= 300 {
			return nil, "", parseError(resp.StatusCode, data)
		}
		return data, resp.Header.Get("Content-Type"), nil
	}
}

func parseError(status int, data []byte) error {
	e := &Error{Status: status}
	var problem struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Code   string `json:"code"`
	}
	if json.Unmarshal(data, &problem) == nil {
		e.Title, e.Detail, e.Code = problem.Title, problem.Detail, problem.Code
	}
	return e
}

// Me devolve a maquina, o cliente e o site do token.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var me Me
	err := c.do(ctx, http.MethodGet, "/api/tray/me", nil, &me)
	return me, err
}

// ListTickets devolve os chamados do usuario nesta maquina.
func (c *Client) ListTickets(ctx context.Context) ([]Ticket, error) {
	tickets := []Ticket{}
	err := c.do(ctx, http.MethodGet, "/api/tray/tickets", nil, &tickets)
	return tickets, err
}

// GetTicket devolve o chamado com a conversa.
func (c *Client) GetTicket(ctx context.Context, id int) (TicketDetail, error) {
	var t TicketDetail
	err := c.do(ctx, http.MethodGet, "/api/tray/tickets/"+strconv.Itoa(id), nil, &t)
	if t.Messages == nil {
		t.Messages = []Message{}
	}
	if t.Attachments == nil {
		t.Attachments = []Attachment{}
	}
	return t, err
}

// CreateTicket abre um chamado; screenshot e opcional.
func (c *Client) CreateTicket(ctx context.Context, title, description string, screenshot *Upload) (Ticket, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("title", title); err != nil {
		return Ticket{}, err
	}
	if err := w.WriteField("description", description); err != nil {
		return Ticket{}, err
	}
	if screenshot != nil {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="screenshot"; filename=%q`, screenshot.FileName))
		h.Set("Content-Type", screenshot.ContentType)
		part, err := w.CreatePart(h)
		if err != nil {
			return Ticket{}, err
		}
		if _, err := part.Write(screenshot.Data); err != nil {
			return Ticket{}, err
		}
	}
	if err := w.Close(); err != nil {
		return Ticket{}, err
	}
	var t Ticket
	err := c.do(ctx, http.MethodPost, "/api/tray/tickets", &requestBody{contentType: w.FormDataContentType(), data: buf.Bytes()}, &t)
	return t, err
}

// SendMessage envia uma mensagem do usuario. A API devolve 409 CHAT_LOCKED sem tecnico atribuido.
func (c *Client) SendMessage(ctx context.Context, ticketID int, body string) (Message, error) {
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return Message{}, err
	}
	var m Message
	err = c.do(ctx, http.MethodPost, "/api/tray/tickets/"+strconv.Itoa(ticketID)+"/messages",
		&requestBody{contentType: "application/json", data: payload}, &m)
	return m, err
}

// Attachment baixa um anexo e devolve o conteudo e o tipo informado pelo servidor.
func (c *Client) Attachment(ctx context.Context, ticketID int, attachmentID int64) ([]byte, string, error) {
	data, contentType, err := c.raw(ctx, http.MethodGet,
		"/api/tray/tickets/"+strconv.Itoa(ticketID)+"/attachments/"+strconv.FormatInt(attachmentID, 10), nil)
	if err != nil {
		return nil, "", err
	}
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return data, strings.TrimSpace(contentType), nil
}
