import type { Ticket } from './types';

// Guarda, por chamado, ate quando o usuario ja viu a conversa (para o indicador de mensagem nova).
const KEY = 'wincare.seen';

function load(): Record<string, string> {
  try {
    const raw = localStorage.getItem(KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : {};
    return typeof parsed === 'object' && parsed !== null ? (parsed as Record<string, string>) : {};
  } catch {
    return {};
  }
}

export function markSeen(ticketId: number, at: string): void {
  const all = load();
  const prev = all[String(ticketId)];
  if (prev && prev >= at) return;
  all[String(ticketId)] = at;
  try {
    localStorage.setItem(KEY, JSON.stringify(all));
  } catch {
    // Sem armazenamento local o indicador volta a aparecer depois de reiniciar; nao e critico.
  }
}

export function hasUnread(t: Ticket): boolean {
  if (!t.lastMessageAt) return false;
  const seen = load()[String(t.id)] ?? t.createdAt;
  return new Date(t.lastMessageAt).getTime() > new Date(seen).getTime() + 1000;
}
