import type { TicketStatus } from './types';

export const STATUS_LABEL: Record<TicketStatus, string> = {
  new: 'Novo',
  in_progress: 'Em atendimento',
  waiting_user: 'Aguardando usuário',
  resolved: 'Resolvido',
  closed: 'Fechado',
};

const dateTime = new Intl.DateTimeFormat('pt-BR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' });
const timeOnly = new Intl.DateTimeFormat('pt-BR', { hour: '2-digit', minute: '2-digit' });

export function formatDateTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : dateTime.format(d);
}

export function formatTime(iso: string): string {
  const d = new Date(iso);
  const today = new Date();
  if (Number.isNaN(d.getTime())) return '';
  return d.toDateString() === today.toDateString() ? timeOnly.format(d) : dateTime.format(d);
}

/** No Windows o agente informa DOMINIO\usuario; a saudacao usa so o usuario. */
export function displayUser(username: string): string {
  const i = username.lastIndexOf('\\');
  return i >= 0 ? username.slice(i + 1) : username;
}
