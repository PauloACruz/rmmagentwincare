import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import type { Message } from '../lib/types';
import { ChatThread } from './ChatThread';
import { CHAT_LOCKED_TEXT, Composer } from './Composer';

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});
afterEach(cleanup);

const messages: Message[] = [
  { id: 1, authorType: 'system', authorName: 'Sistema', body: 'Admin atribuiu o chamado a Ana', createdAt: '2026-10-01T10:00:00Z', attachments: [] },
  { id: 2, authorType: 'technician', authorName: 'Ana', body: 'Olá, posso ajudar?', createdAt: '2026-10-01T10:01:00Z', attachments: [] },
  { id: 3, authorType: 'requester', authorName: 'maria', body: 'Sim, obrigada!', createdAt: '2026-10-01T10:02:00Z', attachments: [] },
];

describe('Chat', () => {
  it('posiciona mensagens por autor', () => {
    render(<ChatThread ticketId={1} messages={messages} />);
    const items = screen.getAllByRole('listitem').filter((li) => li.dataset.author);
    expect(items.map((li) => li.className)).toEqual(['msg msg-system', 'msg msg-technician', 'msg msg-requester']);
    expect(screen.getByText('Ana')).toBeTruthy();
    expect(screen.getByText(/Admin atribuiu o chamado/)).toBeTruthy();
  });

  it('bloqueia o campo de mensagem sem técnico atribuído', () => {
    const { rerender } = render(<Composer enabled={false} onSend={vi.fn()} />);
    expect(screen.getByText(CHAT_LOCKED_TEXT)).toBeTruthy();
    expect(screen.queryByRole('textbox')).toBeNull();
    rerender(<Composer enabled onSend={vi.fn()} />);
    expect(screen.getByRole('textbox', { name: 'Mensagem' })).toBeTruthy();
  });
});
