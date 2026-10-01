import { useState, type SyntheticEvent } from 'react';

interface Props {
  onSubmit: (title: string, description: string, includeScreenshot: boolean) => Promise<void>;
  onCancel: () => void;
  initialTitle?: string;
  initialDescription?: string;
}

export function NewTicketForm({ onSubmit, onCancel, initialTitle = '', initialDescription = '' }: Props) {
  const [title, setTitle] = useState(initialTitle);
  const [description, setDescription] = useState(initialDescription);
  const [screenshot, setScreenshot] = useState(true);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');

  const trimmed = title.trim();
  const titleInvalid = trimmed.length > 0 && trimmed.length < 3;

  async function submit(e: SyntheticEvent) {
    e.preventDefault();
    if (trimmed.length < 3) {
      setError('O título deve ter pelo menos 3 caracteres.');
      return;
    }
    setSending(true);
    setError('');
    try {
      await onSubmit(trimmed, description.trim(), screenshot);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Não foi possível abrir o chamado.');
      setSending(false);
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">
        <button type="button" className="btn btn-link" onClick={onCancel} disabled={sending}>
          ‹ Voltar
        </button>
        <h2>Abrir chamado</h2>
      </div>
      <form className="form" onSubmit={(e) => { void submit(e); }}>
        <label htmlFor="title">Título</label>
        <input
          id="title"
          value={title}
          maxLength={200}
          placeholder="Resuma o problema"
          onChange={(e) => { setTitle(e.target.value); }}
          aria-invalid={titleInvalid}
          disabled={sending}
          autoFocus
          required
        />
        {titleInvalid && <p className="field-hint error">Use pelo menos 3 caracteres.</p>}

        <label htmlFor="description">Descrição</label>
        <textarea
          id="description"
          value={description}
          maxLength={20000}
          rows={7}
          placeholder="Conte o que aconteceu, o que você estava fazendo e se apareceu alguma mensagem."
          onChange={(e) => { setDescription(e.target.value); }}
          disabled={sending}
        />

        <label className="checkbox">
          <input type="checkbox" checked={screenshot} onChange={(e) => { setScreenshot(e.target.checked); }} disabled={sending} />
          Anexar captura da tela
        </label>
        {screenshot && (
          <p className="field-hint muted">A janela do WinCare some por um instante enquanto a tela é capturada.</p>
        )}

        {error && <p className="error" role="alert">{error}</p>}

        <button type="submit" className="btn btn-primary btn-block" disabled={sending || trimmed.length < 3}>
          {sending ? 'Enviando...' : 'Enviar'}
        </button>
      </form>
    </section>
  );
}
