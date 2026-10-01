export type TicketStatus = 'new' | 'in_progress' | 'waiting_user' | 'resolved' | 'closed';
export type AuthorType = 'technician' | 'requester' | 'system';

export interface Attachment {
  id: number;
  fileName: string;
  contentType: string;
  size: number;
}

export interface Ticket {
  id: number;
  title: string;
  status: TicketStatus;
  priority: string;
  createdAt: string;
  updatedAt: string;
  assignedToName: string | null;
  chatEnabled: boolean;
  lastMessageAt: string | null;
}

export interface Message {
  id: number;
  authorType: AuthorType;
  authorName: string;
  body: string;
  createdAt: string;
  attachments: Attachment[] | null;
}

export interface TicketDetail extends Ticket {
  description: string;
  messages: Message[];
  attachments: Attachment[];
}

export interface SessionInfo {
  hostname: string;
  username: string;
  clientName: string;
  siteName: string;
  connected: boolean;
  realtime: boolean;
  error?: string;
}

export interface CreateResult {
  ticket: Ticket;
  screenshotAttached: boolean;
  screenshotError?: string;
}

export interface TicketMessageEvent {
  ticketId: number;
  message: Message;
}

export interface NavigateEvent {
  view: 'new' | 'ticket' | 'list';
  id: number;
}

export type Unsubscribe = () => void;

/** Contrato entre a interface e o processo Go (ou o simulador usado no desenvolvimento). */
export interface Backend {
  session(): Promise<SessionInfo>;
  listTickets(): Promise<Ticket[]>;
  getTicket(id: number): Promise<TicketDetail>;
  createTicket(title: string, description: string, includeScreenshot: boolean): Promise<CreateResult>;
  sendMessage(ticketId: number, body: string): Promise<Message>;
  attachment(ticketId: number, attachmentId: number): Promise<string>;
  onTicketMessage(cb: (e: TicketMessageEvent) => void): Unsubscribe;
  onTicketChanged(cb: (t: Ticket) => void): Unsubscribe;
  onConnection(cb: (realtime: boolean) => void): Unsubscribe;
  onNavigate(cb: (e: NavigateEvent) => void): Unsubscribe;
  onRefresh(cb: () => void): Unsubscribe;
}
