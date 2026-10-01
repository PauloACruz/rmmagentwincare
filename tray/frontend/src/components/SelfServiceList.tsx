import { useState } from 'react';
import { errorMessage } from '../lib/backend';
import type { SelfServiceTask } from '../lib/types';

interface Props {
  tasks: SelfServiceTask[];
  /** Ha uma execucao em andamento: nenhuma outra acao pode comecar. */
  locked: boolean;
  onRun: (task: SelfServiceTask) => Promise<void>;
}

const taskId = (t: SelfServiceTask) => `${t.module}.${t.key}`;

export function SelfServiceList({ tasks, locked, onRun }: Props) {
  const [confirming, setConfirming] = useState('');
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState('');

  async function run(task: SelfServiceTask) {
    setStarting(true);
    setError('');
    try {
      await onRun(task);
      setConfirming('');
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setStarting(false);
    }
  }

  if (tasks.length === 0) {
    return <p className="muted center">Nenhuma ação liberada no momento.</p>;
  }

  return (
    <>
      {error && <p className="error" role="alert">{error}</p>}
      <ul className="ss-list">
        {tasks.map((t) => {
          const id = taskId(t);
          const isConfirming = confirming === id;
          return (
            <li key={id} className="ss-task">
              <div className="ss-task-text">
                <h3>{t.label}</h3>
                {t.description && <p className="muted">{t.description}</p>}
              </div>
              {isConfirming ? (
                <div className="ss-confirm" role="group" aria-label={`Confirmar ${t.label}`}>
                  <p>Executar agora? O computador pode ficar mais lento por alguns minutos.</p>
                  <div className="ss-actions">
                    <button type="button" className="btn btn-secondary" onClick={() => { setConfirming(''); }} disabled={starting}>
                      Cancelar
                    </button>
                    <button type="button" className="btn btn-primary" onClick={() => { void run(t); }} disabled={starting}>
                      {starting ? 'Iniciando...' : 'Confirmar'}
                    </button>
                  </div>
                </div>
              ) : (
                <button
                  type="button"
                  className="btn btn-primary"
                  aria-label={`Executar ${t.label}`}
                  onClick={() => { setError(''); setConfirming(id); }}
                  disabled={locked || starting}
                >
                  Executar
                </button>
              )}
            </li>
          );
        })}
      </ul>
    </>
  );
}
