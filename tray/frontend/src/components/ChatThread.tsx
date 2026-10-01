import { useEffect, useRef } from 'react';
import { formatTime } from '../lib/format';
import type { Message } from '../lib/types';
import { AttachmentImage } from './AttachmentImage';

interface Props {
  ticketId: number;
  messages: Message[];
}

export function ChatThread({ ticketId, messages }: Props) {
  const endRef = useRef<HTMLLIElement>(null);
  const last = messages.at(-1)?.id;

  useEffect(() => {
    endRef.current?.scrollIntoView({ block: 'end' });
  }, [last]);

  if (messages.length === 0) {
    return <p className="muted center chat-empty">Nenhuma mensagem ainda.</p>;
  }

  return (
    <ol className="chat" aria-live="polite" aria-label="Conversa">
      {messages.map((m) => (
        <li key={m.id} className={`msg msg-${m.authorType}`} data-author={m.authorType}>
          {m.authorType === 'system' ? (
            <span className="msg-system-text">
              {m.body} · {formatTime(m.createdAt)}
            </span>
          ) : (
            <div className="bubble">
              {m.authorType === 'technician' && <div className="msg-author">{m.authorName}</div>}
              <div className="msg-body">{m.body}</div>
              {(m.attachments ?? [])
                .filter((a) => a.contentType.startsWith('image/'))
                .map((a) => (
                  <AttachmentImage key={a.id} ticketId={ticketId} attachment={a} />
                ))}
              <time className="msg-time" dateTime={m.createdAt}>
                {formatTime(m.createdAt)}
              </time>
            </div>
          )}
        </li>
      ))}
      <li ref={endRef} className="chat-end" aria-hidden="true" />
    </ol>
  );
}
