import type { Backend, Message, Ticket, TicketDetail, TicketMessageEvent } from './types';

// Simulador usado com "npm run dev:mock" para desenvolver e validar a interface fora do Wails.
type Listener<T> = (data: T) => void;

const now = () => new Date().toISOString();
const msgListeners = new Set<Listener<TicketMessageEvent>>();
const changeListeners = new Set<Listener<Ticket>>();

let nextMessageId = 100;
const tickets: TicketDetail[] = [
  {
    id: 41,
    title: 'Impressora do financeiro não imprime',
    status: 'in_progress',
    priority: 'medium',
    createdAt: '2026-09-30T13:10:00Z',
    updatedAt: '2026-09-30T14:00:00Z',
    assignedToName: 'Ana Souza',
    chatEnabled: true,
    lastMessageAt: '2026-09-30T14:00:00Z',
    description: 'Quando mando imprimir aparece "erro de spool".',
    attachments: [],
    messages: [
      { id: 1, authorType: 'system', authorName: 'Sistema', body: 'Admin atribuiu o chamado a Ana Souza', createdAt: '2026-09-30T13:30:00Z', attachments: [] },
      { id: 2, authorType: 'technician', authorName: 'Ana Souza', body: 'Olá! Pode reiniciar a impressora e me dizer se o erro continua?', createdAt: '2026-09-30T13:40:00Z', attachments: [] },
      { id: 3, authorType: 'requester', authorName: 'maria', body: 'Reiniciei e continua igual.', createdAt: '2026-09-30T14:00:00Z', attachments: [] },
    ],
  },
  {
    id: 38,
    title: 'Instalar o leitor de PDF',
    status: 'resolved',
    priority: 'low',
    createdAt: '2026-09-20T10:00:00Z',
    updatedAt: '2026-09-21T09:00:00Z',
    assignedToName: 'Carlos Lima',
    chatEnabled: true,
    lastMessageAt: '2026-09-21T09:00:00Z',
    description: '',
    attachments: [],
    messages: [],
  },
];

function summary(t: TicketDetail): Ticket {
  return {
    id: t.id,
    title: t.title,
    status: t.status,
    priority: t.priority,
    createdAt: t.createdAt,
    updatedAt: t.updatedAt,
    assignedToName: t.assignedToName,
    chatEnabled: t.chatEnabled,
    lastMessageAt: t.lastMessageAt,
  };
}

function find(id: number): TicketDetail {
  const t = tickets.find((x) => x.id === id);
  if (!t) throw new Error('Chamado não encontrado.');
  return t;
}

function push(t: TicketDetail, m: Message) {
  t.messages.push(m);
  t.lastMessageAt = m.createdAt;
  msgListeners.forEach((l) => { l({ ticketId: t.id, message: m }); });
}

const delay = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

// Imagem 1x1 cinza usada como captura simulada.
const PIXEL =
  'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mN8+P9/PQAJYwPBpqbiOAAAAABJRU5ErkJggg==';

export const mockBackend: Backend = {
  async session() {
    await delay(150);
    return { hostname: 'PC-FINANCEIRO-01', username: 'maria', clientName: 'Contoso', siteName: 'Matriz', connected: true, realtime: true };
  },
  async listTickets() {
    await delay(150);
    return tickets.map(summary).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
  },
  async getTicket(id) {
    await delay(100);
    const t = find(id);
    return { ...t, messages: [...t.messages], attachments: [...t.attachments] };
  },
  async createTicket(title, description, includeScreenshot) {
    await delay(600);
    const t: TicketDetail = {
      id: Math.max(...tickets.map((x) => x.id)) + 1,
      title,
      description,
      status: 'new',
      priority: 'medium',
      createdAt: now(),
      updatedAt: now(),
      assignedToName: null,
      chatEnabled: false,
      lastMessageAt: null,
      messages: [],
      attachments: includeScreenshot ? [{ id: 900, fileName: 'captura.png', contentType: 'image/png', size: 68 }] : [],
    };
    tickets.push(t);
    // Simula um tecnico assumindo e respondendo.
    setTimeout(() => {
      t.assignedToName = 'Ana Souza';
      t.status = 'in_progress';
      t.chatEnabled = true;
      push(t, { id: nextMessageId++, authorType: 'system', authorName: 'Sistema', body: 'Admin atribuiu o chamado a Ana Souza', createdAt: now(), attachments: [] });
      changeListeners.forEach((l) => { l(summary(t)); });
      push(t, { id: nextMessageId++, authorType: 'technician', authorName: 'Ana Souza', body: 'Olá! Já estou olhando o seu chamado.', createdAt: now(), attachments: [] });
    }, 3000);
    return { ticket: summary(t), screenshotAttached: includeScreenshot };
  },
  async sendMessage(ticketId, body) {
    await delay(150);
    const t = find(ticketId);
    if (!t.chatEnabled) throw new Error('O chat será liberado quando um técnico assumir o seu chamado.');
    const m: Message = { id: nextMessageId++, authorType: 'requester', authorName: 'maria', body, createdAt: now(), attachments: [] };
    push(t, m);
    return m;
  },
  attachment() {
    return Promise.resolve(PIXEL);
  },
  onTicketMessage(cb) {
    msgListeners.add(cb);
    return () => { msgListeners.delete(cb); };
  },
  onTicketChanged(cb) {
    changeListeners.add(cb);
    return () => { changeListeners.delete(cb); };
  },
  onConnection() {
    return () => undefined;
  },
  onNavigate() {
    return () => undefined;
  },
  onRefresh() {
    return () => undefined;
  },
};
