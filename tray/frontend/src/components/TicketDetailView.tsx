import { useEffect, useState } from 'react';
import { errorMessage, useBackend } from '../lib/backend';
import { formatDateTime } from '../lib/format';
import { markSeen } from '../lib/seen';
import type { Message, TicketDetail } from '../lib/types';
import { AttachmentImage } from './AttachmentImage';
import { ChatThread } from './ChatThread';
import { Composer } from './Composer';
import { StatusBadge } from './StatusBadge';

interface Props {
  ticketId: number;
  notice: string;
  onBack: () => void;
}

function addMessage(list: Message[], m: Message): Message[] {
  return list.some((x) => x.id === m.id) ? list : [...list, m];
}

export function TicketDetailView({ ticketId, notice, onBack }: Props) {
  const backend = useBackend();
  const [ticket, setTicket] = useState<TicketDetail | null>(null);
  const [error, setError] = useState('');

  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let alive = true;
    backend.getTicket(ticketId).then(
      (t) => {
        if (!alive) return;
        setTicket(t);
        setError('');
      },
      (e: unknown) => {
        if (alive) setError(errorMessage(e));
      },
    );
    return () => {
      alive = false;
    };
  }, [backend, ticketId, reloadKey]);

  useEffect(() => {
    const offMsg = backend.onTicketMessage((e) => {
      if (e.ticketId !== ticketId) return;
      setTicket((t) => (t ? { ...t, messages: addMessage(t.messages, e.message), lastMessageAt: e.message.createdAt } : t));
    });
    const offChanged = backend.onTicketChanged((s) => {
      if (s.id !== ticketId) return;
      setTicket((t) => (t ? { ...t, ...s } : t));
    });
    const offRefresh = backend.onRefresh(() => { setReloadKey((n) => n + 1); });
    return () => {
      offMsg();
      offChanged();
      offRefresh();
    };
  }, [backend, ticketId]);

  const lastAt = ticket?.messages.at(-1)?.createdAt;
  useEffect(() => {
    if (lastAt) markSeen(ticketId, lastAt);
  }, [ticketId, lastAt]);

  async function send(body: string) {
    const m = await backend.sendMessage(ticketId, body);
    setTicket((t) => (t ? { ...t, messages: addMessage(t.messages, m) } : t));
  }

  const images = ticket?.attachments.filter((a) => a.contentType.startsWith('image/')) ?? [];

  return (
    <section className="panel detail">
      <div className="panel-head">
        <button type="button" className="btn btn-link" onClick={onBack}>
          ‹ Voltar
        </button>
        <h2>Chamado #{ticketId}</h2>
      </div>
      {notice && <p className="notice" role="status">{notice}</p>}
      {error && <p className="error" role="alert">{error}</p>}
      {!ticket && !error && <p className="muted center">Carregando...</p>}
      {ticket && (
        <>
          <div className="detail-card">
            <div className="ticket-row-top">
              <StatusBadge status={ticket.status} />
              <span className="muted small">Aberto em {formatDateTime(ticket.createdAt)}</span>
            </div>
            <h3 className="detail-title">{ticket.title}</h3>
            <p className="muted small">Técnico: {ticket.assignedToName ?? 'Aguardando técnico'}</p>
            {ticket.description && <p className="detail-description">{ticket.description}</p>}
            {images.map((a) => (
              <AttachmentImage key={a.id} ticketId={ticket.id} attachment={a} />
            ))}
          </div>
          <ChatThread ticketId={ticket.id} messages={ticket.messages} />
          <Composer enabled={ticket.chatEnabled} onSend={send} />
        </>
      )}
    </section>
  );
}
