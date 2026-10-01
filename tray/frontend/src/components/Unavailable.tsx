export function Unavailable({ message, onRetry, retrying }: { message: string; onRetry: () => void; retrying: boolean }) {
  return (
    <section className="empty" role="alert">
      <h2>Serviço WinCare indisponível</h2>
      {message && message !== 'Serviço WinCare indisponível' && <p className="muted">{message}</p>}
      <p className="muted">Tentaremos de novo a cada 30 segundos.</p>
      <button type="button" className="btn btn-secondary" onClick={onRetry} disabled={retrying}>
        {retrying ? 'Tentando...' : 'Tentar agora'}
      </button>
    </section>
  );
}
