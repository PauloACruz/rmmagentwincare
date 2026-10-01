import { Call, Events } from '@wailsio/runtime';
import type {
  Backend,
  CreateResult,
  Message,
  NavigateEvent,
  SessionInfo,
  Ticket,
  TicketDetail,
  TicketMessageEvent,
} from './types';

// Os metodos do servico Go sao chamados pelo nome completo (pacote.Tipo.Metodo), sem bindings gerados.
const SERVICE = 'main.TrayService.';

async function call(method: string, ...args: unknown[]): Promise<unknown> {
  const result: unknown = await Call.ByName(SERVICE + method, ...args);
  return result;
}

function on(name: string, cb: (data: unknown) => void): () => void {
  return Events.On(name, (ev) => {
    cb(ev.data);
  });
}

export const wailsBackend: Backend = {
  session: () => call('Session') as Promise<SessionInfo>,
  listTickets: async () => ((await call('ListTickets')) as Ticket[] | null) ?? [],
  getTicket: (id) => call('GetTicket', id) as Promise<TicketDetail>,
  createTicket: (title, description, includeScreenshot) =>
    call('CreateTicket', title, description, includeScreenshot) as Promise<CreateResult>,
  sendMessage: (ticketId, body) => call('SendMessage', ticketId, body) as Promise<Message>,
  attachment: (ticketId, attachmentId) => call('Attachment', ticketId, attachmentId) as Promise<string>,
  onTicketMessage: (cb) => on('tray:ticketMessage', (d) => { cb(d as TicketMessageEvent); }),
  onTicketChanged: (cb) => on('tray:ticketChanged', (d) => { cb(d as Ticket); }),
  onConnection: (cb) => on('tray:connection', (d) => { cb((d as { realtime: boolean }).realtime); }),
  onNavigate: (cb) => on('tray:navigate', (d) => { cb(d as NavigateEvent); }),
  onRefresh: (cb) => on('tray:refresh', () => { cb(); }),
};
