import { isFinalStatus, RUN_STATUS_LABEL, type ActiveRun } from '../lib/selfService';

interface Props {
  run: ActiveRun;
  onOpenTicket: () => void;
  onDismiss: () => void;
}

export function SelfServiceProgress({ run, onOpenTicket, onDismiss }: Props) {
  const final = isFinalStatus(run.status);
  const solved = run.status === 'ok';
  return (
    <section className={`ss-run ss-run-${run.status}`} aria-label={`Execução de ${run.task.label}`}>
      <div className="ss-run-head">
        <h3>{run.task.label}</h3>
        <span className={`badge ss-badge-${run.status}`}>{RUN_STATUS_LABEL[run.status]}</span>
      </div>
      <div
        className="ss-bar"
        role="progressbar"
        aria-label="Progresso"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={run.progress}
      >
        <div className="ss-bar-fill" style={{ width: `${String(run.progress)}%` }} />
      </div>
      <p className="ss-message muted" aria-live="polite">
        {run.message || (final ? '' : 'Iniciando...')}
        {!final && <span className="ss-percent"> {run.progress}%</span>}
      </p>
      {final && (
        <div className="ss-result" role="status">
          <p>
            {solved
              ? 'Pronto! A ação terminou com sucesso.'
              : 'A ação terminou sem resolver tudo. Se o problema continuar, abra um chamado e um técnico ajuda você.'}
          </p>
          <div className="ss-actions">
            <button type="button" className="btn btn-secondary" onClick={onDismiss}>
              Fechar
            </button>
            {!solved && (
              <button type="button" className="btn btn-primary" onClick={onOpenTicket}>
                Abrir chamado
              </button>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
