import { useEffect, useState } from 'react';
import { errorMessage, useBackend } from './lib/backend';
import { hasUnread } from './lib/seen';
import type { SessionInfo, Ticket } from './lib/types';
import { Header } from './components/Header';
import { NewTicketForm } from './components/NewTicketForm';
import { TicketDetailView } from './components/TicketDetailView';
import { TicketList } from './components/TicketList';
import { Unavailable } from './components/Unavailable';

type View = { kind: 'list' } | { kind: 'new' } | { kind: 'ticket'; id: number; notice: string };

const RETRY_MS = 30_000;

function upsert(list: Ticket[], t: Ticket): Ticket[] {
  const rest = list.filter((x) => x.id !== t.id);
  return [t, ...rest].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
}

function offline(error: string): SessionInfo {
  return { hostname: '', username: '', clientName: '', siteName: '', connected: false, realtime: false, error };
}

export function App() {
  const backend = useBackend();
  const [session, setSession] = useState<SessionInfo | null>(null);
  const [sessionKey, setSessionKey] = useState(0);
  const [checking, setChecking] = useState(false);
  const [view, setView] = useState<View>({ kind: 'list' });
  const [tickets, setTickets] = useState<Ticket[]>([]);
  const [ticketsKey, setTicketsKey] = useState(0);
  const [loaded, setLoaded] = useState(false);
  const [listError, setListError] = useState('');

  const connected = session?.connected === true;

  useEffect(() => {
    let alive = true;
    backend.session().then(
      (s) => {
        if (alive) setSession(s);
      },
      (e: unknown) => {
        if (alive) setSession(offline(errorMessage(e)));
      },
    ).finally(() => {
      if (alive) setChecking(false);
    });
    return () => {
      alive = false;
    };
  }, [backend, sessionKey]);

  // Sem o servico do agente, tenta de novo a cada 30 s.
  useEffect(() => {
    if (connected) return;
    const timer = setInterval(() => { setSessionKey((n) => n + 1); }, RETRY_MS);
    return () => { clearInterval(timer); };
  }, [connected]);

  useEffect(() => {
    if (!connected) return;
    let alive = true;
    backend.listTickets().then(
      (list) => {
        if (!alive) return;
        setTickets(list);
        setListError('');
        setLoaded(true);
      },
      (e: unknown) => {
        if (!alive) return;
        setListError(errorMessage(e));
        setLoaded(true);
      },
    );
    return () => {
      alive = false;
    };
  }, [backend, connected, ticketsKey]);

  useEffect(() => {
    const offs = [
      backend.onTicketChanged((t) => { setTickets((list) => upsert(list, t)); }),
      backend.onTicketMessage((e) => {
        setTickets((list) =>
          list.map((t) => (t.id === e.ticketId ? { ...t, lastMessageAt: e.message.createdAt, updatedAt: e.message.createdAt } : t)),
        );
      }),
      backend.onConnection((realtime) => { setSession((s) => (s ? { ...s, realtime } : s)); }),
      backend.onRefresh(() => { setTicketsKey((n) => n + 1); }),
      backend.onNavigate((e) => {
        if (e.view === 'new') setView({ kind: 'new' });
        else if (e.view === 'ticket' && e.id > 0) setView({ kind: 'ticket', id: e.id, notice: '' });
        else setView({ kind: 'list' });
      }),
    ];
    return () => { offs.forEach((off) => { off(); }); };
  }, [backend]);

  const openId = view.kind === 'ticket' ? view.id : -1;
  const unread = new Set(tickets.filter((t) => t.id !== openId && hasUnread(t)).map((t) => t.id));

  async function create(title: string, description: string, includeScreenshot: boolean) {
    const result = await backend.createTicket(title, description, includeScreenshot);
    setTickets((list) => upsert(list, result.ticket));
    setView({ kind: 'ticket', id: result.ticket.id, notice: result.screenshotError ?? '' });
  }

  function retry() {
    setChecking(true);
    setSessionKey((n) => n + 1);
  }

  let body;
  if (!session) {
    body = <p className="muted center">Conectando ao WinCare...</p>;
  } else if (!connected) {
    body = <Unavailable message={session.error ?? ''} onRetry={retry} retrying={checking} />;
  } else if (view.kind === 'new') {
    body = <NewTicketForm onSubmit={create} onCancel={() => { setView({ kind: 'list' }); }} />;
  } else if (view.kind === 'ticket') {
    body = (
      <TicketDetailView key={view.id} ticketId={view.id} notice={view.notice} onBack={() => { setView({ kind: 'list' }); }} />
    );
  } else {
    body = (
      <TicketList
        tickets={tickets}
        unread={unread}
        loading={!loaded}
        error={listError}
        onOpen={(id) => { setView({ kind: 'ticket', id, notice: '' }); }}
        onNew={() => { setView({ kind: 'new' }); }}
      />
    );
  }

  return (
    <div className="app">
      <Header session={session} />
      <main className="main">{body}</main>
    </div>
  );
}
