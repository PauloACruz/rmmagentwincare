import { Call, Events } from '@wailsio/runtime';
import type {
  Backend,
  CreateResult,
  Message,
  NavigateEvent,
  SelfServiceEvent,
  SelfServiceOptions,
  SelfServiceRun,
  SelfServiceStart,
  SelfServiceTask,
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
  selfServiceOptions: async () => {
    const o = (await call('SelfServiceOptions')) as Omit<SelfServiceOptions, 'tasks'> & { tasks: SelfServiceTask[] | null };
    return { enabled: o.enabled, tasks: o.tasks ?? [] };
  },
  runSelfService: (module, key) => call('RunSelfService', module, key) as Promise<SelfServiceStart>,
  selfServiceRun: async (runId) => {
    const r = (await call('SelfServiceRun', runId)) as Omit<SelfServiceRun, 'messages'> & { messages: string[] | null };
    return { ...r, messages: r.messages ?? [] };
  },
  onSelfService: (cb) => on('tray:selfService', (d) => { cb(d as SelfServiceEvent); }),
};
