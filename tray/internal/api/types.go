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
