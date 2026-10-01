// Package api e o cliente HTTP das rotas /api/tray/* da plataforma WinCare.
package api

import "time"

// Me identifica a maquina e o usuario do token.
type Me struct {
	Hostname   string `json:"hostname"`
	Username   string `json:"username"`
	ClientName string `json:"clientName"`
	SiteName   string `json:"siteName"`
}

// Attachment descreve um anexo de chamado ou de mensagem.
type Attachment struct {
	ID          int64  `json:"id"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
}

// Ticket e o resumo de chamado visto pelo usuario final (TrayTicket).
type Ticket struct {
	ID             int        `json:"id"`
	Title          string     `json:"title"`
	Status         string     `json:"status"`
	Priority       string     `json:"priority"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	AssignedToName *string    `json:"assignedToName"`
	ChatEnabled    bool       `json:"chatEnabled"`
	LastMessageAt  *time.Time `json:"lastMessageAt"`
}

// Message e uma mensagem publica do chamado (TrayMessage).
type Message struct {
	ID          int64        `json:"id"`
	AuthorType  string       `json:"authorType"`
	AuthorName  string       `json:"authorName"`
	Body        string       `json:"body"`
	CreatedAt   time.Time    `json:"createdAt"`
	Attachments []Attachment `json:"attachments"`
}

// TicketDetail e o chamado com descricao, conversa e anexos sem mensagem (como a captura de tela).
type TicketDetail struct {
	Ticket
	Description string       `json:"description"`
	Messages    []Message    `json:"messages"`
	Attachments []Attachment `json:"attachments"`
}

// Upload e um arquivo enviado no multipart.
type Upload struct {
	FileName    string
	ContentType string
	Data        []byte
}

// Estados finais de uma execucao do autoatendimento (WinCareRunStatus no servidor).
const (
	RunRunning   = "running"
	RunOK        = "ok"
	RunWarning   = "warning"
	RunError     = "error"
	RunCancelled = "cancelled"
	RunTimeout   = "timeout"
)

// IsFinalRunStatus diz se a execucao ja terminou.
func IsFinalRunStatus(status string) bool {
	switch status {
	case RunOK, RunWarning, RunError, RunCancelled, RunTimeout:
		return true
	}
	return false
}

// SelfServiceTask e uma acao liberada pelo tecnico para o usuario executar sozinho.
type SelfServiceTask struct {
	Module      string `json:"module"`
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// SelfServiceOptions e a resposta de GET /api/tray/self-service.
type SelfServiceOptions struct {
	Enabled bool              `json:"enabled"`
	Tasks   []SelfServiceTask `json:"tasks"`
}

// SelfServiceStart e a resposta 202 de POST /api/tray/self-service/run.
type SelfServiceStart struct {
	RunID string `json:"runId"`
}

// SelfServiceRun e o estado de uma execucao (GET /api/tray/self-service/runs/{runId}).
type SelfServiceRun struct {
	RunID    string   `json:"runId"`
	Status   string   `json:"status"`
	Progress int      `json:"progress"`
	Label    string   `json:"label"`
	Messages []string `json:"messages"`
}

// SelfServiceChange e o argumento do evento selfServiceChanged do hub.
type SelfServiceChange struct {
	RunID    string `json:"runId"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Message  string `json:"message"`
}
