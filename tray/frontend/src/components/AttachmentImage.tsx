import { useEffect, useState } from 'react';
import { useBackend } from '../lib/backend';
import type { Attachment } from '../lib/types';

export function AttachmentImage({ ticketId, attachment }: { ticketId: number; attachment: Attachment }) {
  const backend = useBackend();
  const [src, setSrc] = useState('');
  const [failed, setFailed] = useState(false);
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    let alive = true;
    backend.attachment(ticketId, attachment.id).then(
      (url) => { if (alive) setSrc(url); },
      () => { if (alive) setFailed(true); },
    );
    return () => { alive = false; };
  }, [backend, ticketId, attachment.id]);

  if (failed) return <p className="muted small">Não foi possível carregar {attachment.fileName}.</p>;
  if (!src) return <div className="thumb thumb-loading" aria-label="Carregando imagem" />;
  return (
    <button
      type="button"
      className={`thumb-button${expanded ? ' expanded' : ''}`}
      onClick={() => { setExpanded((v) => !v); }}
      title={expanded ? 'Reduzir' : 'Ampliar'}
    >
      <img className="thumb" src={src} alt={attachment.fileName} />
    </button>
  );
}
