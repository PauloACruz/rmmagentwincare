package main

import (
	"context"
	"log"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// notifier envolve o servico de notificacoes do Wails. Se o sistema nao oferecer notificacoes
// (por exemplo, Linux sem barramento D-Bus de sessao), o app continua funcionando sem elas.
type notifier struct {
	ns *notifications.NotificationService

	mu        sync.Mutex
	available bool
	onClick   func(ticketID int)
}

func newNotifier() *notifier {
	return &notifier{ns: notifications.New()}
}

func (n *notifier) ServiceName() string { return "WinCareNotifier" }

func (n *notifier) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if err := n.ns.ServiceStartup(ctx, options); err != nil {
		log.Printf("notificacoes indisponiveis: %v", err)
		return nil
	}
	if ok, err := n.ns.RequestNotificationAuthorization(); err != nil || !ok {
		log.Printf("notificacoes sem autorizacao: %v", err)
	}
	n.ns.OnNotificationResponse(func(result notifications.NotificationResult) {
		if result.Error != nil {
			return
		}
		n.mu.Lock()
		cb := n.onClick
		n.mu.Unlock()
		id := 0
		switch v := result.Response.UserInfo["ticketId"].(type) {
		case float64:
			id = int(v)
		case int:
			id = v
		}
		if cb != nil {
			cb(id)
		}
	})
	n.mu.Lock()
	n.available = true
	n.mu.Unlock()
	return nil
}

func (n *notifier) ServiceShutdown() error {
	n.mu.Lock()
	ok := n.available
	n.mu.Unlock()
	if ok {
		return n.ns.ServiceShutdown()
	}
	return nil
}

func (n *notifier) notify(id, subtitle, body string, ticketID int) {
	if n == nil {
		return
	}
	n.mu.Lock()
	ok := n.available
	n.mu.Unlock()
	if !ok {
		log.Printf("notificacao (sem servico): %s - %s", subtitle, body)
		return
	}
	text := subtitle
	if body != "" {
		text = subtitle + "\n" + body
	}
	err := n.ns.SendNotification(notifications.NotificationOptions{
		ID:    id,
		Title: "WinCare",
		Body:  text,
		Data:  map[string]any{"ticketId": ticketID},
	})
	if err != nil {
		log.Printf("notificacao: %v", err)
	}
}
