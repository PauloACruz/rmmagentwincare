import { formatDateTime } from '../lib/format';
import type { Ticket } from '../lib/types';
import { StatusBadge } from './StatusBadge';

interface Props {
  tickets: Ticket[];
  unread: ReadonlySet<number>;
  loading: boolean;
  error: string;
  onOpen: (id: number) => void;
  onNew: () => void;
}

export function TicketList({ tickets, unread, loading, error, onOpen, onNew }: Props) {
  return (
    <section className="panel">
      <div className="panel-head">
        <h2>Meus chamados</h2>
        <button type="button" className="btn btn-primary" onClick={onNew}>
          Abrir chamado
        </button>
      </div>
      {error && <p className="error" role="alert">{error}</p>}
      {loading && tickets.length === 0 && <p className="muted center">Carregando...</p>}
      {!loading && !error && tickets.length === 0 && (
        <div className="empty">
          <p>Você ainda não abriu nenhum chamado nesta máquina.</p>
          <p className="muted">Precisa de ajuda? Clique em "Abrir chamado".</p>
        </div>
      )}
      <ul className="ticket-list">
        {tickets.map((t) => {
          const isUnread = unread.has(t.id);
          return (
            <li key={t.id}>
              <button type="button" className={`ticket-row${isUnread ? ' unread' : ''}`} onClick={() => { onOpen(t.id); }}>
                <div className="ticket-row-top">
                  <span className="ticket-number">#{t.id}</span>
                  <StatusBadge status={t.status} />
                  {isUnread && (
                    <span className="new-dot" aria-label="Mensagem nova" title="Mensagem nova" />
                  )}
                </div>
                <div className="ticket-title">{t.title}</div>
                <div className="ticket-meta muted">
                  <span>{t.assignedToName ?? 'Aguardando técnico'}</span>
                  <span>{formatDateTime(t.updatedAt)}</span>
                </div>
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
