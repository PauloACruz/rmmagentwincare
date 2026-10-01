import { displayUser } from '../lib/format';
import type { SessionInfo } from '../lib/types';

export function Header({ session }: { session: SessionInfo | null }) {
  const user = session?.username ? displayUser(session.username) : '';
  return (
    <header className="header">
      <div className="logo" aria-hidden="true">+</div>
      <div className="header-text">
        <h1>{user ? `Olá, ${user}` : 'WinCare'}</h1>
        {session?.hostname && (
          <p className="muted">
            {session.hostname}
            {session.clientName ? ` · ${session.clientName}` : ''}
          </p>
        )}
      </div>
      {session?.connected && (
        <span
          className={`dot ${session.realtime ? 'dot-on' : 'dot-off'}`}
          title={session.realtime ? 'Conectado em tempo real' : 'Reconectando'}
          aria-label={session.realtime ? 'Conectado em tempo real' : 'Reconectando'}
        />
      )}
    </header>
  );
}
